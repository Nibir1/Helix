// internal/filetools/filetools.go
// Purpose: the file tools the harness was missing — read, list, glob, grep,
// exact-snippet edit, and atomic whole-file write.
//
// WHAT THIS REPLACES. Until now the planner had no way to touch a file except
// through `shell`, and the planner prompt said so out loud:
//
//   - For in-place file editing on macOS, use: sed -i ” 's/OLD/NEW/g' FILE
//   - Alternative: perl -pi -e "s/OLD/NEW/g" FILE
//
// That is a bad instruction for a model, and not because sed is bad. A `sed -i`
// whose pattern does not match exits 0 and changes nothing, so the model is
// told the edit succeeded when it did not. It cannot tell one occurrence from
// six. Quoting a snippet of real code through a shell line means escaping it
// twice. And reading a file to decide what to change meant `cat`, which spends
// the whole file on the context window whether or not it was needed.
//
// Ported from Synapse's agentloop (internal/agentloop/tools_edit.go and the
// tool bodies in agentloop.go), whose edit_file rationale is exactly right and
// worth restating: with write_file as the only mutation tool, changing a line
// in a 2,000-line file means re-emitting all 2,000 lines, and any line the
// model mis-remembers is silently overwritten. A replacement keyed on an exact
// snippet either matches what is on disk or fails and says so.
//
// WHAT CHANGED IN THE PORT. Synapse confines paths with its own ResolveInDir,
// a prefix check against the working directory. Helix already has a stronger
// one — DirectorySandbox.ValidateSafePath resolves symlinks on both sides,
// compares case-insensitively for macOS and Windows, and handles a target that
// does not exist yet by validating its parent. So this package does NOT carry
// its own path check; it takes a Resolver and refuses to run without one. A
// second, weaker copy of a confinement rule is how a jail grows a door.
//
// Nothing here decides whether an operation is ALLOWED. Risk tiers, the
// approval posture, the Voice Risk Policy and hooks all live in the agent and
// run before these functions are called. This package does the work and reports
// what it did.
package filetools

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Resolver turns a tool-supplied path into an absolute path that is safe to
// touch, or returns an error explaining why it is not.
//
// An interface rather than a function so the sandbox can be passed directly,
// and so a test cannot accidentally satisfy it with a no-op closure that looks
// like confinement while providing none.
type Resolver interface {
	ValidateSafePath(target string) (string, error)
}

// Bounds. Every one of these exists because a tool result is spent on the
// context window: an unbounded read of a vendored bundle, or a grep for "e",
// would push out the conversation that asked for it.
const (
	MaxListEntries = 200  // one list_dir result
	MaxGlobResults = 200  // one glob result
	MaxGrepMatches = 60   // one grep result
	maxGrepFiles   = 2000 // files visited per grep, matched or not
	maxGrepBytes   = 2 << 20
	maxGrepLine    = 240
)

// A read returns one window of a file, sized to fit the execution report the
// planner reads it from, plus a header saying which lines it holds and how
// long the file is.
//
// It used to return the first 20,000 bytes, and the report then kept the LAST
// 60 lines of that (the right rule for command output, where errors print
// last). Asked about a section near the end of a 963-line document, the
// planner was shown lines 270 to 330, could not answer, and reread and
// re-grepped until its budget ran out: 8 of 42 turns in a driven session.
// Now it sees exactly the lines it is told it has, and how to ask for others.
const (
	ReadWindowLines = 80
	ReadWindowBytes = 6000
	maxReadFileSize = 16 << 20
)

// resolve is the one place a path becomes an absolute path. A nil Resolver is
// a programming error rather than a permissive default: a file tool with no
// confinement is the whole vulnerability.
func resolve(r Resolver, path string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("no sandbox resolver configured — refusing to touch the filesystem")
	}
	return r.ValidateSafePath(path)
}

// Read returns the first window of one file (ReadLines from line 1).
func Read(r Resolver, path string) (string, error) { return ReadLines(r, path, 0, 0) }

