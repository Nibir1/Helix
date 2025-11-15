package agent

// Plan is the JSON structure the LLM must output.
type Plan struct {
	Intent string `json:"intent"`
	Steps  []Step `json:"steps"`
}

// Step represents a single tool call or response message.
type Step struct {
	Tool    string                 `json:"tool"`
	Command string                 `json:"command,omitempty"`
	Action  string                 `json:"action,omitempty"`
	Args    map[string]interface{} `json:"args,omitempty"`
	Message string                 `json:"message,omitempty"`
}
