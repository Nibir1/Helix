package tui

import (
	"math/rand"
	"time"

	"helix/internal/agent"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// UI States
const (
	StateLoading = iota
	StateMain
	StateConfirm
)

// Msg types
type MatrixTickMsg time.Time
type GlitchTickMsg struct{}  // Triggers a glitch spike
type GlitchResetMsg struct{} // Resets text to normal

type AppModel struct {
	width   int
	height  int
	ready   bool
	uiState int

	// Animation Data
	matrixDrops []int
	bootTime    time.Time

	// Glitch Data
	glitchProb float64 // Current probability of character corruption (0.0 - 1.0)

	confirmMsg   string
	confirmReply chan bool

	agent   *agent.Agent
	agentCh chan tea.Msg

	viewport  viewport.Model
	textInput textinput.Model
	spinner   spinner.Model
	styles    *Styles
	history   []string
}

func NewModel(ag *agent.Agent, agentCh chan tea.Msg) AppModel {
	s := DefaultStyles()

	ti := textinput.New()
	ti.Placeholder = "Awaiting directive..."
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 20
	ti.Prompt = "⚡ "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier)).Bold(true)
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier))

	spin := spinner.New()
	spin.Spinner = spinner.MiniDot
	spin.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier))

	return AppModel{
		textInput:  ti,
		spinner:    spin,
		styles:     s,
		history:    []string{},
		agent:      ag,
		agentCh:    agentCh,
		uiState:    StateLoading,
		bootTime:   time.Now(),
		glitchProb: 0.0, // Start clean
	}
}

func (m AppModel) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.spinner.Tick,
		waitForAgentOutput(m.agentCh),
		tickMatrix(),
		scheduleGlitch(), // Start the random glitch timer
	)
}

func waitForAgentOutput(sub chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-sub
		if !ok {
			return nil
		}
		if text, ok := msg.(string); ok {
			return AgentMsg{Content: text}
		}
		return msg
	}
}

func tickMatrix() tea.Cmd {
	return tea.Tick(time.Millisecond*50, func(t time.Time) tea.Msg {
		return MatrixTickMsg(t)
	})
}

// scheduleGlitch waits a random time (2-5 seconds) before triggering a glitch
func scheduleGlitch() tea.Cmd {
	delay := time.Duration(rand.Intn(3000)+2000) * time.Millisecond
	return tea.Tick(delay, func(t time.Time) tea.Msg {
		return GlitchTickMsg{}
	})
}

// resetGlitch waits a split second (150ms) before fixing the text
func resetGlitch() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return GlitchResetMsg{}
	})
}
