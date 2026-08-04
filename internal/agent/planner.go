// internal/agent/planner.go

package agent

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
	Query   string                 `json:"query,omitempty"`   // rag tool query
}

// PlannerResult is the full JSON object the planner LLM must output.
type PlannerResult struct {
	Intent Intent        `json:"intent"` // chat | shell | git | package | multi_step
	Steps  []PlannerStep `json:"steps"`
}
