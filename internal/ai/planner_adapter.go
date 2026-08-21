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
	// P8.7: when the provider enforces the schema natively, the JSON-repair
	// ladder below is unnecessary — the model cannot return prose or a fenced
	// block. Any failure falls through to the prompt path unchanged, so this
	// is a fast path, never a new failure mode.
	if raw, ok := runPlannerNative(prompt); ok && isPlannerJSON(raw) {
		return raw, nil
	}

	cfg := PlannerModelConfig()

	// Timeouts are recomputed per attempt rather than once up front: the P11.2
	// breaker can flip the brain from cloud to local BETWEEN these attempts
	// (two failed calls is the default threshold), and a CPU-bound local model
	// handed a 30s cloud-sized budget would time out on the very attempt that
	// was supposed to rescue the turn.
	// Attempt 1: standard config.
	raw, err := runModelKind(KindPlanner, prompt, cfg, plannerTimeout(true))
	if err == nil && isPlannerJSON(raw) {
		return raw, nil
	}

	// Attempt 2: explicit correction instruction.
	retryPrompt := fmt.Sprintf(
		"%s\nYour previous output was invalid. Return ONLY a complete JSON object. No markdown. No commentary.\n",
		prompt,
	)

	raw2, err2 := runModelKind(KindPlanner, retryPrompt, cfg, plannerTimeout(false))
	if err2 == nil && isPlannerJSON(raw2) {
		return raw2, nil
	}

	// Attempt 3: raise temperature slightly.
	cfgWarm := cfg
	cfgWarm.Temperature = 0.4

	raw3, err3 := runModelKind(KindPlanner, retryPrompt, cfgWarm, plannerTimeout(false))
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

// plannerTimeout returns the budget for one planner attempt, sized to the
// provider that will actually serve it.
//
// Local providers get far longer budgets because Gemma 4 and other local models
// can be much slower than hosted APIs, especially on CPU.
//
// Args:
//   - first: true for the initial attempt, false for a correction retry.
//
// Returns: the timeout for this attempt.
// Complexity: O(1).
func plannerTimeout(first bool) time.Duration {
	p, _, _ := resolveProvider()
	if p != nil && p.IsLocal() {
		if first {
			return 180 * time.Second
		}
		return 90 * time.Second
	}
	if first {
		return PlannerTimeout
	}
	return 30 * time.Second
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
