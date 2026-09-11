//go:build !windows

// internal/uninstall/uninstall_unix_test.go
// Purpose: the tests for the parts that only exist on a Unix-like machine —
// /etc/shells and the login shell. Split out because `GOOS=windows go vet`
// typechecks test files too, and etcShells does not exist there.
package uninstall

import (
	"os"
	"path/filepath"
	"testing"
)

// quarantineSystemPaths points the shells file somewhere harmless for the whole
// package. See TestMain for why this is load-bearing rather than tidy.
func quarantineSystemPaths() {
	etcShells = filepath.Join(os.TempDir(), "helix-uninstall-tests-no-such-shells")
}

// isolateShells points etcShells at a temp file and restores it afterwards.
func isolateShells(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shells")
	write(t, path, content)
	prev := etcShells
	etcShells = path
	t.Cleanup(func() { etcShells = prev })
	return path
}

// planShell must read the seam, not a hardcoded path. Proven by pointing the
// seam at a file with a Helix line and checking the plan grows an Edit item.
func TestPlanShellReadsTheShellsSeam(t *testing.T) {
	home := t.TempDir()
	if got := Plan(home, ""); len(got) != 0 {
		t.Fatalf("an empty machine planned %d items: %+v", len(got), got)
	}

	isolateShells(t, "/bin/sh\n/usr/local/bin/helix\n")
	items := Plan(home, "")
	if len(items) != 1 {
		t.Fatalf("planned %d items, want the one /etc/shells edit: %+v", len(items), items)
	}
	it := items[0]
	if !it.Edit {
		t.Error("the shells registration is marked for deletion rather than edit; " +
			"removing /etc/shells unregisters every other shell on the machine")
	}
	if it.EditLine != "/usr/local/bin/helix" {
		t.Errorf("EditLine = %q, want the exact line to cut", it.EditLine)
	}
}

// A commented-out or unrelated line must not be mistaken for a registration.
func TestPlanShellIgnoresCommentsAndOtherShells(t *testing.T) {
	isolateShells(t, "# /usr/local/bin/helix\n/bin/zsh\n/opt/homebrew/bin/fish\n")
	if items := Plan(t.TempDir(), ""); len(items) != 0 {
		t.Errorf("planned %d items from a file with no live Helix line: %+v", len(items), items)
	}
}
