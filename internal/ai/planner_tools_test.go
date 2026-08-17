// internal/ai/planner_tools_test.go
// Purpose: BlackBox P8.7 — the planner tool schema matches the prompt contract,
// tool-call arguments feed the SAME validation path as prompt-produced JSON,
// and every native-path failure degrades silently to the prompt ladder.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"helix/internal/providers"
)

func TestPlannerToolSchemaMatchesPromptContract(t *testing.T) {
	def := plannerToolDefinition()
	if def.Name != PlannerToolName {
		t.Fatalf("tool name = %q, want %q", def.Name, PlannerToolName)
	}

	raw, err := json.Marshal(def.Parameters)
	if err != nil {
		t.Fatalf("schema must marshal to JSON Schema: %v", err)
	}
	var schema struct {
		Type       string `json:"type"`
		Required   []string
		Properties struct {
			Intent struct {
				Enum []string `json:"enum"`
			} `json:"intent"`
			Steps struct {
				Type     string `json:"type"`
				MinItems int    `json:"minItems"`
				Items    struct {
					Required   []string `json:"required"`
					Properties struct {
						Tool struct {
							Enum []string `json:"enum"`
						} `json:"tool"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"steps"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	// The closed enums are the load-bearing part: they stop a model inventing
	// a tool the executor has never heard of, at the API level.
	wantTools := map[string]bool{
		"response": true, "shell": true, "git": true, "package": true, "recon": true,
	}
	got := schema.Properties.Steps.Items.Properties.Tool.Enum
	if len(got) != len(wantTools) {
		t.Fatalf("tool enum = %v, want exactly the 5 executor tools", got)
	}
	for _, tool := range got {
		if !wantTools[tool] {
			t.Errorf("schema offers tool %q that the executor does not implement", tool)
		}
	}

	wantIntents := map[string]bool{
		"chat": true, "shell": true, "git": true, "package": true, "multi_step": true,
	}
	for _, in := range schema.Properties.Intent.Enum {
		if !wantIntents[in] {
			t.Errorf("schema offers intent %q the planner does not define", in)
		}
	}

	// A plan with no steps is meaningless; the schema must say so.
	if schema.Properties.Steps.MinItems != 1 {
		t.Errorf("steps.minItems = %d, want 1", schema.Properties.Steps.MinItems)
	}
}

// The safety-critical property (guardrail §12 #3): tool-call output is NOT
// trusted more than prompt output — it goes through the same parser and
// validator, so an invalid plan is rejected identically.
func TestToolArgumentsFlowThroughNormalValidation(t *testing.T) {
	args := `{"intent":"shell","steps":[{"tool":"shell","command":"ls -la"}]}`
	plan, err := ParsePlanFromModelOutput(args)
	if err != nil {
		t.Fatalf("valid tool arguments must parse: %v", err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Command != "ls -la" {
		t.Fatalf("plan mis-parsed: %+v", plan)
	}

	// And a bogus tool name is refused just as it would be from the prompt path.
	if _, err := ParsePlanFromModelOutput(
		`{"intent":"shell","steps":[{"tool":"nuke","command":"x"}]}`); err == nil {
		t.Fatal("an unknown tool must be rejected regardless of transport")
	}
}

func TestPlannerToolArgumentsSelectsTheRightCall(t *testing.T) {
	res := providers.ChatResult{ToolCalls: []providers.ToolCall{
		// A hallucinated or parallel call must not be mistaken for the plan.
		{Name: "some_other_tool", Arguments: `{"evil":true}`},
		{Name: PlannerToolName, Arguments: `{"intent":"chat","steps":[]}`},
	}}
	got, ok := plannerToolArguments(res)
	if !ok {
		t.Fatal("the planner call must be found among several")
	}
	if got != `{"intent":"chat","steps":[]}` {
		t.Fatalf("wrong call selected: %q", got)
	}
}

func TestPlannerToolArgumentsRejectsUnusable(t *testing.T) {
	cases := map[string]providers.ChatResult{
		"no calls":     {Text: "here is a plan in prose"},
		"wrong tool":   {ToolCalls: []providers.ToolCall{{Name: "other", Arguments: "{}"}}},
		"empty args":   {ToolCalls: []providers.ToolCall{{Name: PlannerToolName, Arguments: "   "}}},
		"empty result": {},
	}
	for name, res := range cases {
		if _, ok := plannerToolArguments(res); ok {
			t.Errorf("%s: must not yield planner arguments", name)
		}
	}
}

// toolFake serves a scripted response so the native path can be exercised
// without a network or a key.
type toolFake struct {
	name      string
	sawTools  bool
	sawChoice string
	calls     []providers.ToolCall
	text      string
	err       error
}

func (p *toolFake) Name() string        { return p.name }
func (p *toolFake) DisplayName() string { return p.name }
func (p *toolFake) SetAPIKey(string)    {}
func (p *toolFake) ListModels(context.Context) ([]providers.ModelInfo, error) {
	return nil, nil
}
func (p *toolFake) HealthCheck(context.Context) error { return nil }
func (p *toolFake) RequiresAPIKey() bool              { return false }
func (p *toolFake) IsLocal() bool                     { return false }
func (p *toolFake) DefaultModel() string              { return "gpt-4o" }
func (p *toolFake) Capabilities() providers.Capabilities {
	return providers.CapabilitiesFor(p.name, "gpt-4o")
}
func (p *toolFake) Chat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamChunk, error) {
	p.sawTools = len(req.Tools) > 0
	p.sawChoice = req.ToolChoice
	if p.err != nil {
		return nil, p.err
	}
	ch := make(chan providers.StreamChunk, 2)
	ch <- providers.StreamChunk{Content: p.text, ToolCalls: p.calls, Done: true}
	close(ch)
	return ch, nil
}

// withProvider installs a fake as the active provider for one test.
func withProvider(t *testing.T, p providers.AIProvider, model string) {
	t.Helper()
	oldP, oldM := activeProvider, activeModel
	t.Cleanup(func() { activeProvider, activeModel = oldP, oldM })
	activeProvider, activeModel = p, model
}

func TestRunPlannerNativeUsesRequiredToolChoice(t *testing.T) {
	fake := &toolFake{name: "openai", calls: []providers.ToolCall{
		{Name: PlannerToolName, Arguments: `{"intent":"shell","steps":[{"tool":"shell","command":"ls"}]}`},
	}}
	withProvider(t, fake, "gpt-4o")

	raw, ok := runPlannerNative("plan something")
	if !ok {
		t.Fatal("native path should have succeeded on a tool-capable provider")
	}
	if !fake.sawTools {
		t.Fatal("tool definitions must be sent")
	}
	// Required, not auto: a chatty model answering in prose is the exact
	// failure native calling is meant to eliminate.
	if fake.sawChoice != providers.ToolChoiceRequired {
		t.Fatalf("tool_choice = %q, want %q", fake.sawChoice, providers.ToolChoiceRequired)
	}
	if !isPlannerJSON(raw) {
		t.Fatalf("native path must return parseable plan JSON, got %q", raw)
	}
}

// Every native failure mode must degrade silently — tool calling is an
// optimization, never a new way for a turn to fail.
func TestRunPlannerNativeFallsBackOnEveryFailure(t *testing.T) {
	cases := map[string]*toolFake{
		"transport error": {name: "openai", err: errors.New("HTTP 500: boom")},
		"prose instead of a call": {name: "openai",
			text: `{"intent":"chat","steps":[]}`},
		"wrong tool": {name: "openai", calls: []providers.ToolCall{
			{Name: "not_the_planner", Arguments: `{"intent":"chat","steps":[]}`}}},
		"empty arguments": {name: "openai", calls: []providers.ToolCall{
			{Name: PlannerToolName, Arguments: ""}}},
	}
	for name, fake := range cases {
		t.Run(name, func(t *testing.T) {
			withProvider(t, fake, "gpt-4o")
			if _, ok := runPlannerNative("plan something"); ok {
				t.Fatal("this failure mode must fall through to the prompt ladder")
			}
		})
	}
}

// A provider without native support must not be probed at all.
func TestRunPlannerNativeSkipsUnsupportedProvider(t *testing.T) {
	fake := &toolFake{name: "ollama", calls: []providers.ToolCall{
		{Name: PlannerToolName, Arguments: `{"intent":"chat","steps":[]}`},
	}}
	withProvider(t, fake, "gemma4:e2b")

	if _, ok := runPlannerNative("plan something"); ok {
		t.Fatal("an adapter without native tool support must not use the native path")
	}
	if fake.sawTools {
		t.Fatal("no request should have been made at all — that is the wasted round trip")
	}
}

func TestToolCallingAvailableTracksActiveProvider(t *testing.T) {
	withProvider(t, &toolFake{name: "openai"}, "gpt-4o")
	if !ToolCallingAvailable() {
		t.Fatal("a tool-capable active provider must report available")
	}
	withProvider(t, &toolFake{name: "ollama"}, "gemma4:e2b")
	if ToolCallingAvailable() {
		t.Fatal("a local provider must report unavailable")
	}
	if PlannerTransport() != "prompt-enforced JSON" {
		t.Fatalf("transport reporting wrong: %q", PlannerTransport())
	}
}
