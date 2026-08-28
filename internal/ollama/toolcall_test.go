// internal/ollama/toolcall_test.go
// Purpose: BlackBox P8.7b — the Ollama adapter sends OpenAI-shaped tool
// definitions to /api/chat and normalizes its object-valued tool arguments
// into the string form every consumer expects.
package ollama

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

func toolStub(t *testing.T, ndjson string) (*Client, *map[string]any) {
	t.Helper()
	body := map[string]any{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = fmt.Fprint(w, ndjson)
	}))
	t.Cleanup(srv.Close)

	return NewClientWithBaseURL(srv.URL), &body
}

func planTool() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "emit_plan",
		Description: "Emit the execution plan.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"intent": map[string]any{"type": "string"}},
		},
	}
}

func TestOllamaToolWireShape(t *testing.T) {
	c, body := toolStub(t, `{"done":true}`+"\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = providers.CollectChatResult(ctx, ollamaProvider{c}, providers.ChatRequest{
		Model:      "llama3.1:8b",
		Messages:   []providers.ChatMessage{{Role: "user", Content: "plan"}},
		Tools:      []providers.ToolDefinition{planTool()},
		ToolChoice: providers.ToolChoiceRequired,
	})

	tools, ok := (*body)["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one tool on the wire, got %v", (*body)["tools"])
	}
	entry := tools[0].(map[string]any)
	if entry["type"] != "function" {
		t.Fatalf("Ollama uses the OpenAI function envelope, got type=%v", entry["type"])
	}
	fn := entry["function"].(map[string]any)
	if fn["name"] != "emit_plan" || fn["parameters"] == nil {
		t.Fatalf("tool definition malformed: %v", fn)
	}

	// Ollama has no tool_choice; sending one would be a fabricated parameter.
	if _, present := (*body)["tool_choice"]; present {
		t.Error("Ollama has no tool_choice — it must not be sent")
	}
}

func TestOllamaToolsAbsentFromOrdinaryChat(t *testing.T) {
	c, body := toolStub(t, `{"message":{"content":"hi"},"done":true}`+"\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := providers.CollectChat(ctx, ollamaProvider{c}, providers.ChatRequest{
		Model:    "llama3.1:8b",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if out != "hi" {
		t.Fatalf("text streaming regressed: %q", out)
	}
	if _, present := (*body)["tools"]; present {
		t.Fatal("a request without tools must not send a tools key")
	}
}

// The normalization that matters: Ollama returns arguments as a JSON OBJECT,
// while ToolCall.Arguments is the raw JSON string every consumer parses.
func TestOllamaObjectArgumentsBecomeJSONString(t *testing.T) {
	ndjson := `{"message":{"tool_calls":[{"function":{"name":"emit_plan",` +
		`"arguments":{"intent":"shell","steps":[{"tool":"shell","command":"ls"}]}}}]}}` + "\n" +
		`{"done":true}` + "\n"

	c, _ := toolStub(t, ndjson)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := providers.CollectChatResult(ctx, ollamaProvider{c}, providers.ChatRequest{
		Model:    "llama3.1:8b",
		Messages: []providers.ChatMessage{{Role: "user", Content: "plan"}},
		Tools:    []providers.ToolDefinition{planTool()},
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d (%+v)", len(res.ToolCalls), res.ToolCalls)
	}
	call := res.ToolCalls[0]
	if call.Name != "emit_plan" {
		t.Fatalf("name = %q", call.Name)
	}
	if !json.Valid([]byte(call.Arguments)) {
		t.Fatalf("arguments must be a valid JSON string, got %q", call.Arguments)
	}

	// And it must round-trip to the same structure the model emitted.
	var parsed struct {
		Intent string `json:"intent"`
		Steps  []struct {
			Tool    string `json:"tool"`
			Command string `json:"command"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &parsed); err != nil {
		t.Fatalf("arguments do not parse: %v (%q)", err, call.Arguments)
	}
	if parsed.Intent != "shell" || len(parsed.Steps) != 1 || parsed.Steps[0].Command != "ls" {
		t.Fatalf("argument content lost in normalization: %+v", parsed)
	}
}

// Tool calls must be delivered exactly once, on the terminating frame — the
// same contract the streaming adapters honor.
func TestOllamaToolCallsDeliveredOnceOnDone(t *testing.T) {
	ndjson := `{"message":{"tool_calls":[{"function":{"name":"a","arguments":{}}}]}}` + "\n" +
		`{"message":{"content":"and some text"}}` + "\n" +
		`{"message":{"tool_calls":[{"function":{"name":"b","arguments":{}}}]}}` + "\n" +
		`{"done":true}` + "\n"

	c, _ := toolStub(t, ndjson)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := providers.CollectChatResult(ctx, ollamaProvider{c}, providers.ChatRequest{
		Model:    "llama3.1:8b",
		Messages: []providers.ChatMessage{{Role: "user", Content: "plan"}},
		Tools:    []providers.ToolDefinition{planTool()},
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(res.ToolCalls) != 2 {
		t.Fatalf("expected both calls collected once, got %+v", res.ToolCalls)
	}
	if res.ToolCalls[0].Name != "a" || res.ToolCalls[1].Name != "b" {
		t.Fatalf("calls out of order: %+v", res.ToolCalls)
	}
	if res.Text != "and some text" {
		t.Fatalf("interleaved text lost: %q", res.Text)
	}
}

func TestOllamaNamelessToolCallIgnored(t *testing.T) {
	ndjson := `{"message":{"tool_calls":[{"function":{"name":"","arguments":{}}}]}}` + "\n" +
		`{"done":true}` + "\n"

	c, _ := toolStub(t, ndjson)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, _ := providers.CollectChatResult(ctx, ollamaProvider{c}, providers.ChatRequest{
		Model:    "llama3.1:8b",
		Messages: []providers.ChatMessage{{Role: "user", Content: "plan"}},
		Tools:    []providers.ToolDefinition{planTool()},
	})
	if len(res.ToolCalls) != 0 {
		t.Fatalf("a nameless call is unusable and must be dropped, got %+v", res.ToolCalls)
	}
}

// ollamaProvider adapts the client to providers.AIProvider for CollectChat.
type ollamaProvider struct{ c *Client }

func (o ollamaProvider) Name() string        { return "ollama" }
func (o ollamaProvider) DisplayName() string { return "Ollama" }
func (o ollamaProvider) SetAPIKey(string)    {}
func (o ollamaProvider) ListModels(ctx context.Context) ([]providers.ModelInfo, error) {
	return o.c.ListModels(ctx)
}
func (o ollamaProvider) HealthCheck(ctx context.Context) error { return o.c.Health(ctx) }
func (o ollamaProvider) RequiresAPIKey() bool                  { return false }
func (o ollamaProvider) IsLocal() bool                         { return true }
func (o ollamaProvider) DefaultModel() string                  { return "llama3.1:8b" }
func (o ollamaProvider) Capabilities() providers.Capabilities {
	return providers.CapabilitiesFor("ollama", "llama3.1:8b")
}
func (o ollamaProvider) Chat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamChunk, error) {
	return o.c.Chat(ctx, req)
}
