// internal/providers/openai_compatible/max_tokens_test.go
// Purpose: the completion-bound field, against servers that reject the wrong one.
//
// From a real session, on the openai provider:
//
//	Planner model error: HTTP 400: {"error":{"message":"Unsupported parameter:
//	'max_tokens' is not supported with this model. Use
//	'max_completion_tokens' instead.", ... "code":"unsupported_parameter"}}
//
// Every turn failed. There is no single field that works everywhere — OpenAI's
// reasoning models reject max_tokens, and llama.cpp/Ollama/Groq understand only
// max_tokens — so the adapter guesses, reads the server's correction, and
// remembers it.
package openaicompatible

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"helix/internal/providers"
)

// reasoningModelServer rejects max_tokens exactly as OpenAI does, and accepts
// max_completion_tokens.
func reasoningModelServer(t *testing.T, seen *[]map[string]any, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		*seen = append(*seen, body)
		mu.Unlock()

		if _, bad := body["max_tokens"]; bad {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.","type":"invalid_request_error","param":"max_tokens","code":"unsupported_parameter"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
}

// legacyServer accepts only max_tokens, as llama.cpp and Ollama do.
func legacyServer(t *testing.T, seen *[]map[string]any, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		*seen = append(*seen, body)
		mu.Unlock()

		if _, bad := body["max_completion_tokens"]; bad {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unrecognized request argument supplied: max_completion_tokens. Use 'max_tokens' instead.","type":"invalid_request_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
}

func newProvider(t *testing.T, name, model, url string) *Provider {
	t.Helper()
	return New(Config{
		Name: name, DisplayName: name, BaseURL: url,
		APIKey: "test", DefaultModel: model,
	}, providers.NewHTTPClient(10*1e9))
}

func collect(t *testing.T, p *Provider, model string) error {
	t.Helper()
	_, err := providers.CollectChat(context.Background(), p, providers.ChatRequest{
		Model:     model,
		Messages:  []providers.ChatMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 256,
	})
	return err
}

// TestReasoningModelUsesMaxCompletionTokensFirst: a known reasoning model must
// get the right field on the FIRST request, with no wasted round trip.
func TestReasoningModelUsesMaxCompletionTokensFirst(t *testing.T) {
	var mu sync.Mutex
	var seen []map[string]any
	srv := reasoningModelServer(t, &seen, &mu)
	defer srv.Close()

	p := newProvider(t, "openai", "gpt-5.6-luna", srv.URL)
	if err := collect(t, p, "gpt-5.6-luna"); err != nil {
		t.Fatalf("reasoning model should succeed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("made %d requests, want 1 — the field should be right first time", len(seen))
	}
	if _, ok := seen[0]["max_completion_tokens"]; !ok {
		t.Errorf("expected max_completion_tokens, body was %v", seen[0])
	}
	if _, bad := seen[0]["max_tokens"]; bad {
		t.Error("max_tokens must not be sent to a reasoning model")
	}
}

// TestUnknownModelRecoversFromRejection is the case that actually bit: a model
// not on any list, rejected, then corrected from the server's own message.
func TestUnknownModelRecoversFromRejection(t *testing.T) {
	var mu sync.Mutex
	var seen []map[string]any
	srv := reasoningModelServer(t, &seen, &mu)
	defer srv.Close()

	// A name no table would match, on a server that rejects max_tokens.
	p := newProvider(t, "openai", "some-future-model", srv.URL)
	if err := collect(t, p, "some-future-model"); err != nil {
		t.Fatalf("the adapter should recover from the rejection: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("made %d requests, want 2 (reject then corrected)", len(seen))
	}
	if _, ok := seen[0]["max_tokens"]; !ok {
		t.Errorf("first attempt should use max_tokens: %v", seen[0])
	}
	if _, ok := seen[1]["max_completion_tokens"]; !ok {
		t.Errorf("retry should use max_completion_tokens: %v", seen[1])
	}
}

// TestCorrectionIsRememberedPerModel: the recovery must cost one extra round trip
// per model per process, not one per call.
func TestCorrectionIsRememberedPerModel(t *testing.T) {
	var mu sync.Mutex
	var seen []map[string]any
	srv := reasoningModelServer(t, &seen, &mu)
	defer srv.Close()

	p := newProvider(t, "openai", "some-future-model", srv.URL)
	for i := 0; i < 3; i++ {
		if err := collect(t, p, "some-future-model"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	// 2 for the first call (reject + retry), then 1 each.
	if len(seen) != 4 {
		t.Fatalf("made %d requests, want 4 — the correction is not being cached", len(seen))
	}
	for _, body := range seen[1:] {
		if _, bad := body["max_tokens"]; bad {
			t.Errorf("a cached correction was ignored: %v", body)
		}
	}
}

// TestLegacyServerKeepsMaxTokens: llama.cpp, Ollama and Groq understand only
// max_tokens, and must not be handed the new field.
func TestLegacyServerKeepsMaxTokens(t *testing.T) {
	var mu sync.Mutex
	var seen []map[string]any
	srv := legacyServer(t, &seen, &mu)
	defer srv.Close()

	p := newProvider(t, "llamacpp", "local-gguf", srv.URL)
	if err := collect(t, p, "local-gguf"); err != nil {
		t.Fatalf("legacy server should succeed on the first try: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("made %d requests, want 1", len(seen))
	}
	if _, ok := seen[0]["max_tokens"]; !ok {
		t.Errorf("expected max_tokens for a local runtime: %v", seen[0])
	}
}

// TestLegacyServerCorrectsAReasoningGuess covers the reverse direction: a proxy
// named like a reasoning model that only speaks max_tokens.
func TestLegacyServerCorrectsAReasoningGuess(t *testing.T) {
	var mu sync.Mutex
	var seen []map[string]any
	srv := legacyServer(t, &seen, &mu)
	defer srv.Close()

	p := newProvider(t, "custom", "gpt-5-proxy", srv.URL)
	if err := collect(t, p, "gpt-5-proxy"); err != nil {
		t.Fatalf("should recover in the other direction too: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("made %d requests, want 2", len(seen))
	}
	if _, ok := seen[1]["max_tokens"]; !ok {
		t.Errorf("retry should fall back to max_tokens: %v", seen[1])
	}
}

// TestUnrelatedBadRequestIsNotRetried: only a rejection that names both fields
// is a field problem. Retrying anything else would mask the real cause and
// double the cost of every failure.
func TestUnrelatedBadRequestIsNotRetried(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid value for 'temperature'","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	p := newProvider(t, "openai", "gpt-4o", srv.URL)
	if err := collect(t, p, "gpt-4o"); err == nil {
		t.Fatal("the 400 should surface")
	}

	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Errorf("made %d requests, want 1 — an unrelated 400 must not be retried", hits)
	}
}

// TestNoMaxTokensSendsNeitherField: a request with no bound must not gain one.
func TestNoMaxTokensSendsNeitherField(t *testing.T) {
	var mu sync.Mutex
	var seen []map[string]any
	srv := legacyServer(t, &seen, &mu)
	defer srv.Close()

	p := newProvider(t, "openai", "gpt-4o", srv.URL)
	_, err := providers.CollectChat(context.Background(), p, providers.ChatRequest{
		Model:    "gpt-4o",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unbounded request: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, f := range []string{"max_tokens", "max_completion_tokens"} {
		if _, present := seen[0][f]; present {
			t.Errorf("%s was sent for an unbounded request: %v", f, seen[0])
		}
	}
}

func TestPreferredMaxTokensField(t *testing.T) {
	reasoning := []string{
		"gpt-5", "gpt-5.6-luna", "gpt-5.4-mini", "o1", "o1-preview",
		"o3", "o3-mini", "o4-mini", "O3-MINI",
	}
	for _, m := range reasoning {
		if got := providers.PreferredMaxTokensField("openai", m); got != providers.FieldMaxCompletionTokens {
			t.Errorf("PreferredMaxTokensField(%q) = %q, want max_completion_tokens", m, got)
		}
	}
	// gpt-4-turbo accepts ONLY max_tokens, so the rule cannot be "all OpenAI".
	legacy := []string{
		"gpt-4o", "gpt-4-turbo", "gpt-4", "gpt-3.5-turbo",
		"local-gguf", "llama3.1:8b", "deepseek-chat", "grok-4.6", "", "o-not-a-model",
	}
	for _, m := range legacy {
		if got := providers.PreferredMaxTokensField("openai", m); got != providers.FieldMaxTokens {
			t.Errorf("PreferredMaxTokensField(%q) = %q, want max_tokens", m, got)
		}
	}
}

func TestAlternateMaxTokensField(t *testing.T) {
	if got := providers.AlternateMaxTokensField(providers.FieldMaxTokens); got != providers.FieldMaxCompletionTokens {
		t.Errorf("alternate of max_tokens = %q", got)
	}
	if got := providers.AlternateMaxTokensField(providers.FieldMaxCompletionTokens); got != providers.FieldMaxTokens {
		t.Errorf("alternate of max_completion_tokens = %q", got)
	}
}
