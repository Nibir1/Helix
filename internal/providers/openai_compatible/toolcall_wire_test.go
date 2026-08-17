// internal/providers/openai_compatible/toolcall_wire_test.go
// Purpose: BlackBox P8.7 — the OpenAI-compatible adapter emits the correct
// tools/tool_choice wire shape, reassembles a fragmented tool-call stream, and
// leaves ordinary chat requests byte-identical to before the feature existed.
package openaicompatible

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

// serverCapturing returns a provider plus a pointer to the last request body.
func serverCapturing(t *testing.T, respond func(w http.ResponseWriter)) (*Provider, *map[string]any) {
	t.Helper()
	body := map[string]any{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		respond(w)
	}))
	t.Cleanup(srv.Close)

	p := New(Config{
		Name: "mock", DisplayName: "Mock", BaseURL: srv.URL,
		DefaultModel: "mock-model", Local: true,
	}, providers.NewHTTPClient(5*time.Second))
	return p, &body
}

func TestToolsAbsentFromOrdinaryChatRequest(t *testing.T) {
	p, body := serverCapturing(t, func(w http.ResponseWriter) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := providers.CollectChat(ctx, p, providers.ChatRequest{
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	}); err != nil {
		t.Fatalf("chat: %v", err)
	}

	// The regression that matters most: adding tool support must not alter the
	// wire format of every existing chat call.
	if _, present := (*body)["tools"]; present {
		t.Fatal("a request without tools must not send a tools key")
	}
	if _, present := (*body)["tool_choice"]; present {
		t.Fatal("a request without tools must not send tool_choice")
	}
}

func TestToolsWireShape(t *testing.T) {
	p, body := serverCapturing(t, func(w http.ResponseWriter) {
		fmt.Fprint(w, "data: [DONE]\n\n")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = providers.CollectChatResult(ctx, p, providers.ChatRequest{
		Messages:   []providers.ChatMessage{{Role: "user", Content: "plan this"}},
		Tools:      []providers.ToolDefinition{planTool()},
		ToolChoice: providers.ToolChoiceRequired,
	})

	if (*body)["tool_choice"] != "required" {
		t.Fatalf("tool_choice = %v, want \"required\"", (*body)["tool_choice"])
	}
	tools, ok := (*body)["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one tool on the wire, got %v", (*body)["tools"])
	}
	entry := tools[0].(map[string]any)
	if entry["type"] != "function" {
		t.Fatalf("tool envelope type = %v, want \"function\"", entry["type"])
	}
	fn := entry["function"].(map[string]any)
	if fn["name"] != "emit_plan" {
		t.Fatalf("function name = %v", fn["name"])
	}
	if fn["parameters"] == nil {
		t.Fatal("the JSON Schema must reach the provider, or nothing is enforced")
	}
}

// The full path: a provider streams a tool call in fragments and the caller
// receives one complete, parseable arguments string.
func TestStreamedToolCallReachesCaller(t *testing.T) {
	p, _ := serverCapturing(t, func(w http.ResponseWriter) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"emit_plan\",\"arguments\":\"{\\\"intent\\\":\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"shell\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := providers.CollectChatResult(ctx, p, providers.ChatRequest{
		Messages:   []providers.ChatMessage{{Role: "user", Content: "plan this"}},
		Tools:      []providers.ToolDefinition{planTool()},
		ToolChoice: providers.ToolChoiceRequired,
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d (%+v)", len(res.ToolCalls), res.ToolCalls)
	}
	call := res.ToolCalls[0]
	if call.Name != "emit_plan" || call.ID != "call_1" {
		t.Fatalf("call metadata lost: %+v", call)
	}
	// Consumers must never see partial JSON.
	if !json.Valid([]byte(call.Arguments)) {
		t.Fatalf("assembled arguments are not valid JSON: %q", call.Arguments)
	}
	if call.Arguments != `{"intent":"shell"}` {
		t.Fatalf("arguments = %q", call.Arguments)
	}
}

// "tool_calls" is a distinct finish reason from "stop"; treating only "stop" as
// terminal would hang the stream until the client timed out.
func TestToolCallsFinishReasonTerminatesStream(t *testing.T) {
	p, _ := serverCapturing(t, func(w http.ResponseWriter) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c\",\"function\":{\"name\":\"emit_plan\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		// Deliberately no [DONE]: some providers omit it after a tool call.
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	res, err := providers.CollectChatResult(ctx, p, providers.ChatRequest{
		Messages: []providers.ChatMessage{{Role: "user", Content: "x"}},
		Tools:    []providers.ToolDefinition{planTool()},
	})
	if err != nil {
		t.Fatalf("stream must terminate on finish_reason=tool_calls: %v", err)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected the accumulated call to be delivered, got %+v", res.ToolCalls)
	}
}

// Text-only responses must keep working through the new result path.
func TestCollectChatStillReturnsText(t *testing.T) {
	p, _ := serverCapturing(t, func(w http.ResponseWriter) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"world\"},\"finish_reason\":\"stop\"}]}\n\n")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := providers.CollectChat(ctx, p, providers.ChatRequest{
		Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if out != "hello world" {
		t.Fatalf("text streaming regressed: %q", out)
	}
}
