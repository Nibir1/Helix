package tui

import (
	"helix/internal/agent"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type AppModel struct {
	// State
	width  int
	height int
	ready  bool // Is the UI initialized?

	// The Brain
	agent   *agent.Agent
	agentCh chan string // Channel to receive text from UX

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
func NewModel(ag *agent.Agent, agentCh chan string) AppModel {
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

// NEW: A "Command" to wait for the next line of output from the Agent
// This runs in a goroutine managed by Bubble Tea and returns a Msg when data arrives.
func waitForAgentOutput(sub chan string) tea.Cmd {
	return func() tea.Msg {
		content, ok := <-sub
		if !ok {
			return nil // Channel closed
		}
		return AgentMsg{Content: content}
	}
}
