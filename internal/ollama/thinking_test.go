// internal/ollama/thinking_test.go
// Purpose: a reasoning model that runs out of budget before it answers must say
// so. Found in QA against Helix's own default local model.
package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"helix/internal/providers"
)

// ndjson serves a canned Ollama chat stream.
func ndjson(t *testing.T, frames ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, f := range frames {
			_, _ = w.Write([]byte(f + "\n"))
		}
	}))
}

func drain(t *testing.T, ch <-chan providers.StreamChunk) (string, error) {
	t.Helper()
	var b strings.Builder
	for c := range ch {
		if c.Error != nil {
			return b.String(), c.Error
		}
		b.WriteString(c.Content)
	}
	return b.String(), nil
}

// The bug: ollama reports a perfectly successful stream in which every frame
// carries `thinking` and none carries `content`. Reading only `content` made
// that indistinguishable from a model with nothing to say, and it surfaced to
// the user as "The vision model returned nothing."
func TestReasoningBudgetExhaustionIsReportedNotSwallowed(t *testing.T) {
	srv := ndjson(t,
		`{"message":{"role":"assistant","thinking":"Let me look at the image."}}`,
		`{"message":{"role":"assistant","thinking":" Three bands, so..."}}`,
		`{"message":{"role":"assistant","content":""},"done":true}`,
	)
	defer srv.Close()

	ch, err := NewClientWithBaseURL(srv.URL).Chat(context.Background(), providers.ChatRequest{
		Model:     "gemma4:e2b",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "what is this"}},
		MaxTokens: 512,
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	out, err := drain(t, ch)
	if err == nil {
		t.Fatalf("a thought-only stream must be an error, got %q", out)
	}
	for _, want := range []string{"reasoning", "512"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q so the fix is obvious", err, want)
		}
	}
}

// A model that thinks AND answers is normal, and must not be flagged.
func TestThinkingFollowedByAnAnswerIsFine(t *testing.T) {
	srv := ndjson(t,
		`{"message":{"role":"assistant","thinking":"Considering the image."}}`,
		`{"message":{"role":"assistant","content":"Red, green, blue."}}`,
		`{"message":{"role":"assistant","content":""},"done":true}`,
	)
	defer srv.Close()

	ch, err := NewClientWithBaseURL(srv.URL).Chat(context.Background(), providers.ChatRequest{
		Model:    "gemma4:e2b",
		Messages: []providers.ChatMessage{{Role: "user", Content: "what is this"}},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	out, err := drain(t, ch)
	if err != nil {
		t.Fatalf("a normal reasoning turn must not error: %v", err)
	}
	if out != "Red, green, blue." {
		t.Errorf("content = %q, want the answer only — thinking is not the reply", out)
	}
}

// A genuinely empty answer with no thinking stays a plain empty result: only
// the thought-only case is diagnosable, and claiming otherwise would put a
// wrong explanation in front of the user.
func TestEmptyWithoutThinkingIsNotBlamedOnReasoning(t *testing.T) {
	srv := ndjson(t, `{"message":{"role":"assistant","content":""},"done":true}`)
	defer srv.Close()

	ch, err := NewClientWithBaseURL(srv.URL).Chat(context.Background(), providers.ChatRequest{
		Model:    "tinyllama",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if out, err := drain(t, ch); err != nil || out != "" {
		t.Errorf("got (%q, %v), want an empty result with no error", out, err)
	}
}
