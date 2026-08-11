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
	"time"
)

// RunPlannerWithRetry runs the planner prompt with progressive retry.
//
// Local providers receive longer timeouts because Gemma 4 and other local
// models can be much slower than hosted APIs, especially on CPU.
//
// Args:
//   - prompt: planner prompt.
//
// Returns: raw planner output or error.
// Complexity: O(provider round trips).
func RunPlannerWithRetry(prompt string) (string, error) {
	cfg := PlannerModelConfig()

	initialTimeout := PlannerTimeout
	retryTimeout := 30 * time.Second

	if activeProvider != nil && activeProvider.IsLocal() {
		initialTimeout = 180 * time.Second
		retryTimeout = 90 * time.Second
	}

	// Attempt 1: standard config.
	raw, err := RunModelWithTimeout(prompt, cfg, initialTimeout)
	if err == nil && isPlannerJSON(raw) {
		return raw, nil
	}

	// Attempt 2: explicit correction instruction.
	retryPrompt := fmt.Sprintf(
		"%s\nYour previous output was invalid. Return ONLY a complete JSON object. No markdown. No commentary.\n",
		prompt,
	)

	raw2, err2 := RunModelWithTimeout(retryPrompt, cfg, retryTimeout)
	if err2 == nil && isPlannerJSON(raw2) {
		return raw2, nil
	}

	// Attempt 3: raise temperature slightly.
	cfgWarm := cfg
	cfgWarm.Temperature = 0.4

	raw3, err3 := RunModelWithTimeout(retryPrompt, cfgWarm, retryTimeout)
	if err3 == nil && isPlannerJSON(raw3) {
		return raw3, nil
	}

	// Return the best non-empty result we got.
	for _, r := range []string{raw3, raw2, raw} {
		if strings.TrimSpace(r) != "" {
			return r, nil
		}
	}

	if err3 != nil {
		return "", err3
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
