// internal/providers/anthropic/toolcall_test.go
// Purpose: BlackBox P8.7b — the Anthropic adapter emits Messages-format tool
// definitions and reassembles `tool_use` content blocks from their streamed
// `input_json_delta` fragments.
package anthropic

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

func planTool() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "emit_plan",
		Description: "Emit the execution plan.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"intent": map[string]any{"type": "string"}},
			"required":   []string{"intent"},
		},
	}
}

// anthropicServer returns a provider pointed at a stub plus the captured body.
func anthropicServer(t *testing.T, sse string) (*Provider, *map[string]any) {
	t.Helper()
	body := map[string]any{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	t.Cleanup(srv.Close)

	p := New("test-key", providers.NewHTTPClient(5*time.Second))
	p.endpoint = srv.URL
	return p, &body
}

// Anthropic names the schema field input_schema and puts it at the top level —
// not nested under `function` like OpenAI. Getting this wrong is a silent 400.
func TestAnthropicToolWireShape(t *testing.T) {
	p, body := anthropicServer(t, "data: {\"type\":\"message_stop\"}\n\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = providers.CollectChatResult(ctx, p, providers.ChatRequest{
		Messages:   []providers.ChatMessage{{Role: "user", Content: "plan this"}},
		Tools:      []providers.ToolDefinition{planTool()},
		ToolChoice: providers.ToolChoiceRequired,
	})

	tools, ok := (*body)["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one tool on the wire, got %v", (*body)["tools"])
	}
	entry := tools[0].(map[string]any)
	if entry["name"] != "emit_plan" {
		t.Fatalf("tool name = %v", entry["name"])
	}
	if entry["input_schema"] == nil {
		t.Fatal("Anthropic requires input_schema; a nested function.parameters is a 400")
	}
	if _, wrong := entry["function"]; wrong {
		t.Fatal("the OpenAI function envelope must not be sent to Anthropic")
	}

	// "required" maps to Anthropic's {"type":"any"} object form, not a string.
	choice, ok := (*body)["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice must be an object, got %v", (*body)["tool_choice"])
	}
	if choice["type"] != "any" {
		t.Fatalf("required must map to type \"any\", got %v", choice["type"])
	}
}

func TestAnthropicToolsAbsentFromOrdinaryChat(t *testing.T) {
	p, body := anthropicServer(t,
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"+
			"data: {\"type\":\"message_stop\"}\n\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := providers.CollectChat(ctx, p, providers.ChatRequest{
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

// The reassembly path: a tool_use block opens with id+name, then arguments
// arrive as input_json_delta fragments.
func TestAnthropicStreamedToolUseReassembles(t *testing.T) {
	sse := "data: {\"type\":\"content_block_start\",\"index\":0," +
		"\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"emit_plan\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0," +
		"\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"intent\\\":\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0," +
		"\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"shell\\\"}\"}}\n\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	p, _ := anthropicServer(t, sse)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := providers.CollectChatResult(ctx, p, providers.ChatRequest{
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
	if call.Name != "emit_plan" || call.ID != "toolu_1" {
		t.Fatalf("call metadata lost: %+v", call)
	}
	if !json.Valid([]byte(call.Arguments)) {
		t.Fatalf("assembled arguments are not valid JSON: %q", call.Arguments)
	}
	if call.Arguments != `{"intent":"shell"}` {
		t.Fatalf("arguments = %q", call.Arguments)
	}
}

// Text and a tool call can appear in one response; both must survive.
func TestAnthropicMixedTextAndToolUse(t *testing.T) {
	sse := "data: {\"type\":\"content_block_delta\",\"index\":0," +
		"\"delta\":{\"type\":\"text_delta\",\"text\":\"thinking...\"}}\n\n" +
		"data: {\"type\":\"content_block_start\",\"index\":1," +
		"\"content_block\":{\"type\":\"tool_use\",\"id\":\"t2\",\"name\":\"emit_plan\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":1," +
		"\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	p, _ := anthropicServer(t, sse)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := providers.CollectChatResult(ctx, p, providers.ChatRequest{
		Messages: []providers.ChatMessage{{Role: "user", Content: "plan"}},
		Tools:    []providers.ToolDefinition{planTool()},
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if res.Text != "thinking..." {
		t.Fatalf("text lost alongside the tool call: %q", res.Text)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "emit_plan" {
		t.Fatalf("tool call lost alongside text: %+v", res.ToolCalls)
	}
}

// A stream that ends without message_stop must still deliver what it assembled
// rather than silently dropping the call (the existing no-hang guarantee).
func TestAnthropicTruncatedStreamStillDelivers(t *testing.T) {
	sse := "data: {\"type\":\"content_block_start\",\"index\":0," +
		"\"content_block\":{\"type\":\"tool_use\",\"id\":\"t\",\"name\":\"emit_plan\"}}\n\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0," +
		"\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n"

	p, _ := anthropicServer(t, sse)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := providers.CollectChatResult(ctx, p, providers.ChatRequest{
		Messages: []providers.ChatMessage{{Role: "user", Content: "plan"}},
		Tools:    []providers.ToolDefinition{planTool()},
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("a truncated stream must still deliver assembled calls, got %+v", res.ToolCalls)
	}
}
