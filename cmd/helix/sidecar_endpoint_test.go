// cmd/helix/sidecar_endpoint_test.go
//
// Purpose: a sidecar moved to a free port must still be there after the wizard
// saves.
//
// The failure this pins is subtle and shipped twice. A local sidecar chosen as
// a FALLBACK (chain: "deepgram → piper-local") had its port reassigned, and the
// reassignment was written to TTS.BaseURL — a field that belongs to whichever
// provider is PRIMARY. The wizard's final merge then set Provider back to
// deepgram and carried across an Endpoints map nothing had written, so the
// moved port was simply gone. The adapter dialled piper's stock port 5000,
// which macOS AirPlay owns, and the summary reported "still not answering"
// three lines below "✔ verified" — about a server that was running.
//
// whisper-local escaped it by accident: its port collided a SECOND time at
// start, which records the endpoint by the other route. That accident is why
// one half of the chain worked and the other did not, and why the bug looked
// like a TTS problem rather than a shared one.
package main

import (
	"os"
	"strings"
	"testing"

	"helix/internal/config"
	"helix/internal/speech"
)

// withSpeechConfig swaps the package-level cfg for one test.
func withSpeechConfig(t *testing.T, kind, primary, fallback string) {
	t.Helper()
	saved := cfg
	t.Cleanup(func() { cfg = saved })

	cfg = &config.Config{}
	switch kind {
	case "stt":
		cfg.Speech.STT.Provider = primary
		cfg.Speech.STT.Fallbacks = []string{fallback}
	case "tts":
		cfg.Speech.TTS.Provider = primary
		cfg.Speech.TTS.Fallbacks = []string{fallback}
	}
}

func endpointsFor(kind string) map[string]string {
	if kind == "stt" {
		return cfg.Speech.STT.Endpoints
	}
	return cfg.Speech.TTS.Endpoints
}

func primaryOf(kind string) string {
	if kind == "stt" {
		return cfg.Speech.STT.Provider
	}
	return cfg.Speech.TTS.Provider
}

// A reassigned port must land in the per-provider Endpoints map, for BOTH
// directions and whether the sidecar is primary or a fallback.
func TestMovedPortIsRecordedPerProvider(t *testing.T) {
	cases := []struct {
		name, kind, primary, sidecar string
	}{
		{"tts fallback", "tts", "deepgram", "piper-local"},
		{"stt fallback", "stt", "deepgram", "whisper-local"},
		{"tts primary", "tts", "piper-local", ""},
		{"stt primary", "stt", "whisper-local", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sidecar := c.sidecar
			if sidecar == "" {
				sidecar = c.primary
			}
			withSpeechConfig(t, c.kind, c.primary, sidecar)

			const moved = "http://127.0.0.1:28185"
			applySidecarEndpoint(c.kind, sidecar, moved)

			eps := endpointsFor(c.kind)
			if got := eps[sidecar]; got != moved {
				t.Fatalf("Endpoints[%q] = %q, want %q\n"+
					"a moved port that is not in Endpoints is lost the moment the "+
					"wizard merges its config", sidecar, got, moved)
			}
			// Recording a FALLBACK's address must not hijack the chain.
			if primaryOf(c.kind) != c.primary {
				t.Errorf("primary provider changed to %q, want %q — recording a "+
					"sidecar's port must not make it the primary",
					primaryOf(c.kind), c.primary)
			}
		})
	}
}

// The endpoint has to survive the merge the wizard performs at the end, which
// replaces the whole section with the values it collected.
func TestMovedPortSurvivesTheWizardMerge(t *testing.T) {
	withSpeechConfig(t, "tts", "deepgram", "piper-local")
	const moved = "http://127.0.0.1:28185"
	applySidecarEndpoint("tts", "piper-local", moved)

	// Exactly what handleVoiceSetup does: a freshly collected section, with the
	// endpoints carried across explicitly.
	collected := cfg.Speech.TTS
	collected.Provider = "deepgram"
	collected.Fallbacks = []string{"piper-local"}
	collected.Endpoints = cfg.Speech.TTS.Endpoints
	cfg.Speech.TTS = collected

	if got := cfg.Speech.TTS.Endpoints["piper-local"]; got != moved {
		t.Fatalf("after the merge Endpoints[piper-local] = %q, want %q", got, moved)
	}
}

// One vendor, one account, one key.
//
// The "lowest latency" preset puts Deepgram on both sides of the chain, and the
// wizard asked for its key twice in a single run — once under stt.deepgram and
// once under tts.deepgram. Separate keystores per direction are right (a chain
// may hear with one vendor and speak with another); treating them as separate
// ACCOUNTS is not.
func TestKeyEnteredForOneDirectionIsAdoptedByTheOther(t *testing.T) {
	if err := speech.Init(speech.Config{}); err != nil {
		t.Skipf("speech engine unavailable: %v", err)
	}
	reg := speech.Default()
	if reg == nil {
		t.Skip("no speech registry")
	}

	const provider = "deepgram"
	const key = "test-key-not-a-real-credential"

	// Entered once, on the STT side.
	if err := speech.SaveSTTKey(provider, key); err != nil {
		t.Skipf("cannot save a key in this environment: %v", err)
	}
	t.Cleanup(func() {
		_ = speech.SaveSTTKey(provider, "")
		_ = speech.SaveTTSKey(provider, "")
	})

	if !adoptSiblingSpeechKey("tts", provider) {
		t.Fatal("the TTS side did not adopt the key just entered for STT — " +
			"the user is asked for the same credential twice in one wizard run")
	}
	if got := reg.Keys().Get(speech.TTSKeyPrefix + provider); got != key {
		t.Fatalf("tts.%s = %q, want the key entered for STT", provider, got)
	}
}

// ...and it must not invent one when the other direction has nothing.
func TestNoSiblingKeyMeansNoAdoption(t *testing.T) {
	if err := speech.Init(speech.Config{}); err != nil {
		t.Skipf("speech engine unavailable: %v", err)
	}
	if adoptSiblingSpeechKey("tts", "a-provider-nobody-has-configured") {
		t.Error("adopted a key for a provider with none stored")
	}
}

// The helper must actually be WIRED IN.
//
// Testing adoptSiblingSpeechKey alone proves the mechanism and not the
// behaviour: deleting its call from settleSpeechKey left every other test in
// this file green while restoring the double prompt exactly. Driving
// settleSpeechKey directly would reach commands.AskLine and read stdin, so the
// wiring is pinned by reading the source — the same approach
// TestPullFailureDoesNotAbortSetup uses for the same reason.
func TestSettleSpeechKeyConsultsTheOtherDirection(t *testing.T) {
	src, err := os.ReadFile("speech_handlers.go")
	if err != nil {
		t.Fatalf("read speech_handlers.go: %v", err)
	}
	body := strings.ReplaceAll(string(src), "\r\n", "\n")

	start := strings.Index(body, "func settleSpeechKey(")
	if start < 0 {
		t.Fatal("settleSpeechKey not found")
	}
	fn := body[start:]
	if end := strings.Index(fn, "\nfunc "); end > 0 {
		fn = fn[:end]
	}

	ask := strings.Index(fn, "commands.AskLine")
	adopt := strings.Index(fn, "adoptSiblingSpeechKey(")
	if adopt < 0 {
		t.Fatal("settleSpeechKey does not call adoptSiblingSpeechKey — a provider " +
			"on both sides of a chain will be asked for its key twice")
	}
	if ask >= 0 && adopt > ask {
		t.Error("adoptSiblingSpeechKey is called AFTER the prompt; it has to run " +
			"before, or the user has already typed the key again")
	}
}
