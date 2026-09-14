// internal/filetools/filetools_test.go
// Purpose: pin the properties that make these tools safer than shelling out.
package filetools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// jail is a Resolver with the one property that matters: paths resolve inside
// root and nothing else does. It stands in for DirectorySandbox, which needs a
// process-wide working directory this package must not depend on.
type jail struct{ root string }

func (j jail) ValidateSafePath(target string) (string, error) {
	p := target
	if !filepath.IsAbs(p) {
		p = filepath.Join(j.root, p)
	}
	p = filepath.Clean(p)
	rel, err := filepath.Rel(j.root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %s is outside root %s", target, j.root)
	}
	return p, nil
}

func newJail(t *testing.T) (jail, string) {
	t.Helper()
	root := t.TempDir()
	return jail{root: root}, root
}

func write(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return p
}

// ---------------------------------------------------------------- confinement

// A file tool with no confinement is the whole vulnerability, so a missing
// Resolver must refuse rather than default to "anywhere".
func TestEveryToolRefusesWithoutAResolver(t *testing.T) {
	cases := map[string]func() error{
		"Read":  func() error { _, err := Read(nil, "x"); return err },
		"List":  func() error { _, err := List(nil, "."); return err },
		"Glob":  func() error { _, err := Glob(nil, "*", "."); return err },
		"Grep":  func() error { _, err := Grep(nil, "x", "."); return err },
		"Edit":  func() error { _, err := Edit(nil, "x", "a", "b", false); return err },
		"Write": func() error { _, err := Write(nil, "x", "c"); return err },
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatal("proceeded with no sandbox resolver")
			}
			if !strings.Contains(err.Error(), "sandbox") {
				t.Errorf("error does not say confinement was missing: %v", err)
			}
		})
	}
}

// The Resolver's refusal must actually stop the work, not merely be consulted.
func TestToolsCannotReachOutsideTheJail(t *testing.T) {
	j, root := newJail(t)
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	if _, err := Read(j, "../outside.txt"); err == nil {
		t.Error("read escaped the jail via ..")
	}
	if _, err := Read(j, outside); err == nil {
		t.Error("read escaped the jail via an absolute path")
	}
	if _, err := Write(j, "../escaped.txt", "x"); err == nil {
		t.Error("write escaped the jail")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escaped.txt")); err == nil {
		t.Error("a file was created outside the jail")
	}
	// And the refusal did not corrupt what was already there.
	if b, _ := os.ReadFile(outside); string(b) != "secret" {
		t.Error("a file outside the jail was modified")
	}
}

// ----------------------------------------------------------------- edit_file

