// internal/commands/sandbox_test.go
package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectorySandbox_ValidateSafePath(t *testing.T) {
	tmpDir := t.TempDir()
	sandbox := &DirectorySandbox{
		allowedDir:  tmpDir,
		mode:        SandboxCurrentDir,
		originalDir: tmpDir,
	}

	// Safe path inside sandbox
	safePath := filepath.Join(tmpDir, "safe.txt")
	if _, err := sandbox.ValidateSafePath(safePath); err != nil {
		t.Fatalf("expected safe path to pass, got: %v", err)
	}

	// Unsafe path outside sandbox
	unsafePath := filepath.Join(os.TempDir(), "unsafe.txt")
	if _, err := sandbox.ValidateSafePath(unsafePath); err == nil {
		t.Fatal("expected unsafe path to be blocked")
	}
}

func TestDirectorySandbox_IsDangerousWrite(t *testing.T) {
	sandbox := &DirectorySandbox{}
	if !sandbox.isDangerousWriteOperation("rm -rf /") {
		t.Fatal("expected rm to be dangerous")
	}
	if sandbox.isDangerousWriteOperation("ls -la") {
		t.Fatal("expected ls to be safe")
	}
}
