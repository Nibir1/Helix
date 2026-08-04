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

// TerminalModeMsg toggles the terminal emulator mode.
type TerminalModeMsg struct {
	Active bool
	Shell  string // "zsh", "bash", "powershell", etc.
}

// TerminalOutputMsg carries raw bytes from the PTY.
type TerminalOutputMsg struct {
	Data []byte
}

// TerminalExitMsg signals that the PTY process has ended.
type TerminalExitMsg struct {
	Err error
}

// TerminalTitleMsg updates the HUD with the current shell title.
type TerminalTitleMsg struct {
	Title string
}

// TerminalBellMsg triggers audio and visual bell.
type TerminalBellMsg struct{}

// TerminalClipboardMsg writes text to the system clipboard.
type TerminalClipboardMsg struct {
	Text string
}
