// internal/providers/openai_compatible/openai_compatible_test.go
package openaicompatible

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"helix/internal/providers"
)

func TestOpenAICompatibleListAndChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"test-model","owned_by":"test"}]}`))

		case "/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := providers.NewHTTPClient(5 * time.Second)

	p := New(Config{
		Name:         "test",
		DisplayName:  "Test",
		BaseURL:      server.URL,
		APIKey:       "test-key", // FIX: Provide dummy API key to pass RequiresAPIKey() check
		DefaultModel: "test-model",
	}, client)

	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("list models failed: %v", err)
	}

	if len(models) != 1 || models[0].ID != "test-model" {
		t.Fatalf("unexpected models: %+v", models)
	}

	ch, err := p.Chat(context.Background(), providers.ChatRequest{
		Model: "test-model",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}

	out := ""

	for chunk := range ch {
		if chunk.Error != nil {
			t.Fatalf("stream error: %v", chunk.Error)
		}

		out += chunk.Content

		if chunk.Done {
			break
		}
	}

	if out != "Hello" {
		t.Fatalf("expected Hello, got %q", out)
	}
}
