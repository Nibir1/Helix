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
