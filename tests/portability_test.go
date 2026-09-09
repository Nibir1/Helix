// tests/portability_test.go
//
// Purpose: catch a cross-platform test mistake this repository has actually
// made — four times — at the point it is written rather than three weeks later
// on a Windows runner.
//
// A second check was drafted here and deliberately removed: it flagged any test
// building a shell command from a path variable without filepath.ToSlash. That
// is a real bug (Git Bash on a Windows runner reads the backslashes of
// C:\Users\... as escapes), but the pattern cannot distinguish a command that
// gets EXECUTED from a string that only gets VALIDATED, and it fired on both.
// A guard with false positives teaches people to ignore guards.
//
// Both were invisible for a long time because a lint failure earlier in the
// Windows CI job meant the tests never ran there at all. Fifteen of them were
// failing by the time anything looked.
package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot walks up from this file to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("could not locate the module root from %s: %v", dir, err)
	}
	return dir
}

func goTestFiles(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "dist", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path) //nolint:gosec // repository-local test source
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 20 {
		t.Fatalf("only found %d test files — the walk is broken, so this "+
			"check would pass by finding nothing", len(out))
	}
	return out
}

var setenvHome = regexp.MustCompile(`t\.Setenv\("HOME",`)

// A test that redirects HOME must redirect USERPROFILE too.
//
// os.UserHomeDir() reads %USERPROFILE% on Windows and ignores $HOME entirely,
// so a test that sets only HOME silently keeps using the REAL home directory
// there — reading the developer's own ~/.helix, and failing in ways that look
// like logic bugs. Four tests were doing this.
func TestTestsRedirectingHomeAlsoRedirectUserprofile(t *testing.T) {
	for name, body := range goTestFiles(t) {
		lines := strings.Split(body, "\n")
		for i, line := range lines {
			if !setenvHome.MatchString(line) {
				continue
			}
			// Accept the override anywhere in the surrounding few lines, so
			// the two calls need not be adjacent.
			window := strings.Join(lines[max(0, i-3):min(len(lines), i+4)], "\n")
			if !strings.Contains(window, `Setenv("USERPROFILE"`) {
				t.Errorf(`%s:%d sets HOME without USERPROFILE.
    os.UserHomeDir() reads %%USERPROFILE%% on Windows, so this test would use the
    real home directory there. Add:
        t.Setenv("USERPROFILE", <same value>) // os.UserHomeDir on Windows`,
					name, i+1)
			}
		}
	}
}

// goInstalledTools are the tools this repo's Makefile obtains with `go install`.
//
// They share a trap: `go install` writes to GOBIN or GOPATH/bin, neither of
// which is guaranteed to be on PATH, so invoking one by bare name in a recipe
// works on the maintainer's machine and dies on someone else's with make's own
// error and no guidance:
//
//	Running govulncheck...
//	make: govulncheck: No such file or directory
//	make: *** [sec-scan] Error 1
var goInstalledTools = []string{"govulncheck", "actionlint", "golangci-lint"}

// TestMakefileResolvesGoInstalledTools fails when a recipe invokes one of those
// tools by bare name.
//
// The fix they must use instead is the find-tool function at the top of the
// Makefile, which looks on PATH and then in the directory `go install` actually
// writes to. This is a guard rather than a convention because the failure is
// invisible to whoever writes it: the tool is on THEIR path.
func TestMakefileResolvesGoInstalledTools(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}

	checked := 0
	for i, line := range strings.Split(string(data), "\n") {
		// Recipe lines only: they start with a tab. Variable definitions and
		// comments that merely NAME a tool are not invocations.
		if !strings.HasPrefix(line, "\t") {
			continue
		}
		body := strings.TrimLeft(line, "\t")
		body = strings.TrimPrefix(body, "@")
		body = strings.TrimPrefix(body, "-")
		body = strings.TrimSpace(body)
		if strings.HasPrefix(body, "#") || strings.HasPrefix(body, "echo ") {
			continue
		}
		checked++
		for _, tool := range goInstalledTools {
			// A bare invocation is the tool at the START of a command, not the
			// same word inside `command -v x`, an echo, or a find-tool call.
			if body == tool || strings.HasPrefix(body, tool+" ") {
				t.Errorf("Makefile:%d invokes %q by bare name:\n  %s\n"+
					"Use $(call find-tool,%s) — `go install` writes to GOBIN or "+
					"GOPATH/bin, and neither is guaranteed to be on PATH. A bare name "+
					"works on the machine that wrote it and dies elsewhere with make's "+
					"own error and no guidance.", i+1, tool, body, tool)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no recipe lines were examined — this guard would pass vacuously")
	}
}
