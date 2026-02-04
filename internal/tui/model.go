package tui

import (
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

	// Components
	viewport  viewport.Model  // The scrollable history
	textInput textinput.Model // The command bar
	spinner   spinner.Model   // "Thinking..." animation

	// Theme
	styles *Styles

	// Data (Placeholder for now, we'll hook up Agent later)
	history []string
}

func NewModel() AppModel {
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
	s.Spinner = spinner.Dot // A sci-fi looking polygon
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier))

	return AppModel{
		textInput: ti,
		spinner:   s,
		styles:    DefaultStyles(),
		history:   []string{},
	}
}

// Helper: Init is the first function called by Bubble Tea
func (m AppModel) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink, // Start cursor blinking
		m.spinner.Tick,  // Start spinner animation
	)
}