// ReadLines returns lines start to end of one file (1-based, inclusive),
// at most ReadWindowLines lines and ReadWindowBytes bytes of them, under a
// header naming the range and the file's length, and with a pointer to the
// next window when the file goes on. start <= 0 means line 1; end <= 0 means
// as far as the window allows. Lines are returned verbatim, with no number
// prefixes, so a snippet copied from a read still matches the file byte for
// byte in an edit.
func ReadLines(r Resolver, path string, start, end int) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("read_file requires a 'path' argument")
	}
	p, err := resolve(r, path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory — use list_dir", filepath.ToSlash(path))
	}
	if info.Size() > maxReadFileSize {
		return "", fmt.Errorf("%s is %d bytes, too large to read; grep it instead", filepath.ToSlash(path), info.Size())
	}
	data, err := os.ReadFile(p) //nolint:gosec // confined by the Resolver
	if err != nil {
		return "", err
	}
	// Binary content is refused rather than truncated. A prefix of a compiled
	// object is not information, and it can carry byte sequences that break
	// the enclosing report's framing.
	if isBinary(data) {
		return "", fmt.Errorf("%s looks like a binary file", filepath.ToSlash(path))
	}
	name := filepath.ToSlash(path)
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return fmt.Sprintf("[%s: empty file]", name), nil
	}
	lines := strings.Split(text, "\n")
	total := len(lines)
	if start <= 0 {
		start = 1
	}
	if start > total {
		return "", fmt.Errorf("%s has %d lines; start_line %d is past the end", name, total, start)
	}
	if end <= 0 || end > total {
		end = total
	}
	if end < start {
		return "", fmt.Errorf("end_line %d is before start_line %d", end, start)
	}

	var body strings.Builder
	last, used := start-1, 0
	for i := start; i <= end && i-start < ReadWindowLines; i++ {
		line := strings.TrimSuffix(lines[i-1], "\r")
		if used+len(line)+1 > ReadWindowBytes {
			if i > start {
				break
			}
			// A single line longer than the window: show its start.
			line = line[:ReadWindowBytes-40] + " …[line truncated]"
		}
		body.WriteString(line + "\n")
		used += len(line) + 1
		last = i
	}
	out := fmt.Sprintf("[%s: lines %d-%d of %d]\n%s", name, start, last, total, body.String())
	if last < total {
		out += fmt.Sprintf("[more: read again with start_line=%d, or grep for what you need]", last+1)
	}
	return strings.TrimSuffix(out, "\n"), nil
}

// List names the entries of one directory, directories marked with a slash.
func List(r Resolver, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	p, err := resolve(r, path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "(empty directory)", nil
	}
	var sb strings.Builder
	for i, e := range entries {
		if i >= MaxListEntries {
			fmt.Fprintf(&sb, "...[%d more entries]\n", len(entries)-i)
			break
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		sb.WriteString(name)
		sb.WriteString("\n")
	}
	return strings.TrimSuffix(sb.String(), "\n"), nil
}

// Glob lists files matching a pattern, most recently modified first.
//
// Ordering by modification time is deliberate and is Synapse's call: when an
// agent asks "where are the handlers", the files someone touched this week are
// far likelier to be the ones that matter than an alphabetically-first
// vendored fixture.
func Glob(r Resolver, pattern, root string) (string, error) {
	if strings.TrimSpace(pattern) == "" {
		return "", fmt.Errorf("glob requires a 'pattern' argument (e.g. '**/*.go' or 'internal/**/*_test.go')")
	}
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	base, err := resolve(r, root)
	if err != nil {
		return "", err
	}

	type hit struct {
		rel string
		mod int64
	}
	var hits []hit
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if d.IsDir() {
			if p != base && SkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !MatchGlob(pattern, rel) {
			return nil
		}
		var mod int64
		if info, ierr := d.Info(); ierr == nil {
			mod = info.ModTime().UnixNano()
		}
		hits = append(hits, hit{rel: rel, mod: mod})
		return nil
	})
	if len(hits) == 0 {
		return "(no files matched)", nil
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].mod != hits[j].mod {
			return hits[i].mod > hits[j].mod
		}
		return hits[i].rel < hits[j].rel
	})

	var sb strings.Builder
	for i, h := range hits {
		if i >= MaxGlobResults {
			fmt.Fprintf(&sb, "...[%d more matches; narrow the pattern]\n", len(hits)-i)
			break
		}
		sb.WriteString(shown(root, h.rel))
		sb.WriteString("\n")
	}
	return strings.TrimSuffix(sb.String(), "\n"), nil
}

// shown is a result path as the planner should use it: the search root as
// the planner wrote it, joined with the path under it. Results used to be
// relative to the search root alone, so a glob in
// Development/Personal/Metabolism reported judge/pair.go, and the planner's
// next read of that path, from the working directory, failed: a wasted round,
// seen in several replays of real turns. A grep of one file reported its
// lines as ".:840:" for the same reason.
func shown(root, rel string) string {
	root = filepath.ToSlash(strings.TrimSpace(root))
	if root == "" || root == "." {
		return rel
	}
	return path.Join(root, rel)
}

