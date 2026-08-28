// cmd/helix/local_endpoints_test.go
// Purpose: the endpoint collector must report where a sidecar is ACTUALLY
// configured to answer, because everything built on it — the collision warning
// in /doctor, /blackbox status and the setup wizard — is only as correct as
// the address it starts from.
package main

import (
	"testing"

	"helix/internal/config"
	"helix/internal/edge"
)

// The wizard moves a sidecar off a busy port by writing the per-provider
// Endpoints map — NOT BaseURL, which only ever holds the primary's URL. The
// endpoint collector read BaseURL and otherwise returned the stock default, so
// after the wizard reassigned whisper-local to a free port and said so on
// screen, the conflict report still announced it on 8080 and warned about a
// collision with llama.cpp that no longer existed.
func TestEndpointsFollowTheWizardsPortReassignment(t *testing.T) {
	restore := cfg
	t.Cleanup(func() { cfg = restore })
	cfg = &config.Config{}

	// The exact shape from the reported session: whisper-local is a FALLBACK,
	// not the primary, and lives on a reassigned port.
	cfg.Speech.STT.Provider = "groq"
	cfg.Speech.STT.Fallbacks = []string{"whisper-local"}
	cfg.Speech.STT.Endpoints = map[string]string{"whisper-local": "http://127.0.0.1:28859"}

	if got := localSTTURL(); got != "http://127.0.0.1:28859" {
		t.Errorf("localSTTURL() = %q, want the reassigned port from the Endpoints map", got)
	}

	cfg.Speech.TTS.Provider = "openai"
	cfg.Speech.TTS.Fallbacks = []string{"piper-local"}
	cfg.Speech.TTS.Endpoints = map[string]string{"piper-local": "http://127.0.0.1:28183"}
	if got := localTTSURL("piper-local", piperDefaultEndpoint); got != "http://127.0.0.1:28183" {
		t.Errorf("localTTSURL() = %q, want the reassigned port", got)
	}

	// With nothing configured the stock default is still the honest answer.
	cfg.Speech.STT.Endpoints = nil
	if got := localSTTURL(); got != whisperDefaultEndpoint {
		t.Errorf("unconfigured localSTTURL() = %q, want the stock default", got)
	}
}

// A reassigned port must also clear the collision the report was warning about,
// which is the user-visible half of the bug.
func TestReassignedPortResolvesTheReportedConflict(t *testing.T) {
	restore := cfg
	t.Cleanup(func() { cfg = restore })
	cfg = &config.Config{}

	// Both on 8080: a real collision.
	cfg.LLM.LlamaCppURL = "http://127.0.0.1:8080"
	cfg.LLM.Fallback.Provider = "llamacpp"
	cfg.Speech.STT.Provider = "whisper-local"
	cfg.Speech.STT.Endpoints = map[string]string{"whisper-local": "http://127.0.0.1:8080"}

	if len(edge.FindConflicts(localSidecarEndpoints())) == 0 {
		t.Fatal("precondition: two services on 8080 must be reported as a conflict")
	}

	// Moved off it, exactly as the wizard does.
	cfg.Speech.STT.Endpoints["whisper-local"] = "http://127.0.0.1:28859"
	for _, c := range edge.FindConflicts(localSidecarEndpoints()) {
		if c.Involves() {
			t.Errorf("the reassigned port must clear the collision, still reported: %s", c.Describe())
		}
	}
}
