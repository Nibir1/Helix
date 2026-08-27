// internal/providers/keystore_env_test.go
// Purpose: an environment variable a provider's own docs tell you to export has
// to be the one Helix reads, or the key is "configured" everywhere except where
// it counts.
package providers

import (
	"path/filepath"
	"testing"
)

func newTestKeyStore(t *testing.T) *KeyStore {
	t.Helper()
	ks, err := NewKeyStoreAt(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatalf("keystore: %v", err)
	}
	return ks
}

func TestProviderKeysComeFromTheirDocumentedEnvVars(t *testing.T) {
	for _, tc := range []struct{ provider, env string }{
		{"gemini", "GEMINI_API_KEY"},
		{"meta", "META_API_KEY"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			ks := newTestKeyStore(t)
			t.Setenv(tc.env, "from-env")
			if got := ks.Get(tc.provider); got != "from-env" {
				t.Errorf("Get(%q) = %q, want the value of %s", tc.provider, got, tc.env)
			}
		})
	}
}

// Meta's quickstart tells people to export MODEL_API_KEY. Helix prefers its own
// vendor-named variable — MODEL_API_KEY says nothing about whose model — but a
// user who followed Meta's docs must not be told their key is missing.
func TestMetaAcceptsItsVendorDocumentedEnvVarAsAFallback(t *testing.T) {
	ks := newTestKeyStore(t)
	t.Setenv("MODEL_API_KEY", "meta-quickstart")
	if got := ks.Get("meta"); got != "meta-quickstart" {
		t.Errorf("Get(meta) = %q, want the MODEL_API_KEY fallback", got)
	}

	// Precedence: the explicit, vendor-named variable wins.
	t.Setenv("META_API_KEY", "explicit")
	if got := ks.Get("meta"); got != "explicit" {
		t.Errorf("META_API_KEY must outrank MODEL_API_KEY, got %q", got)
	}
}

// The generic fallback must stay generic: MODEL_API_KEY belongs to Meta alone,
// and leaking it into every provider would hand one vendor's key to another.
func TestModelAPIKeyIsNotReadForOtherProviders(t *testing.T) {
	ks := newTestKeyStore(t)
	t.Setenv("MODEL_API_KEY", "meta-only")
	for _, p := range []string{"openai", "gemini", "anthropic", "glm"} {
		if got := ks.Get(p); got != "" {
			t.Errorf("Get(%q) = %q; MODEL_API_KEY must not leak beyond meta", p, got)
		}
	}
}

// Google issues AIza-prefixed keys across all its APIs. Pasting one into any
// other provider's slot is the same class of mistake as the xai/groq incident
// this guard was built for.
func TestGoogleKeyPastedElsewhereIsCaught(t *testing.T) {
	owner, misdirected := MisdirectedKey("openai", "AIzaSyExampleNotARealKey")
	if !misdirected || owner != "gemini" {
		t.Errorf("MisdirectedKey(openai, AIza…) = (%q, %v), want (gemini, true)",
			owner, misdirected)
	}
	if _, misdirected := MisdirectedKey("gemini", "AIzaSyExampleNotARealKey"); misdirected {
		t.Error("an AIza key in the gemini slot is where it belongs")
	}
}
