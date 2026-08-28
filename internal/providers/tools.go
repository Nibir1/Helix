// internal/providers/tools.go
//
// Purpose: shared native tool-calling plumbing (BlackBox P8.7 / P8.7b).
//
// Three adapters now speak tools over three different wires — OpenAI-compatible
// (`tools` + `tool_choice`, streamed as indexed argument fragments), Anthropic
// (`tools` with `input_schema`, streamed as `tool_use` blocks with
// `input_json_delta`), and Ollama (`/api/chat` with OpenAI-shaped tool
// definitions but complete tool calls whose arguments arrive as a JSON OBJECT
// rather than a string). The reassembly rules and the OpenAI-shaped definition
// envelope are identical across them, so they live here rather than being
// re-derived — and re-bugged — per adapter.
package providers

import "sort"

// ToolsToOpenAIWire renders tool definitions in the
// {"type":"function","function":{...}} envelope used by both OpenAI-compatible
// endpoints and Ollama's /api/chat.
func ToolsToOpenAIWire(tools []ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		fn := map[string]any{"name": t.Name}
		if t.Description != "" {
			fn["description"] = t.Description
		}
		if t.Parameters != nil {
			fn["parameters"] = t.Parameters
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

// ToolsToAnthropicWire renders tool definitions in Anthropic's Messages
// format, which is flat and names the schema field `input_schema`.
func ToolsToAnthropicWire(tools []ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		entry := map[string]any{"name": t.Name}
		if t.Description != "" {
			entry["description"] = t.Description
		}
		// input_schema is required by the API; an empty object is the valid
		// "takes no arguments" form.
		if t.Parameters != nil {
			entry["input_schema"] = t.Parameters
		} else {
			entry["input_schema"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, entry)
	}
	return out
}

// AnthropicToolChoice maps a normalized ToolChoice onto Anthropic's object
// form. Anthropic spells "you must call some tool" as type "any"; an empty
// choice returns nil so the field is omitted entirely.
func AnthropicToolChoice(choice string) map[string]any {
	switch choice {
	case ToolChoiceRequired:
		return map[string]any{"type": "any"}
	case ToolChoiceAuto:
		return map[string]any{"type": "auto"}
	default:
		return nil
	}
}

// ToolCallAccumulator reassembles streamed tool-call fragments.
//
// Every streaming provider splits a tool call across frames: an opening frame
// carries the id and function name, later frames append slices of the JSON
// argument string, and calls issued in parallel interleave. Fragments are
// therefore keyed and ordered by the provider's own index, never by arrival.
type ToolCallAccumulator struct {
	order []int
	byIdx map[int]*ToolCall
}

// NewToolCallAccumulator creates an empty accumulator.
func NewToolCallAccumulator() *ToolCallAccumulator {
	return &ToolCallAccumulator{byIdx: map[int]*ToolCall{}}
}

// Add merges one fragment. Id and name are set only when non-empty (they
// arrive once, on the opening frame); arguments accumulate.
func (a *ToolCallAccumulator) Add(idx int, id, name, args string) {
	tc, ok := a.byIdx[idx]
	if !ok {
		tc = &ToolCall{}
		a.byIdx[idx] = tc
		a.order = append(a.order, idx)
	}
	if id != "" {
		tc.ID = id
	}
	if name != "" {
		tc.Name = name
	}
	tc.Arguments += args
}

// Assemble returns completed calls in index order.
//
// Nameless entries are dropped: a fragment that never received a function name
// means a truncated stream, and forwarding it would surface downstream as an
// unparseable tool call rather than as the transport failure it is.
func (a *ToolCallAccumulator) Assemble() []ToolCall {
	if len(a.order) == 0 {
		return nil
	}
	sort.Ints(a.order)
	out := make([]ToolCall, 0, len(a.order))
	for _, idx := range a.order {
		if tc := a.byIdx[idx]; tc.Name != "" {
			out = append(out, *tc)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
