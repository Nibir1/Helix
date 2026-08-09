// internal/ai/model_timeout_test.go
//
// Purpose: Unit tests for explicit model timeout entrypoints.
package ai

import (
	"testing"
	"time"
)

// TestRunModelWithTimeoutRejectsEmptyPrompt ensures empty prompts fail fast.
func TestRunModelWithTimeoutRejectsEmptyPrompt(t *testing.T) {
	_, err := RunModelWithTimeout("   ", DefaultModelConfig(), 1*time.Second)
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

// TestRunModelWithTimeoutRequiresProvider ensures missing provider fails fast.
func TestRunModelWithTimeoutRequiresProvider(t *testing.T) {
	oldProvider := activeProvider
	oldModel := activeModel

	activeProvider = nil
	activeModel = ""

	defer func() {
		activeProvider = oldProvider
		activeModel = oldModel
	}()

	_, err := RunModelWithTimeout("hello", DefaultModelConfig(), 1*time.Second)
	if err == nil {
		t.Fatal("expected error when no provider is configured")
	}
}
