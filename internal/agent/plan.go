package agent

type Plan struct {
	Intent string `json:"intent"`
	Steps  []Step `json:"steps"`
}

type Step struct {
	Tool    string                 `json:"tool"`
	Command string                 `json:"command,omitempty"`
	Message string                 `json:"message,omitempty"`
	Action  string                 `json:"action,omitempty"`
	Name    string                 `json:"name,omitempty"` // for package/git actions if needed
	Args    map[string]interface{} `json:"args,omitempty"`
	Query   string                 `json:"query,omitempty"` // for rag
}

// Helper: does this plan request any RAG calls?
func (p *Plan) HasRAGSteps() bool {
	for _, s := range p.Steps {
		if s.Tool == "rag" {
			return true
		}
	}
	return false
}

// Helper: collect all RAG queries
func (p *Plan) RAGQueries() []string {
	var queries []string
	for _, s := range p.Steps {
		if s.Tool == "rag" && s.Query != "" {
			queries = append(queries, s.Query)
		}
	}
	return queries
}
