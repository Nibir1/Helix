// internal/tui/messages.go

package tui

// AgentMsg represents a line of text coming from the Helix Brain
type AgentMsg struct {
	Content string
}

// ErrorMsg allows the Agent to report critical failures
type ErrorMsg error

// SessionDoneMsg indicates the Agent has finished a task
type SessionDoneMsg struct{}
