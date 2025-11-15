package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fatih/color"
)

// Intent represents what the agent is trying to do at a high level.
type Intent string

const (
	IntentChat      Intent = "chat"
	IntentShell     Intent = "shell"
	IntentGit       Intent = "git"
	IntentPackage   Intent = "package"
	IntentMultiStep Intent = "multi_step"
)

// PlannerStep is a single step in the agent plan, produced by the LLM planner.
type PlannerStep struct {
	Tool    string                 `json:"tool"`              // "shell", "git", "package", "response"
	Command string                 `json:"command,omitempty"` // for tool="shell"
	Action  string                 `json:"action,omitempty"`  // for tool="git"/"package"
	Name    string                 `json:"name,omitempty"`    // package name, tag name, etc.
	Args    map[string]interface{} `json:"args,omitempty"`    // extra structured args (commit message, etc.)
	Message string                 `json:"message,omitempty"` // for tool="response"
}

// PlannerResult is the full JSON object the planner LLM must output.
type PlannerResult struct {
	Intent Intent        `json:"intent"` // chat | shell | git | package | multi_step
	Steps  []PlannerStep `json:"steps"`
}

// decodePlannerResult takes the raw LLM output and tries to parse it into PlannerResult.
// It is defensive against extra text before/after the JSON block.
func decodePlannerResult(raw string) (*PlannerResult, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty planner output")
	}

	// Try to isolate the JSON object if the model added extra chatter
	cleaned := extractJSONBlock(raw)
	if cleaned == "" {
		cleaned = raw
	}

	color.Yellow("🔍 Planner raw JSON candidate: %s", truncate(cleaned, 400))

	var plan PlannerResult
	if err := json.Unmarshal([]byte(cleaned), &plan); err != nil {
		return nil, fmt.Errorf("failed to parse planner JSON: %w", err)
	}

	// Default intent to "chat" if missing
	if plan.Intent == "" {
		plan.Intent = IntentChat
	}

	return &plan, nil
}

// extractJSONBlock tries to pull out the first top-level {...} block from the string.
// This helps when the model accidentally adds text before/after the JSON.
func extractJSONBlock(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end == -1 || end <= start {
		return ""
	}

	candidate := s[start : end+1]
	return strings.TrimSpace(candidate)
}

// truncate is just for debug printing, to avoid spamming the terminal.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
