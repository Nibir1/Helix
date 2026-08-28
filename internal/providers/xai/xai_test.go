// internal/providers/xai/xai_test.go
// Purpose: the xAI (Grok) adapter is wired to the right vendor, requires a key,
// and is planner- and tool-calling-capable.
package xai

import (
	"testing"
	"time"

	"helix/internal/providers"
)

func TestProviderIdentity(t *testing.T) {
	p := New("xai-testkey", providers.NewHTTPClient(5*time.Second))

	if p.Name() != Name {
		t.Fatalf("Name() = %q, want %q", p.Name(), Name)
	}
	if p.IsLocal() {
		t.Error("xAI is a hosted API, not local")
	}
	if !p.RequiresAPIKey() {
		t.Error("xAI requires a key")
	}
	// The display name must carry BOTH words: the whole reason keys get pasted
	// into the wrong slot is that users see only one of "xAI"/"Grok" and cannot
	// tell it apart from Groq.
	if got := p.DisplayName(); got != "xAI (Grok)" {
		t.Errorf("DisplayName() = %q, want it to name company and model", got)
	}
}

// Guards the exact confusion this provider is adjacent to: xAI is api.x.ai,
// Groq is api.groq.com. Swapping them would authenticate against the wrong
// company and fail every call.
func TestBaseURLIsXAINotGroq(t *testing.T) {
	if BaseURL != "https://api.x.ai/v1" {
		t.Fatalf("BaseURL = %q, want xAI's endpoint", BaseURL)
	}
	if want := "groq"; contains(BaseURL, want) {
		t.Fatalf("BaseURL %q points at Groq — a different company", BaseURL)
	}
}

func TestCapabilities(t *testing.T) {
	p := New("xai-testkey", providers.NewHTTPClient(5*time.Second))
	caps := p.Capabilities()

	if !caps.Chat {
		t.Error("chat must be supported")
	}
	// docs.x.ai lists function calling, so Grok joins the native planner path.
	if !caps.ToolUse {
		t.Error("xAI supports function calling and must report ToolUse")
	}
	if !caps.Planner {
		t.Error("Grok's context window is far above the planner threshold")
	}
	if caps.Local {
		t.Error("xAI must not be flagged local")
	}
}

// A missing context-limit entry silently clamps RAG context to a fraction of
// what the model accepts, so the catalogue must know Grok.
func TestContextLimitsKnown(t *testing.T) {
	for model, want := range map[string]int{
		"grok-4.6":   500_000,
		"grok-4.3":   1_000_000,
		"grok-build": 256_000,
	} {
		if got := providers.GetContextLimit(model); got != want {
			t.Errorf("GetContextLimit(%q) = %d, want %d", model, got, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