// The reason edit_file exists instead of `sed -i`: a pattern that does not
// match must FAIL. sed exits 0 and changes nothing, so the model is told the
// edit succeeded when the file is untouched.
func TestEditFailsLoudlyWhenTheSnippetIsAbsent(t *testing.T) {
	j, root := newJail(t)
	write(t, root, "main.go", "package main\n\nfunc main() {}\n")

	_, err := Edit(j, "main.go", "func other()", "func changed()", false)
	if err == nil {
		t.Fatal("an absent snippet reported success — this is the sed -i failure mode")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error does not say the snippet was absent: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if string(b) != "package main\n\nfunc main() {}\n" {
		t.Error("the file changed despite the failure")
	}
}

// An ambiguous snippet must not be guessed at. Editing the wrong one of six
// occurrences changes working code and nothing reports it.
func TestEditRefusesAnAmbiguousSnippet(t *testing.T) {
	j, root := newJail(t)
	write(t, root, "a.go", "x := 1\ny := 1\nz := 1\n")

	_, err := Edit(j, "a.go", "1", "2", false)
	if err == nil {
		t.Fatal("an ambiguous snippet was edited anyway")
	}
	if !strings.Contains(err.Error(), "3 times") {
		t.Errorf("error does not say how many occurrences there were: %v", err)
	}
	if !strings.Contains(err.Error(), "replace_all") {
		t.Errorf("error does not name the way out: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if strings.Contains(string(b), "2") {
		t.Error("an ambiguous edit was applied")
	}
}

func TestEditReplacesOnceAndReportsTheCount(t *testing.T) {
	j, root := newJail(t)
	write(t, root, "a.go", "alpha\nbeta\nalpha\n")

	msg, err := Edit(j, "a.go", "beta", "gamma", false)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if !strings.Contains(msg, "1 replacement") {
		t.Errorf("result does not report the count: %q", msg)
	}
	b, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(b) != "alpha\ngamma\nalpha\n" {
		t.Errorf("wrong content after edit: %q", b)
	}
}

func TestEditReplaceAllChangesEveryOccurrence(t *testing.T) {
	j, root := newJail(t)
	write(t, root, "a.go", "alpha\nbeta\nalpha\n")

	msg, err := Edit(j, "a.go", "alpha", "omega", true)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if !strings.Contains(msg, "2 replacements") {
		t.Errorf("result does not report 2 replacements: %q", msg)
	}
	b, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(b) != "omega\nbeta\nomega\n" {
		t.Errorf("wrong content: %q", b)
	}
}

// An edit that would change nothing is a wasted round-trip the model should be
// told about, not a silent success it will believe.
func TestEditRefusesANoOp(t *testing.T) {
	j, root := newJail(t)
	write(t, root, "a.go", "same\n")
	if _, err := Edit(j, "a.go", "same", "same", false); err == nil {
		t.Fatal("an identical replacement reported success")
	}
}

// Permissions must survive an edit. A 0600 file silently relaxed to 0644 by a
// tool call is a real change nobody asked for.
func TestEditPreservesFilePermissions(t *testing.T) {
	j, root := newJail(t)
	p := write(t, root, "secret.txt", "old\n")
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := Edit(j, "secret.txt", "old", "new", false); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("permissions became %v; a 0600 file was relaxed by an edit", info.Mode().Perm())
	}
}

// ---------------------------------------------------------------- write_file

func TestWriteCreatesAndReports(t *testing.T) {
	j, root := newJail(t)
	msg, err := Write(j, "new/deep/file.txt", "hello")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(msg, "created") {
		t.Errorf("a new file was not reported as created: %q", msg)
	}
	b, err := os.ReadFile(filepath.Join(root, "new/deep/file.txt"))
	if err != nil || string(b) != "hello" {
		t.Errorf("content = %q, err = %v", b, err)
	}
}

// The typo guard. Writing to a misspelled path creates a stray file, reports
// success, and loses the edit somewhere nobody will look.
func TestWriteRefusesANearMissOfAnExistingSibling(t *testing.T) {
	j, root := newJail(t)
	write(t, root, "helpers.go", "package main\n")

	_, err := Write(j, "hepers.go", "package main\n")
	if err == nil {
		t.Fatal("a near-miss filename was created silently")
	}
	if !strings.Contains(err.Error(), "helpers.go") {
		t.Errorf("error does not name the file that was probably meant: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(root, "hepers.go")); serr == nil {
		t.Error("the stray file was created anyway")
	}
}

// The guard must not block a genuinely new file that simply shares a directory.
func TestWriteAllowsAnUnrelatedNewFile(t *testing.T) {
	j, root := newJail(t)
	write(t, root, "helpers.go", "package main\n")

	if _, err := Write(j, "transport_layer.go", "package main\n"); err != nil {
		t.Fatalf("an unrelated new file was refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "transport_layer.go")); err != nil {
		t.Errorf("file was not created: %v", err)
	}
}

// Overwriting an existing file is not a typo case at all.
func TestWriteOverwritesAnExistingFileWithoutComplaint(t *testing.T) {
	j, root := newJail(t)
	write(t, root, "helpers.go", "old\n")
	msg, err := Write(j, "helpers.go", "new\n")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(msg, "wrote") {
		t.Errorf("an overwrite was reported as %q", msg)
	}
	b, _ := os.ReadFile(filepath.Join(root, "helpers.go"))
	if string(b) != "new\n" {
		t.Errorf("content = %q", b)
	}
}

// ------------------------------------------------------------- read and list

func TestReadTruncatesAndRefusesBinaryAndDirectories(t *testing.T) {
	j, root := newJail(t)

	write(t, root, "big.txt", strings.Repeat("a", MaxReadBytes+500))
	out, err := Read(j, "big.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.HasSuffix(out, "[truncated]") {
		t.Error("an oversized file was not marked truncated")
	}
	if len(out) > MaxReadBytes+64 {
		t.Errorf("truncation did not bound the result: %d bytes", len(out))
	}

	if err := os.WriteFile(filepath.Join(root, "bin"), []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatalf("seed binary: %v", err)
	}
	if _, err := Read(j, "bin"); err == nil {
		t.Error("a binary file was read into the context window")
	}

	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := Read(j, "sub"); err == nil {
		t.Error("a directory was read as a file")
	}
}

func TestListMarksDirectoriesAndBounds(t *testing.T) {
	j, root := newJail(t)
	write(t, root, "a.txt", "x")
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	out, err := List(j, ".")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(out, "sub/") {
		t.Errorf("directories are not marked: %q", out)
	}
	if !strings.Contains(out, "a.txt") {
		t.Errorf("file missing: %q", out)
	}

	many := t.TempDir()
	for i := 0; i < MaxListEntries+10; i++ {
		write(t, many, fmt.Sprintf("f%03d.txt", i), "x")
	}
	out, err = List(jail{root: many}, ".")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(out, "more entries") {
		t.Error("a large directory was not bounded")
	}
}

// ------------------------------------------------------------- glob and grep

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "internal/ai/planner.go", true}, // no separator: matches the base name
		{"internal/*.go", "internal/x.go", true},
		{"internal/*.go", "internal/ai/x.go", false},
		{"**/*.go", "a/b/c.go", true},
		{"**/*.go", "c.go", true},
		{"**/*_test.go", "internal/ai/planner_test.go", true},
		{"**/*_test.go", "internal/ai/planner.go", false},
		{"internal/**/*.go", "internal/ai/planner.go", true},
		{"internal/**/*.go", "cmd/helix/main.go", false},
		{"internal/**", "internal/anything/at/all.txt", true},
		{"a/**/b", "a/b", true}, // the separator around ** is optional
		{"a/**/b", "a/x/y/b", true},
	}
	for _, c := range cases {
		if got := MatchGlob(c.pattern, c.name); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

// Generated and vendored trees are pruned. A grep that spends its match budget
// inside node_modules has answered a question nobody asked.
func TestGlobAndGrepSkipGeneratedTrees(t *testing.T) {
	j, root := newJail(t)
	write(t, root, "real.go", "package main // needle\n")
	write(t, root, "node_modules/pkg/index.go", "package pkg // needle\n")
	write(t, root, ".git/config.go", "package git // needle\n")

	g, err := Glob(j, "**/*.go", ".")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if !strings.Contains(g, "real.go") {
		t.Errorf("the real file is missing: %q", g)
	}
	if strings.Contains(g, "node_modules") || strings.Contains(g, ".git") {
		t.Errorf("a pruned directory was walked: %q", g)
	}

	m, err := Grep(j, "needle", ".")
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if !strings.Contains(m, "real.go:1:") {
		t.Errorf("grep missed the real match or dropped file:line: %q", m)
	}
	if strings.Contains(m, "node_modules") {
		t.Errorf("grep walked a pruned directory: %q", m)
	}
}

func TestGrepIsCaseInsensitiveAndReportsNothingClearly(t *testing.T) {
	j, root := newJail(t)
	write(t, root, "a.txt", "The Needle Is Here\n")

	m, err := Grep(j, "needle", ".")
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if !strings.Contains(m, "a.txt:1:") {
		t.Errorf("case-insensitive match failed: %q", m)
	}

	m, err = Grep(j, "haystack", ".")
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if m != "(no matches)" {
		t.Errorf("an empty result should say so plainly, got %q", m)
	}
}

func TestGrepBoundsItsOutput(t *testing.T) {
	j, root := newJail(t)
	var sb strings.Builder
	for i := 0; i < MaxGrepMatches*3; i++ {
		sb.WriteString("needle\n")
	}
	write(t, root, "many.txt", sb.String())

	m, err := Grep(j, "needle", ".")
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if !strings.Contains(m, "narrow the pattern") {
		t.Error("an unbounded grep result was returned")
	}
	if n := strings.Count(m, "many.txt:"); n > MaxGrepMatches {
		t.Errorf("%d matches returned, cap is %d", n, MaxGrepMatches)
	}
}

// Most recently modified first — the ordering is the useful part of glob.
func TestGlobOrdersMostRecentlyModifiedFirst(t *testing.T) {
	j, root := newJail(t)
	write(t, root, "old.go", "x")
	write(t, root, "new.go", "x")
	oldTime := mustTime(t, "2020-01-01")
	if err := os.Chtimes(filepath.Join(root, "old.go"), oldTime, oldTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	out, err := Glob(j, "*.go", ".")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 || lines[0] != "new.go" {
		t.Errorf("glob did not put the recently modified file first: %q", out)
	}
}

func mustTime(t *testing.T, day string) time.Time {
	t.Helper()
	p, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	return p
}
