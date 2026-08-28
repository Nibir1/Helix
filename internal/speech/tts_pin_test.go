// internal/speech/tts_pin_test.go
//
// Purpose: reproduce "first two sentences speaks someone and then the rest two
// sentence speaks someone else", and hold it fixed.
package speech

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"helix/internal/providers"
)

// countingTTS records every sentence it is asked to speak, and can be made to
// fail from a given call onward — a provider that starts working and stops,
// which is what a rate limit looks like halfway through a reply.
type countingTTS struct {
	name      string
	spoke     []string
	failAfter int // 0 = never fail
	calls     int
}

func (c *countingTTS) Name() string         { return c.name }
func (c *countingTTS) DisplayName() string  { return c.name }
func (c *countingTTS) SetAPIKey(string)     {}
func (c *countingTTS) RequiresAPIKey() bool { return false }
func (c *countingTTS) IsLocal() bool        { return true }
func (c *countingTTS) DefaultModel() string { return c.name }

func (c *countingTTS) Synthesize(_ context.Context, text string, _ SynthesisOptions) (AudioFormat, error) {
	c.calls++
	if c.failAfter > 0 && c.calls > c.failAfter {
		return AudioFormat{}, errors.New(c.name + ": rate limited")
	}
	c.spoke = append(c.spoke, text)
	return AudioFormat{Kind: KindWAV, SampleRate: 22050, Channels: 1, Bytes: []byte("audio")}, nil
}

func (c *countingTTS) HealthCheck(context.Context) error { return nil }

func registryWith(t *testing.T, primary, fallback *countingTTS) *Registry {
	t.Helper()
	keys, err := providers.NewKeyStoreAt(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatalf("keystore: %v", err)
	}
	r := NewRegistry(keys, providers.NewHTTPClient(5e9))
	r.RegisterTTS(primary)
	r.RegisterTTS(fallback)
	r.SetConfig(Config{TTS: TTSConfig{
		Provider:  primary.name,
		Fallbacks: []string{fallback.name},
	}})
	return r
}

// The reported bug: four sentences, primary dies after two, voice changes.
func TestOneUtteranceKeepsOneVoice(t *testing.T) {
	primary := &countingTTS{name: "cloud", failAfter: 2}
	fallback := &countingTTS{name: "local"}
	r := registryWith(t, primary, fallback)

	ctx := WithUtterance(context.Background())
	sentences := []string{"One.", "Two.", "Three.", "Four."}
	for _, s := range sentences {
		if _, err := r.Synthesize(ctx, s, SynthesisOptions{}); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
	}

	// Sentences 1-2 from the primary, 3-4 from the fallback: one change, which
	// is unavoidable once the primary is down.
	if len(primary.spoke) != 2 {
		t.Errorf("primary spoke %d sentences (%v), want 2", len(primary.spoke), primary.spoke)
	}
	if len(fallback.spoke) != 2 {
		t.Errorf("fallback spoke %d sentences (%v), want 2", len(fallback.spoke), fallback.spoke)
	}

	// The bug being fixed is the RE-ASKING. Without the pin the dead primary is
	// tried again for every remaining sentence, and each of those costs its
	// full timeout — which is what made a degraded reply slow as well as
	// jarring.
	if primary.calls != 3 {
		t.Errorf("primary was called %d times, want 3 (two successes and the one "+
			"failure that retires it) — a provider known to be down is being "+
			"asked again once per sentence", primary.calls)
	}
}

// If the primary recovers mid-reply, the voice must NOT switch back.
func TestRecoveredPrimaryDoesNotStealTheRestOfTheReply(t *testing.T) {
	primary := &countingTTS{name: "cloud", failAfter: 1}
	fallback := &countingTTS{name: "local"}
	r := registryWith(t, primary, fallback)

	ctx := WithUtterance(context.Background())
	for _, s := range []string{"One.", "Two.", "Three."} {
		if _, err := r.Synthesize(ctx, s, SynthesisOptions{}); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
	}
	// Sentence 2 fails over. Even though "cloud" would now succeed again (its
	// failAfter only tripped once per call count), it must not be re-asked.
	if got := strings.Join(fallback.spoke, " "); got != "Two. Three." {
		t.Errorf("fallback spoke %q, want it to finish the reply once it started", got)
	}
}

// Each reply starts fresh: a provider retired in one utterance must be tried
// again in the next, or one transient failure would silence a cloud voice for
// the rest of the session.
func TestANewUtteranceForgetsTheLastOne(t *testing.T) {
	primary := &countingTTS{name: "cloud", failAfter: 1}
	fallback := &countingTTS{name: "local"}
	r := registryWith(t, primary, fallback)

	first := WithUtterance(context.Background())
	_, _ = r.Synthesize(first, "One.", SynthesisOptions{})
	_, _ = r.Synthesize(first, "Two.", SynthesisOptions{}) // retires the primary

	primary.failAfter = 0 // recovered between replies
	primary.calls = 0
	second := WithUtterance(context.Background())
	if _, err := r.Synthesize(second, "Next reply.", SynthesisOptions{}); err != nil {
		t.Fatal(err)
	}
	if primary.calls == 0 {
		t.Error("the primary was never tried in the new utterance — a transient " +
			"failure would mute it for the rest of the session")
	}
}

// Without an utterance scope nothing changes: /blackbox say and the tests that
// call Synthesize directly must behave exactly as before.
func TestNoPinMeansTheConfiguredChain(t *testing.T) {
	primary := &countingTTS{name: "cloud", failAfter: 1}
	fallback := &countingTTS{name: "local"}
	r := registryWith(t, primary, fallback)

	for _, s := range []string{"One.", "Two.", "Three."} {
		if _, err := r.Synthesize(context.Background(), s, SynthesisOptions{}); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
	}
	// Unpinned, the primary is re-tried every time — the old behaviour, kept
	// for callers that speak one thing at a time.
	if primary.calls != 3 {
		t.Errorf("primary called %d times without a pin, want 3", primary.calls)
	}
}
