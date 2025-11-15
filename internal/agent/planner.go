package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"helix/internal/ai"
	"helix/internal/utils"
)

// =========================================================
// TYPES — MUST MATCH BuildPlannerPrompt EXACTLY
// =========================================================

// PlannerResult represents the agent plan produced by the LLM.
type PlannerResult struct {
	Intent string        `json:"intent"`
	Steps  []PlannerStep `json:"steps"`
}

// PlannerStep represents an individual step of the plan.
type PlannerStep struct {
	Tool    string         `json:"tool,omitempty"`
	Message string         `json:"message,omitempty"` // chat response
	Command string         `json:"command,omitempty"` // shell command
	Action  string         `json:"action,omitempty"`  // git/package action
	Args    map[string]any `json:"args,omitempty"`    // arbitrary key-value
	Name    string         `json:"name,omitempty"`    // package name
}

// =========================================================
// PLANNER — CALLS LLM AND RETURNS STRUCTURED PLAN
// =========================================================

// PlanFromLLM produces the PlannerResult for a user input.
func PlanFromLLM(prompt string) (*PlannerResult, error) {

	raw, err := ai.RunModel(prompt)
	if err != nil {
		return nil, fmt.Errorf("AI planner error: %w", err)
	}

	// Clean junk — sometimes LLM prints extra text accidentally
	clean, err := extractJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("LLM did not return valid JSON: %w\nRaw: %s", err, raw)
	}

	// Parse JSON into struct
	var result PlannerResult
	if err := json.Unmarshal([]byte(clean), &result); err != nil {
		return nil, fmt.Errorf("invalid plan JSON: %w\nJSON: %s", err, clean)
	}

	// Validate required fields
	if result.Intent == "" {
		return nil, fmt.Errorf("planner missing intent field")
	}
	if len(result.Steps) == 0 {
		return nil, fmt.Errorf("planner returned zero steps")
	}

	return &result, nil
}

// =========================================================
// JSON EXTRACTION / SANITIZATION
// =========================================================

// extractJSON attempts to pull the JSON object out of the model output.
func extractJSON(raw string) (string, error) {

	if raw == "" {
		return "", fmt.Errorf("empty LLM output")
	}

	// Remove whitespace around output
	raw = strings.TrimSpace(raw)

	// Case 1: output already starts with '{'
	if strings.HasPrefix(raw, "{") {
		return raw, nil
	}

	// Case 2: find first '{' and last '}'
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")

	if start == -1 || end == -1 || end <= start {
		return "", fmt.Errorf("no complete JSON object found")
	}

	clean := raw[start : end+1]
	clean = strings.TrimSpace(clean)

	// LAST chance: prune obvious junk (markdown, backticks)
	clean = strings.ReplaceAll(clean, "```json", "")
	clean = strings.ReplaceAll(clean, "```", "")
	clean = strings.TrimSpace(clean)

	// Minimal JSON validation (ensure matching braces)
	if !utils.BracesBalanced(clean) {
		return "", fmt.Errorf("planner returned unbalanced JSON")
	}

	return clean, nil
}
