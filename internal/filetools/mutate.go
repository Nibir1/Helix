// internal/filetools/mutate.go
// Purpose: the two tools that change a file — exact-snippet edit, and atomic
// whole-file write.
//
// These are separated from the read tools because they are gated differently
// and for a different reason: a read spends context, a write changes the
// machine. The agent grades every call here as medium risk, which under the
// default posture means it asks first.
package filetools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Edit performs an exact string replacement inside one file and reports how
// many occurrences it changed.
//
// THE UNIQUENESS REQUIREMENT IS THE WHOLE POINT, and it is what `sed -i` could
// never give the planner. If old appears more than once the call fails rather
// than guessing which occurrence was meant — a wrong guess edits working code
// and nothing reports it. The model's remedy is to include more surrounding
// lines until the snippet is unique, or to pass replaceAll when it genuinely
// means every occurrence. A snippet that appears zero times fails too, where
// sed would have exited 0 and reported success.
func Edit(r Resolver, path, old, updated string, replaceAll bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("edit_file requires a 'path' argument")
	}
	if old == "" {
		return "", fmt.Errorf("edit_file requires a non-empty 'old_string'. To create a file or replace it wholesale, use write_file")
	}
	if old == updated {
		return "", fmt.Errorf("'old_string' and 'new_string' are identical — nothing to do")
	}

	p, err := resolve(r, path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p) //nolint:gosec // confined by the Resolver
	if err != nil {
		return "", err
	}
	content := string(data)

	count := strings.Count(content, old)
	switch {
	case count == 0:
		return "", fmt.Errorf("'old_string' was not found in %s. Read the file first and copy the snippet exactly, including indentation",
			filepath.ToSlash(path))
	case count > 1 && !replaceAll:
		return "", fmt.Errorf("'old_string' appears %d times in %s. Include more surrounding context so the snippet is unique, or pass replace_all=true to change every occurrence",
			count, filepath.ToSlash(path))
	}

	var result string
	replacements := 1
	if replaceAll {
		result = strings.ReplaceAll(content, old, updated)
		replacements = count
	} else {
		result = strings.Replace(content, old, updated, 1)
	}

	if err := AtomicWrite(p, result); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s (%d replacement%s)",
		filepath.ToSlash(path), replacements, plural(replacements)), nil
}

// Write replaces a whole file's contents atomically.
func Write(r Resolver, path, content string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("write_file requires a 'path' argument")
	}
	p, err := resolve(r, path)
	if err != nil {
		return "", err
	}

	// TYPO GUARD (Synapse's, and it earns its keep). Creating a NEW file whose
	// name is a near-miss of an existing sibling — "hepers.go" beside
	// "helpers.go" — is almost always a path mistake by the model. Left alone
	// it writes a stray file, reports success, and the edit the user asked for
	// is nowhere. Refusing with the suggestion costs one round-trip and names
	// the file that was probably meant.
	existed := true
	if _, serr := os.Stat(p); os.IsNotExist(serr) {
		existed = false
		if suggestion := nearestSiblingFile(p); suggestion != "" {
			return "", fmt.Errorf(
				"refusing to create %q — the very similar %q already exists in the same directory. "+
					"If you meant that file, call write_file again with that exact name. If you really do want a new file, "+
					"create it first with a shell step (touch %s) and then write it",
				filepath.ToSlash(path), suggestion, filepath.ToSlash(path))
		}
	}

	if err := AtomicWrite(p, content); err != nil {
		return "", err
	}
	verb := "wrote"
	if !existed {
		verb = "created"
	}
	return fmt.Sprintf("%s %s (%d bytes)", verb, filepath.ToSlash(path), len(content)), nil
}

// AtomicWrite writes content to path via a temporary file in the same
// directory and one rename, preserving an existing file's permissions.
//
// Ported from Synapse's filesystem.AtomicWrite. The property that matters: a
// crash, a full disk or a killed process leaves the original file intact rather
// than a half-written one. A tool the planner calls in a loop gets this wrong
// often enough to matter, and a truncated source file is worse than a failed
// edit.
func AtomicWrite(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".helix-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeds

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// Preserve the existing file's permissions, defaulting to 0644 for new
	// files. Without this an atomic write silently relaxes a 0600 file to
	// whatever CreateTemp chose, which for a key or a config is a real change.
	perm := os.FileMode(0o644)
	if info, serr := os.Stat(path); serr == nil {
		perm = info.Mode().Perm()
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("set permissions: %w", err)
	}
	return os.Rename(tmpName, path)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// nearestSiblingFile returns the existing file in the same directory whose name
// is within a small edit distance of the target, or "" if none is close.
func nearestSiblingFile(target string) string {
	dir := filepath.Dir(target)
	base := strings.ToLower(filepath.Base(target))
	entries, err := os.ReadDir(dir)
	if err != nil || base == "" {
		return ""
	}
	best, bestDist := "", 3
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if name == base {
			continue // an exact match exists — not a typo case
		}
		if absInt(len(name)-len(base)) > 2 {
			continue
		}
		if d := editDistance(name, base); d < bestDist {
			best, bestDist = e.Name(), d
		}
	}
	return best
}

// editDistance is a plain Levenshtein distance on lowercase names (short
// strings only).
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = minInt(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func minInt(xs ...int) int {
	m := xs[0]
	for _, x := range xs[1:] {
		if x < m {
			m = x
		}
	}
	return m
}
