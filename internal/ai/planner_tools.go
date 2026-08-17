// internal/ai/planner_tools.go
//
// Purpose: provider-native tool calling for the planner (BlackBox P8.7).
//
// The planner protocol has always been prompt-enforced: a large block of
// "ABSOLUTE OUTPUT RULES" begs the model for a bare JSON object, and
// RunPlannerWithRetry then burns up to three round trips repairing markdown
// fences, prose preambles, and truncated braces. Providers that implement
// function calling enforce the schema at the API level instead — the model
// physically cannot answer with prose, and arguments come back as validated
// JSON. That removes the failure mode the retry ladder exists to paper over.
//
// Safety (guardrail §12 #3): this changes only HOW the plan arrives. The
// returned arguments go through the exact same ParsePlanFromModelOutput →
// validatePlan → firewall → risk tiers → sandbox path as a prompt-produced
// plan. Nothing about tool calling grants a step more authority, and the
// planner still cannot invent tools — the schema's enum is closed.
package ai

import (
	"fmt"
	"strings"

	"helix/internal/providers"
)

// PlannerToolName is the function the planner is asked to call. It is
// deliberately not named after a shell action: the model is emitting a PLAN
// for Helix to validate, not executing anything.
const PlannerToolName = "emit_plan"

// plannerToolDefinition mirrors the schema documented in BuildPlannerPrompt.
//
// The two enums are the load-bearing part: they close the tool and intent
// vocabularies at the API level, so a model cannot invent a sixth tool the
// executor has never heard of. validatePlan still enforces the same set —
// this is defense in depth, not a replacement.
func plannerToolDefinition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: PlannerToolName,
		Description: "Emit the execution plan for the user's request. " +
			"Every action Helix takes must be expressed as a step in this plan.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"intent": map[string]any{
					"type":        "string",
					"enum":        []string{"chat", "shell", "git", "package", "multi_step"},
					"description": "Use multi_step when the plan has two or more steps.",
				},
				"steps": map[string]any{
					"type":     "array",
					"minItems": 1,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"tool": map[string]any{
								"type": "string",
								"enum": []string{"response", "shell", "git", "package", "recon", "web"},
							},
							"message": map[string]any{
								"type":        "string",
								"description": "Text reply. Only for tool=response.",
							},
							"command": map[string]any{
								"type":        "string",
								"description": "Single-line shell command. Only for tool=shell.",
							},
							"action": map[string]any{
								"type":        "string",
								"description": "Sub-action for git, package, recon, and web tools (web: search|fetch).",
							},
							"args": map[string]any{
								"type":                 "object",
								"additionalProperties": map[string]any{"type": "string"},
								"description":          "String-valued arguments for the action.",
							},
						},
						"required": []string{"tool"},
					},
				},
			},
			"required": []string{"intent", "steps"},
		},
	}
}

// runPlannerNative attempts the tool-calling path.
//
// It returns ok=false for every failure mode — unsupported provider, transport
// error, no tool call, wrong tool, empty arguments — so the caller silently
// falls back to the prompt ladder. That is the whole risk posture of this
// feature: native tool calling is an optimization, never a new way for a turn
// to fail. A provider that advertises tool support and then misbehaves costs
// one round trip, not the turn.
//
// Args: prompt: the standard planner prompt.
// Returns: raw plan JSON and whether the native path succeeded.
// Complexity: O(1) provider round trip.
func runPlannerNative(prompt string) (string, bool) {
	if !ToolCallingAvailable() {
		return "", false
	}

	res, err := RunToolCall(
		prompt,
		[]providers.ToolDefinition{plannerToolDefinition()},
		// Required, not auto: the planner's job is to produce a plan, and a
		// chatty model answering in prose is the exact failure this replaces.
		providers.ToolChoiceRequired,
		PlannerModelConfig(),
		plannerTimeout(true),
	)
	if err != nil {
		return "", false
	}

	args, ok := plannerToolArguments(res)
	if !ok {
		return "", false
	}
	return args, true
}

// plannerToolArguments extracts the plan JSON from a tool-calling response.
//
// The tool name is checked rather than assumed: a model may emit parallel or
// hallucinated calls, and accepting arguments from an unexpected function
// would feed the plan parser something it never asked for.
func plannerToolArguments(res providers.ChatResult) (string, bool) {
	for _, call := range res.ToolCalls {
		if call.Name != PlannerToolName {
			continue
		}
		args := strings.TrimSpace(call.Arguments)
		if args == "" {
			continue
		}
		return args, true
	}
	return "", false
}

// PlannerTransport names the mechanism that produced the last plan, for
// /doctor-style reporting and debug output.
func PlannerTransport() string {
	if ToolCallingAvailable() {
		return fmt.Sprintf("native tool calling (%s)", ActiveProviderName())
	}
	return "prompt-enforced JSON"
}