// Grep searches file contents, case-insensitively, and reports file:line:text.
func Grep(r Resolver, pattern, path string) (string, error) {
	if strings.TrimSpace(pattern) == "" {
		return "", fmt.Errorf("grep requires a 'pattern' argument")
	}
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	base, err := resolve(r, path)
	if err != nil {
		return "", err
	}

	needle := strings.ToLower(pattern)
	var sb strings.Builder
	matches, files := 0, 0

	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != base && SkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if matches >= MaxGrepMatches || files >= maxGrepFiles {
			return nil
		}
		files++
		info, ierr := d.Info()
		if ierr != nil || info.Size() > maxGrepBytes {
			return nil
		}
		data, rerr := os.ReadFile(p) //nolint:gosec // confined by the Resolver
		if rerr != nil || isBinary(data) {
			return nil
		}
		rel, _ := filepath.Rel(base, p)
		for n, line := range strings.Split(string(data), "\n") {
			if matches >= MaxGrepMatches {
				break
			}
			if strings.Contains(strings.ToLower(line), needle) {
				trimmed := strings.TrimSpace(line)
				if len(trimmed) > maxGrepLine {
					trimmed = trimmed[:maxGrepLine] + "…"
				}
				fmt.Fprintf(&sb, "%s:%d: %s\n", shown(path, filepath.ToSlash(rel)), n+1, trimmed)
				matches++
			}
		}
		return nil
	})
	if matches >= MaxGrepMatches {
		fmt.Fprintf(&sb, "...[%d+ matches — narrow the pattern]", matches)
	}
	if sb.Len() == 0 {
		return "(no matches)", nil
	}
	return strings.TrimSuffix(sb.String(), "\n"), nil
}

// SkipDir reports whether a directory should be pruned during a walk. These are
// directories whose contents are generated, vendored or enormous; a grep that
// spends its match budget inside node_modules has answered a question nobody
// asked.
func SkipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "dist", "build", "vendor", ".venv", "venv",
		"__pycache__", ".next", ".nuxt", "target", "coverage", ".cache", ".parcel-cache":
		return true
	}
	return false
}

// isBinary performs a cheap NUL-byte sniff on the first 8KB of a file.
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// MatchGlob matches a slash-separated path against a pattern, supporting `**`
// for "any number of path segments".
//
// path.Match cannot express `**`, and `**/*.go` is the single most useful
// pattern an agent writes, so the doublestar case is handled explicitly rather
// than pulling in a dependency — a self-updating shell is the wrong place to
// widen the dependency surface for one wildcard.
//
// path, NOT filepath, for the matching itself. Both inputs are normalised to
// forward slashes above, and filepath.Match takes the separator from the HOST:
// on Windows that is `\`, so `/` stopped being a separator and a single `*`
// matched straight through it — `internal/*.go` matched `internal/ai/x.go`
// there and nowhere else. path.Match is always `/`-separated, which is what
// this function's own contract says it takes.
func MatchGlob(pattern, name string) bool {
	pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "./")
	name = strings.TrimPrefix(filepath.ToSlash(name), "./")

	if !strings.Contains(pattern, "**") {
		// A pattern with no separator matches on the base name, which is what
		// someone writing `*.go` means.
		if !strings.Contains(pattern, "/") {
			ok, err := path.Match(pattern, path.Base(name))
			return err == nil && ok
		}
		ok, err := path.Match(pattern, name)
		return err == nil && ok
	}

	// Split on the first `**` and match the halves. `a/**/b` must match `a/b`
	// too, so the separator around the wildcard is optional.
	idx := strings.Index(pattern, "**")
	prefix := strings.TrimSuffix(pattern[:idx], "/")
	suffix := strings.TrimPrefix(pattern[idx+2:], "/")

	rest := name
	if prefix != "" {
		if !strings.HasPrefix(name, prefix+"/") && name != prefix {
			return false
		}
		rest = strings.TrimPrefix(strings.TrimPrefix(name, prefix), "/")
	}
	if suffix == "" {
		return true // `a/**` matches everything beneath a/
	}
	// The suffix may itself contain `**`; recurse over each possible split.
	if MatchGlob(suffix, rest) {
		return true
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' && MatchGlob(suffix, rest[i+1:]) {
			return true
		}
	}
	return false
}
