// internal/ai/stream_test.go
// Purpose: BlackBox P8.8 — StreamModel delivers fragments as they arrive,
// still returns the complete text callers depend on, and keeps the P11.2
// failover breaker in the loop.
package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"helix/internal/providers"
)

// streamFake emits scripted chunks, optionally failing partway.
type streamFake struct {
	name    string
	chunks  []string
	midErr  error
	openErr error
}

func (p *streamFake) Name() string        { return p.name }
func (p *streamFake) DisplayName() string { return p.name }
func (p *streamFake) SetAPIKey(string)    {}
func (p *streamFake) ListModels(context.Context) ([]providers.ModelInfo, error) {
	return nil, nil
}
func (p *streamFake) HealthCheck(context.Context) error { return nil }
func (p *streamFake) RequiresAPIKey() bool              { return false }
func (p *streamFake) IsLocal() bool                     { return false }
func (p *streamFake) DefaultModel() string              { return "fake" }
func (p *streamFake) Capabilities() providers.Capabilities {
	return providers.Capabilities{Chat: true}
}
func (p *streamFake) Chat(_ context.Context, _ providers.ChatRequest) (<-chan providers.StreamChunk, error) {
	if p.openErr != nil {
		return nil, p.openErr
	}
	ch := make(chan providers.StreamChunk, len(p.chunks)+2)
	for _, c := range p.chunks {
		ch <- providers.StreamChunk{Content: c}
	}
	if p.midErr != nil {
		ch <- providers.StreamChunk{Error: p.midErr}
	} else {
		ch <- providers.StreamChunk{Done: true}
	}
	close(ch)
	return ch, nil
}

func useStreamProvider(t *testing.T, p providers.AIProvider) {
	t.Helper()
	oldP, oldM := activeProvider, activeModel
	t.Cleanup(func() { activeProvider, activeModel = oldP, oldM })
	activeProvider, activeModel = p, "fake"
}

func TestStreamModelDeliversChunksAndFullText(t *testing.T) {
	useStreamProvider(t, &streamFake{name: "fake", chunks: []string{"Hel", "lo ", "world"}})

	var seen []string
	full, err := StreamModel("hi", DefaultModelConfig(), 5*time.Second, func(c string) {
		seen = append(seen, c)
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	// Incremental delivery is the whole point — one callback per fragment.
	if len(seen) != 3 {
		t.Fatalf("expected 3 streamed fragments, got %d (%v)", len(seen), seen)
	}
	if strings.Join(seen, "") != "Hello world" {
		t.Fatalf("fragments reassemble wrong: %q", strings.Join(seen, ""))
	}
	// Callers still need the whole response (script promotion, session store).
	if full != "Hello world" {
		t.Fatalf("full text = %q, want %q", full, "Hello world")
	}
}

func TestStreamModelWorksWithoutCallback(t *testing.T) {
	useStreamProvider(t, &streamFake{name: "fake", chunks: []string{"a", "b"}})

	full, err := StreamModel("hi", DefaultModelConfig(), 5*time.Second, nil)
	if err != nil {
		t.Fatalf("a nil callback must be allowed: %v", err)
	}
	if full != "ab" {
		t.Fatalf("full text = %q", full)
	}
}

// A stream that dies partway must keep what was already displayed: the user
// has seen those tokens, so discarding them would contradict the screen.
func TestStreamModelKeepsPartialTextOnMidStreamError(t *testing.T) {
	useStreamProvider(t, &streamFake{
		name: "fake", chunks: []string{"partial "}, midErr: errors.New("upstream reset"),
	})

	full, err := StreamModel("hi", DefaultModelConfig(), 5*time.Second, nil)
	if err == nil {
		t.Fatal("a mid-stream error must be reported")
	}
	if full != "partial" {
		t.Fatalf("partial text must survive the error, got %q", full)
	}
}

func TestStreamModelSurfacesOpenError(t *testing.T) {
	useStreamProvider(t, &streamFake{name: "fake", openErr: errors.New("HTTP 500: boom")})

	if _, err := StreamModel("hi", DefaultModelConfig(), 5*time.Second, nil); err == nil {
		t.Fatal("a failed Chat call must be reported")
	}
}

func TestStreamModelRejectsEmptyPrompt(t *testing.T) {
	useStreamProvider(t, &streamFake{name: "fake"})
	if _, err := StreamModel("   ", DefaultModelConfig(), time.Second, nil); err == nil {
		t.Fatal("an empty prompt must be rejected, as in RunModelWithTimeout")
	}
}

func TestStreamModelWithoutProvider(t *testing.T) {
	oldP := activeProvider
	t.Cleanup(func() { activeProvider = oldP })
	activeProvider = nil

	if _, err := StreamModel("hi", DefaultModelConfig(), time.Second, nil); err == nil {
		t.Fatal("no configured provider must be an error, not a silent empty reply")
	}
}

// Streaming must not bypass the P11.2 breaker: an availability failure here
// has to count toward failover exactly as a buffered call would.
func TestStreamModelFeedsTheFailoverBreaker(t *testing.T) {
	h := newFailoverHarness(t, errUnreachable, true)
	// Replace the harness's cloud provider with a failing streamer.
	failing := &streamFake{name: "cloud", openErr: errUnreachable}
	failoverMu.Lock()
	activeProvider = failing
	failoverMu.Unlock()

	for i := 0; i < 2; i++ {
		if _, err := StreamModel("hi", DefaultModelConfig(), 2*time.Second, nil); err == nil {
			t.Fatal("expected the streaming call to fail")
		}
	}
	if !LocalFallbackActive() {
		t.Fatal("streamed calls must feed the breaker, or streaming would silently disable failover")
	}
	h.awaitNotice(t)
}
