// internal/ollama/client_test.go
package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"helix/internal/providers"
)

func TestOllamaListAndChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"phi4-mini"}]}`))

		case "/api/chat":
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte(`{"message":{"content":"Hi"},"done":false}` + "\n"))
			_, _ = w.Write([]byte(`{"message":{"content":""},"done":true}` + "\n"))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClientWithBaseURL(server.URL)

	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("list models failed: %v", err)
	}

	if len(models) != 1 || models[0].ID != "phi4-mini" {
		t.Fatalf("unexpected models: %+v", models)
	}

	ch, err := client.Chat(context.Background(), providers.ChatRequest{
		Model: "phi4-mini",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
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

	if out != "Hi" {
		t.Fatalf("expected Hi, got %q", out)
	}
}
