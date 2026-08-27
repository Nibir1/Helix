// tests/e2e/reboot_upgrade_e2e_test.go
//
// Purpose: what a restart actually refreshes.
//
// The natural question about `/reboot` is whether it works the way a software
// update does — restart, come back with the latest capabilities. Helix has no
// self-update, so it never FETCHES anything. But it re-executes whatever binary
// sits at its own path, which means an upgrade applied by any other means —
// `go build`, a package manager, a copied file — takes effect on the next
// `/reboot` rather than requiring the user to quit and relaunch.
//
// That property is worth pinning because it is easy to lose by accident: caching
// an inode, copying the binary somewhere first, or resolving the executable once
// and reusing a file handle would each break it silently, and the symptom would
// be a shell that restarts and is somehow still the old version.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestE2E_RebootRunsWhateverBinaryIsAtItsPathNow(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	marker := buildMarkerBinary(t)

	// binPath is built ONCE in TestMain and shared by every test in this
	// package, so swapping it has to be undone or every test that runs after
	// this one launches the marker instead of Helix — a whole suite silently
	// testing the wrong program.
	original, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read the helix binary: %v", err)
	}
	t.Cleanup(func() { replaceBinary(t, binPath, original) })

	// Replace the binary at the SAME path, the way `go build -o` or a package
	// manager upgrade does: unlink, then create. The running process keeps its
	// open image; the path now names a different file.
	markerBytes, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read the marker binary: %v", err)
	}
	replaceBinary(t, binPath, markerBytes)

	h.WriteLine("/reboot")
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(h.stripped(), "HELIX-UPGRADE-MARKER") {
			return // the restart ran the replacement
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the reboot did not run the replaced binary — an upgrade applied " +
		"while Helix was running would not take effect on restart")
}

// buildMarkerBinary compiles a trivial program that announces itself, so the
// test can tell which image the supervisor started.
//
// A real compiled binary rather than a script: the supervisor spawns an
// executable, and a shebang file would prove something different on the
// platforms where it works at all.
func buildMarkerBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(
		"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"HELIX-UPGRADE-MARKER\") }\n",
	), 0o600); err != nil {
		t.Fatalf("write marker source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module helixupgrademarker\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatalf("write marker go.mod: %v", err)
	}

	out := filepath.Join(dir, "marker")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build the marker binary here: %v\n%s", err, b)
	}
	return out
}

// replaceBinary installs data at path, unlinking first.
//
// Remove-then-create rather than write-in-place: overwriting a running
// executable fails with ETXTBSY on Linux, which is precisely why build tools
// unlink, and precisely why a restart picks the replacement up.
func replaceBinary(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove old binary: %v", err)
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatalf("install binary: %v", err)
	}
}
