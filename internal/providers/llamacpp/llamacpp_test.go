// internal/providers/llamacpp/llamacpp_test.go
// Purpose: BlackBox P11.4 — the llama.cpp provider speaks llama-server's
// OpenAI-compatible API, needs no API key, and is recognized as local (which is
// what makes it eligible as a Phase 11 offline fallback).
package llamacpp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"helix/internal/providers"
)

func TestBaseURLPrecedenceAndNormalization(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		env        string
		want       string
	}{
		{"default", "", "", DefaultBaseURL},
		{"env override", "", "http://192.168.1.9:9090/v1", "http://192.168.1.9:9090/v1"},
		{"config beats env", "http://config:1/v1", "http://env:2/v1", "http://config:1/v1"},
		// The /v1 omission is the most common llama-server misconfiguration and
		// surfaces as an opaque 404, so it is absorbed rather than diagnosed.
		{"bare host gains /v1", "http://127.0.0.1:8080", "", "http://127.0.0.1:8080/v1"},
		{"trailing slash", "http://127.0.0.1:8080/", "", "http://127.0.0.1:8080/v1"},
		{"already has /v1", "http://127.0.0.1:8080/v1", "", "http://127.0.0.1:8080/v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(URLEnv, tc.env)
			if got := BaseURL(tc.configured); got != tc.want {
				t.Fatalf("BaseURL(%q) with env %q = %q, want %q",
					tc.configured, tc.env, got, tc.want)
			}
		})
	}
}

func TestProviderContractIsLocalAndKeyless(t *testing.T) {
	p := New("http://127.0.0.1:8080/v1", providers.NewHTTPClient(5*time.Second))

	if p.Name() != Name {
		t.Fatalf("Name() = %q, want %q", p.Name(), Name)
	}
	// Local is load-bearing: it exempts the provider from the key requirement,
	// grants the planner's longer local timeouts, and makes it a valid P11.2
	// fallback target (a cloud fallback would not survive an outage).
	if !p.IsLocal() {
		t.Fatal("llama.cpp must report as local")
	}
	if p.RequiresAPIKey() {
		t.Fatal("a local llama-server must not require an API key")
	}
	caps := p.Capabilities()
	if !caps.Chat || !caps.Local {
		t.Fatalf("expected chat+local capabilities, got %+v", caps)
	}
	if !caps.Planner {
		t.Fatal("the provider must be planner-eligible, or it cannot serve a degraded turn")
	}
}

func TestChatRoundTripAgainstLlamaServer(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"pong\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := New(srv.URL, providers.NewHTTPClient(5*time.Second))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := providers.CollectChat(ctx, p, providers.ChatRequest{
		Messages: []providers.ChatMessage{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	if out != "pong" {
		t.Fatalf("expected streamed content %q, got %q", "pong", out)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("llama-server serves /v1/chat/completions, request went to %q", gotPath)
	}
	// A keyless local sidecar must not be sent a bearer header.
	if gotAuth != "" {
		t.Fatalf("unexpected Authorization header %q on a local provider", gotAuth)
	}
	if gotBody["stream"] != true {
		t.Fatalf("expected a streaming request, body was %v", gotBody)
	}
}

func TestHealthCheckReportsUnreachableServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	srv.Close() // closed on purpose: nothing is listening

	p := New(srv.URL, providers.NewHTTPClient(2*time.Second))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// This is the check the P11.2 breaker runs before every switch — it must
	// fail when llama-server is not running, so Helix never degrades onto a
	// brain that is not there.
	if err := p.HealthCheck(ctx); err == nil {
		t.Fatal("HealthCheck must fail when llama-server is unreachable")
	}
}

func TestListModelsReportsLoadedGGUF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"qwen2.5-3b-instruct-q4_k_m.gguf","owned_by":"llamacpp"}]}`)
	}))
	defer srv.Close()

	p := New(srv.URL, providers.NewHTTPClient(5*time.Second))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	models, err := p.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 1 || models[0].ID != "qwen2.5-3b-instruct-q4_k_m.gguf" {
		t.Fatalf("expected the loaded GGUF reported, got %+v", models)
	}
}
