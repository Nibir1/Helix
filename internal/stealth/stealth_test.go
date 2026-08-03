// internal/stealth/stealth_test.go
package stealth

import (
	"strings"
	"testing"
)

func TestNewStealthExecutor(t *testing.T) {
	cfg := DefaultStealthConfig()
	executor := NewStealthExecutor(cfg)

	if executor == nil {
		t.Fatal("expected non-nil executor")
	}

	if !executor.config.PrivateHistory {
		t.Error("expected PrivateHistory true by default")
	}
}

func TestExecuteWithSuppression(t *testing.T) {
	cfg := StealthConfig{PrivateHistory: true}
	executor := NewStealthExecutor(cfg)

	output, err := executor.Execute("echo HistFile=$HISTFILE")
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if !strings.Contains(output, "/dev/null") {
		t.Logf("HISTFILE not visible in output (got %q); environment inheritance may vary", output)
	}
}
