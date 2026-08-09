// internal/ai/planner_adapter.go
//
// Purpose: Planner provider adapter with JSON validation and one retry.
//
// Hardening:
//   - planner calls use PlannerTimeout instead of the general long timeout.
package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RunPlannerWithRetry runs the planner prompt and retries once on invalid JSON.
//
// Args:
//   - prompt: full planner prompt.
//
// Returns:
//   - raw planner output or error.
//
// Complexity: O(1) provider round trip(s).
func RunPlannerWithRetry(prompt string) (string, error) {
	cfg := PlannerModelConfig()

	raw, err := RunModelWithTimeout(prompt, cfg, PlannerTimeout)
	if err != nil {
		return "", err
	}

	if isPlannerJSON(raw) {
		return raw, nil
	}

	retryPrompt := fmt.Sprintf(
		"%s\nYour previous output was invalid. Return ONLY a complete JSON object. No markdown. No commentary.\n",
		prompt,
	)

	raw2, err2 := RunModelWithTimeout(retryPrompt, cfg, PlannerTimeout)
	if err2 == nil && isPlannerJSON(raw2) {
		return raw2, nil
	}

	if strings.TrimSpace(raw2) != "" {
		return raw2, nil
	}

	if strings.TrimSpace(raw) != "" {
		return raw, nil
	}

	if err2 != nil {
		return "", err2
	}

	return "", fmt.Errorf("planner returned empty output")
}

// isPlannerJSON reports whether raw model output is a valid planner JSON object.
//
// Args:
//   - raw: raw model output.
//
// Returns:
//   - bool.
//
// Complexity: O(len(raw)).
func isPlannerJSON(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false
	}

	s = plannerStripFences(s)
	candidate := plannerExtractObject(s)
	if candidate == "" {
		return false
	}

	return json.Valid([]byte(candidate))
}

// plannerStripFences removes accidental markdown fences.
//
// Args:
//   - raw: raw model output.
//
// Returns:
//   - cleaned string.
//
// Complexity: O(len(raw)).
func plannerStripFences(raw string) string {
	raw = strings.TrimSpace(raw)

	if strings.HasPrefix(raw, "```") {
		if idx := strings.Index(raw, "\n"); idx >= 0 {
			raw = strings.TrimSpace(raw[idx+1:])
		} else {
			raw = strings.TrimPrefix(raw, "```")
		}
	}

	raw = strings.TrimSuffix(raw, "```")
	return strings.TrimSpace(raw)
}

// plannerExtractObject extracts the outermost JSON object candidate.
//
// Args:
//   - s: candidate text.
//
// Returns:
//   - extracted object or empty string.
//
// Complexity: O(len(s)).
func plannerExtractObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")

	if start == -1 || end == -1 || end <= start {
		return ""
	}

	return strings.TrimSpace(s[start : end+1])
}
