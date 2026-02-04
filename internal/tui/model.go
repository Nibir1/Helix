package tui

import (
	"helix/internal/agent"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// UI States
const (
	StateMain    = iota // Standard chat/command mode
	StateConfirm        // Modal confirmation dialog (Y/N)
)

type AppModel struct {
	// State
	width   int
	height  int
	ready   bool // Is the UI initialized?
	uiState int  // Current UI state (Main vs Confirm)

	// Interactive Modal Data
	confirmMsg   string    // The question being asked (e.g. "Execute dangerous command?")
	confirmReply chan bool // Channel to send the user's Y/N answer back to the Agent

	// The Brain
	agent   *agent.Agent
	agentCh chan tea.Msg // Channel to receive events (logs or requests) from UX

	// Components
	viewport  viewport.Model  // The scrollable history
	textInput textinput.Model // The command bar
	spinner   spinner.Model   // "Thinking..." animation

	// Theme
	styles *Styles

	// Data
	history []string
}

// NewModel creates the initial TUI state.
// It requires the Agent instance and the channel where the Agent/UX writes output.
func NewModel(ag *agent.Agent, agentCh chan tea.Msg) AppModel {
	ti := textinput.New()
	ti.Placeholder = "Enter command / directive..."
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 20

	// Style the prompt to look like a red cursor
	ti.Prompt = "┃ "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier))
	ti.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorText))
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorSubtle))

	// Configure the spinner (The "Loading" animation)
	s := spinner.New()
	s.Spinner = spinner.Dot // Sci-fi looking polygon
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier))

	return AppModel{
		textInput: ti,
		spinner:   s,
		styles:    DefaultStyles(),
		history:   []string{},
		agent:     ag,
		agentCh:   agentCh,
		uiState:   StateMain,
	}
}

// Helper: Init is the first function called by Bubble Tea
func (m AppModel) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,               // Start cursor blinking
		m.spinner.Tick,                // Start spinner animation
		waitForAgentOutput(m.agentCh), // Start listening for Agent/UX messages immediately
	)
}

// NEW: A "Command" to wait for the next event from the Agent
// This runs in a goroutine managed by Bubble Tea and returns a Msg when data arrives.
func waitForAgentOutput(sub chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-sub
		if !ok {
			return nil // Channel closed
		}

		// If it's a plain string, wrap it in AgentMsg to keep our update switch logic clean.
		// If it's already a struct (like ConfirmationRequest), return it as is.
		if text, ok := msg.(string); ok {
			return AgentMsg{Content: text}
		}
		return msg
	}
}
