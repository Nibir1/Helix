// internal/stealth/stealth_test.go
package stealth

import (
	"slices"
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
	if !executor.config.MemoryOnly {
		t.Error("expected MemoryOnly true by default")
	}
}

func TestEnvironmentSuppression(t *testing.T) {
	executor := NewStealthExecutor(StealthConfig{PrivateHistory: true})
	env := executor.Environment()

	want := []string{"HISTFILE=/dev/null", "HISTSIZE=0", "HISTFILESIZE=0"}
	if !slices.Equal(env, want) {
		t.Errorf("Environment() = %v, want %v", env, want)
	}

	off := NewStealthExecutor(StealthConfig{PrivateHistory: false})
	if got := off.Environment(); len(got) != 0 {
		t.Errorf("Environment() with PrivateHistory off = %v, want empty", got)
	}
}

func TestPersistsHistory(t *testing.T) {
	memoryOnly := NewStealthExecutor(StealthConfig{MemoryOnly: true})
	if memoryOnly.PersistsHistory() {
		t.Error("MemoryOnly must suppress on-disk history persistence")
	}

	persisting := NewStealthExecutor(StealthConfig{MemoryOnly: false})
	if !persisting.PersistsHistory() {
		t.Error("MemoryOnly=false must keep on-disk history persistence")
	}
}
