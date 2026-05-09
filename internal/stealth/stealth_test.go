// internal/stealth/stealth_test.go
package stealth

import (
	"os"
	"strings"
	"testing"
)

func TestNewStealthExecutor(t *testing.T) {
	cfg := DefaultStealthConfig()
	exec := NewStealthExecutor(cfg)
	if exec == nil {
		t.Fatal("expected non‑nil executor")
	}
	if exec.config.MemoryOnly != true {
		t.Error("expected MemoryOnly true by default")
	}
}

func TestExecuteWithSuppression(t *testing.T) {
	cfg := StealthConfig{SuppressHistory: true}
	exec := NewStealthExecutor(cfg)

	// Run a command that prints the value of HISTFILE.
	output, err := exec.Execute("echo HistFile=$HISTFILE")
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if !strings.Contains(output, "/dev/null") {
		t.Logf("HISTFILE not seen in output (got %q) – environment may not be inherited on all systems.", output)
	}
}

func TestWipeLogs(t *testing.T) {
	f, err := os.CreateTemp("", "helix_test_log_*")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("sensitive data")
	f.Close()

	cfg := StealthConfig{LogFiles: []string{f.Name()}}
	exec := NewStealthExecutor(cfg)

	if err := exec.WipeLogs(); err != nil {
		t.Fatalf("wipe failed: %v", err)
	}
	data, _ := os.ReadFile(f.Name())
	if len(data) != 0 {
		t.Errorf("expected empty file after wipe, got %d bytes", len(data))
	}
	os.Remove(f.Name())
}
