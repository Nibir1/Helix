// tests/e2e/main_test.go
// Purpose: Build the helix binary once for the e2e suite. Lives in a
// build-tag-free file so the non-PTY (daemon remote) tests share the same
// binary on Windows as well as macOS/Linux.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// binPath is the compiled helix binary built once in TestMain.
var binPath string

// TestMain builds the helix binary once for all e2e tests.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "helix-e2e-bin-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: temp dir:", err)
		os.Exit(1)
	}
	binPath = filepath.Join(tmp, "helix")
	if runtime.GOOS == "windows" {
		binPath += ".exe" // go build appends .exe on Windows
	}
	build := exec.Command("go", "build", "-o", binPath, "helix/cmd/helix")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: build failed: %v\n", err)
		_ = os.RemoveAll(tmp)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}
