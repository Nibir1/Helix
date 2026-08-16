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

func TestDirectorySandbox_ValidateSafePathSiblingPrefix(t *testing.T) {
	// Regression: a sibling directory whose name merely extends the root
	// (/tmp/jail vs /tmp/jail-x) must NOT pass a plain string-prefix check.
	root, err := os.MkdirTemp("", "helix-jail")
	if err != nil {
		t.Fatal(err)
	}
	sibling := root + "-x"
	if mkErr := os.Mkdir(sibling, 0o755); mkErr != nil {
		t.Fatal(mkErr)
	}
	sandbox := &DirectorySandbox{
		allowedDir:  root,
		mode:        SandboxCurrentDir,
		originalDir: root,
	}

	if _, err := sandbox.ValidateSafePath(filepath.Join(sibling, "escape.txt")); err == nil {
		t.Fatal("expected sibling directory with shared prefix to be blocked")
	}
	// The root itself and real children must still pass.
	if _, err := sandbox.ValidateSafePath(root); err != nil {
		t.Fatalf("expected root itself to pass, got: %v", err)
	}
	if _, err := sandbox.ValidateSafePath(filepath.Join(root, "child.txt")); err != nil {
		t.Fatalf("expected child of root to pass, got: %v", err)
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
