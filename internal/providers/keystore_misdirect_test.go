// internal/providers/keystore_misdirect_test.go
// Purpose: catch a key pasted into the wrong provider's slot at entry time,
// while correcting it is still free — not at the first paid API call.
package providers

import "testing"

func TestMisdirectedKey(t *testing.T) {
	cases := []struct {
		name      string
		provider  string
		key       string
		wantOwner string
		wantWrong bool
	}{
		// The incident this exists for: an xAI key (xAI serves Grok) pasted
		// into Helix's groq slot — GroqCloud, a different company one letter
		// away. It authenticated against the wrong vendor and failed every
		// transcription.
		{"xai key into groq STT", "stt.groq", "xai-8uPrCOl8LGKqzvjl", "xai", true},
		{"xai key into groq", "groq", "xai-abc123", "xai", true},
		{"groq key into xai", "xai", "gsk_abc123", "groq", true},
		{"anthropic key into openai", "openai", "sk-ant-api03-xyz", "anthropic", true},

		// Correct pairings must pass silently.
		{"xai key into xai", "xai", "xai-abc123", "xai", false},
		{"groq key into groq", "groq", "gsk_abc123", "groq", false},
		{"groq key into namespaced groq TTS", "tts.groq", "gsk_abc", "groq", false},
		{"anthropic key into anthropic", "anthropic", "sk-ant-api03-xyz", "anthropic", false},

		// A bare "sk-" is shared by OpenAI, DeepSeek, Kimi and Qwen, so it
		// identifies nothing. Treating it as OpenAI-only would false-alarm on
		// every DeepSeek key.
		{"sk- into deepseek is not a collision", "deepseek", "sk-abcdef", "", false},
		{"sk-proj- into openai", "openai", "sk-proj-abcdef", "", false},
		{"sk- into kimi", "kimi", "sk-abcdef", "", false},

		// Unknown formats must always pass: vendors change key shapes, and a
		// positive rule would start rejecting valid new keys.
		{"unknown format", "deepgram", "0123456789abcdef", "", false},
		{"empty key", "openai", "", "", false},
		{"whitespace only", "openai", "   ", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, wrong := MisdirectedKey(tc.provider, tc.key)
			if wrong != tc.wantWrong {
				t.Fatalf("MisdirectedKey(%q, …) wrong=%v, want %v (owner=%q)",
					tc.provider, wrong, tc.wantWrong, owner)
			}
			if tc.wantWrong && owner != tc.wantOwner {
				t.Fatalf("owner = %q, want %q", owner, tc.wantOwner)
			}
		})
	}
}

// The hint must name the actual confusion, not just report a mismatch — the
// whole reason this mistake happens is that "Groq" and "Grok" look alike.
func TestKeyOwnerHintExplainsTheNameCollision(t *testing.T) {
	hint := KeyOwnerHint("stt.groq", "xai")
	for _, want := range []string{"xAI", "Grok", "Groq", "console.groq.com", "console.x.ai"} {
		if !contains(hint, want) {
			t.Errorf("hint should mention %q to be actionable, got: %s", want, hint)
		}
	}

	// The reverse direction must be explained too.
	if r := KeyOwnerHint("xai", "groq"); !contains(r, "console.x.ai") {
		t.Errorf("reverse hint should point at the xAI console, got: %s", r)
	}

	// An unrelated mismatch still names the issuer rather than saying nothing.
	if g := KeyOwnerHint("openai", "anthropic"); !contains(g, "anthropic") {
		t.Errorf("generic hint should name the issuer, got: %s", g)
	}
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
