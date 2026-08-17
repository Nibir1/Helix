// internal/providers/toolcall_test.go
// Purpose: BlackBox P8.7 — streamed tool calls are reassembled correctly from
// fragmented SSE deltas, and capability reporting stays honest about which
// adapters can actually drive function calling.
package providers

import (
	"testing"
)

func TestToolCallAccumulatorReassemblesFragments(t *testing.T) {
	a := NewToolCallAccumulator()
	// The shape a provider actually streams: id + name once, then argument
	// slices with neither.
	a.Add(0, "call_1", "emit_plan", `{"inte`)
	a.Add(0, "", "", `nt":"shell","steps":[`)
	a.Add(0, "", "", `{"tool":"shell","command":"ls"}]}`)

	got := a.Assemble()
	if len(got) != 1 {
		t.Fatalf("expected 1 assembled call, got %d", len(got))
	}
	if got[0].ID != "call_1" || got[0].Name != "emit_plan" {
		t.Fatalf("id/name lost during accumulation: %+v", got[0])
	}
	want := `{"intent":"shell","steps":[{"tool":"shell","command":"ls"}]}`
	if got[0].Arguments != want {
		t.Fatalf("arguments = %q, want %q", got[0].Arguments, want)
	}
}

// Parallel calls interleave in the stream, so ordering must come from the
// provider's index, not from arrival order.
func TestToolCallAccumulatorOrdersByIndex(t *testing.T) {
	a := NewToolCallAccumulator()
	a.Add(1, "call_b", "second", `{"b":1}`)
	a.Add(0, "call_a", "first", `{"a":`)
	a.Add(1, "", "", ``)
	a.Add(0, "", "", `1}`)

	got := a.Assemble()
	if len(got) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(got))
	}
	if got[0].Name != "first" || got[1].Name != "second" {
		t.Fatalf("calls out of index order: %+v", got)
	}
	if got[0].Arguments != `{"a":1}` {
		t.Fatalf("interleaved fragments mismatched: %q", got[0].Arguments)
	}
}

// A truncated stream can leave a fragment that never received a function name.
// Forwarding it would surface downstream as an unparseable tool call.
func TestToolCallAccumulatorDropsNamelessFragments(t *testing.T) {
	a := NewToolCallAccumulator()
	a.Add(0, "call_x", "", `{"partial":`)
	if got := a.Assemble(); got != nil {
		t.Fatalf("nameless fragment must be dropped, got %+v", got)
	}
}

func TestToolCallAccumulatorEmptyIsNil(t *testing.T) {
	if got := NewToolCallAccumulator().Assemble(); got != nil {
		t.Fatalf("no fragments must assemble to nil, got %+v", got)
	}
}

func TestSupportsToolUse(t *testing.T) {
	cases := []struct {
		provider, model string
		want            bool
	}{
		{"openai", "gpt-4o", true},
		{"deepseek", "deepseek-chat", true},
		{"kimi", "kimi-k2.6", true},
		{"qwen", "qwen3.7-plus", true},
		{"glm", "glm-5.2", true},
		{"OpenAI", "gpt-4o", true}, // case-insensitive
		// Embedding endpoints share the provider name but have no tools.
		{"openai", "text-embedding-3-small", false},
		// P8.7b: Anthropic (tool_use blocks) and Ollama (/api/chat tools) are
		// now driven natively.
		{"anthropic", "claude-opus-5", true},
		{"ollama", "llama3.1:8b", true},
		{"ollama", "qwen2.5:3b", true},
		{"ollama", "mistral-nemo", true},
		// Ollama support is per-MODEL, not per-provider: Gemma ships no tool
		// template, and it is Helix's own default local model — claiming
		// support would waste a round trip on every plan, on exactly the
		// hardware that can least afford one.
		{"ollama", "gemma4:e2b", false},
		{"ollama", "gemma3", false},
		{"ollama", "tinyllama", false},
		// Unknowable endpoints: guessing wrong is worse than not trying.
		{"custom", "whatever", false},
		{"llamacpp", "local-gguf", false},
		{"", "", false},
	}
	for _, tc := range cases {
		if got := SupportsToolUse(tc.provider, tc.model); got != tc.want {
			t.Errorf("SupportsToolUse(%q, %q) = %v, want %v",
				tc.provider, tc.model, got, tc.want)
		}
	}
}

func TestCapabilitiesToolUseMatchesSupportsToolUse(t *testing.T) {
	// The flag and the predicate must never disagree — Capabilities.ToolUse is
	// the documented consumer-facing signal.
	for _, tc := range []struct{ provider, model string }{
		{"openai", "gpt-4o"}, {"ollama", "gemma4:e2b"},
		{"ollama", "llama3.1:8b"}, {"anthropic", "claude-opus-5"},
	} {
		caps := CapabilitiesFor(tc.provider, tc.model)
		if caps.ToolUse != SupportsToolUse(tc.provider, tc.model) {
			t.Errorf("%s/%s: Capabilities.ToolUse disagrees with SupportsToolUse",
				tc.provider, tc.model)
		}
	}
}
