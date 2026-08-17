// internal/ollama/installer_test.go
// Purpose: Verify the installer checksum helper (supply-chain hardening, P7.7).
package ollama

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileSHA256(t *testing.T) {
	// SHA-256 of "hello\n" is a well-known stable vector.
	path := filepath.Join(t.TempDir(), "probe.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := fileSHA256(path)
	if err != nil {
		t.Fatalf("fileSHA256: %v", err)
	}
	const want = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	if got != want {
		t.Fatalf("checksum mismatch: got %s want %s", got, want)
	}
}
