// internal/commands/sandbox_fuzz_test.go
// Purpose: Continuous fuzzing for the directory sandbox path and command validators.
// Invariants: Must never panic; ValidateSafePath never validates a path outside the root.
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzSandboxValidateCommand(f *testing.F) {
	tmpDir := f.TempDir()
	sandbox := &DirectorySandbox{
		allowedDir:  tmpDir,
		mode:        SandboxCurrentDir,
		originalDir: tmpDir,
	}

	seeds := []string{
		"ls -la",
		"rm -rf /",
		"cat " + filepath.Join(tmpDir, "safe.txt"),
		"rm " + filepath.Join(os.TempDir(), "unsafe.txt"),
		"echo hello > " + filepath.Join(tmpDir, "out.txt"),
		"echo hello > /etc/passwd",
		"cd ../..",
		"mv file.txt ../",
		"chmod 777 " + filepath.Join(tmpDir, "script.sh"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		// Invariant: Must never panic.
		_, _ = sandbox.ValidateCommand(input)
	})
}

func FuzzValidateSafePath(f *testing.F) {
	tmpDir := f.TempDir()
	sandbox := &DirectorySandbox{
		allowedDir:  tmpDir,
		mode:        SandboxCurrentDir,
		originalDir: tmpDir,
	}

	seeds := []string{
		filepath.Join(tmpDir, "safe.txt"),
		filepath.Join(os.TempDir(), "unsafe.txt"),
		".",
		"..",
		"../",
		tmpDir + "/../" + filepath.Base(tmpDir),
		"~/outside",
		"/etc/passwd",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		cleanPath, err := sandbox.ValidateSafePath(input)
		if err == nil {
			// Invariant: If no error, the resolved path MUST logically be inside the sandbox root.
			// We assert it doesn't contain obvious unresolved escapes.
			if strings.Contains(cleanPath, "..") {
				t.Fatalf("validated path contains '..': %s", cleanPath)
			}
		}
	})
}
