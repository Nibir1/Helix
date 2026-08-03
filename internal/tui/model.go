// internal/tui/model.go

package tui

import (
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
	StateText
)

// Log Types for Color Coding
const (
	LogTypeUser = iota
	LogTypeInfo
	LogTypeSuccess
	LogTypeError
	LogTypeWarning
	LogTypeDebug
	LogTypeAgent
)

// LogEntry replaces simple strings for history
type LogEntry struct {
	Type      int
	Content   string
	Timestamp time.Time
}

// Msg types
type MatrixTickMsg time.Time
type GlitchTickMsg struct{}
type GlitchResetMsg struct{}
type SystemTickMsg time.Time    // Triggers HUD updates
type TypewriterTickMsg struct{} // New: Triggers character streaming

type AppModel struct {
	width   int
	height  int
	ready   bool
	uiState int

	// Animation / Glitch Data
	matrixDrops []int
	bootTime    time.Time
	glitchProb  float64

	// HUD Data (Real-time Resources)
	memUsage    string
	activeProcs int

	// NEW: Typing Effect Data
	isTyping    bool      // Are we currently streaming text?
	pendingText string    // The full message waiting to be typed
	typedSoFar  string    // What has been typed on screen so far
	typingType  int       // The LogType of the message being typed
	typingStart time.Time // When the current message stream started

	// Interactive Modal Data
	confirmMsg   string
	confirmReply chan bool

	// Phase 0: free-form text modal state.
	textPrompt string
	textReply  chan string
	modalInput textinput.Model

	agent   *agent.Agent
	agentCh chan tea.Msg

	viewport  viewport.Model
	textInput textinput.Model
	spinner   spinner.Model
	styles    *Styles

	// History is now structured
	history []LogEntry
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

	// Phase 0: modal text input for typed confirmations and line prompts.
	mi := textinput.New()
	mi.Placeholder = "Type here..."
	mi.CharLimit = 256
	mi.Width = 60
	mi.Prompt = "> "
	mi.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier)).Bold(true)
	mi.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier))

	spin := spinner.New()
	spin.Spinner = spinner.MiniDot
	spin.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier))

	return AppModel{
		textInput:   ti,
		spinner:     spin,
		styles:      s,
		history:     []LogEntry{},
		agent:       ag,
		agentCh:     agentCh,
		uiState:     StateLoading,
		bootTime:    time.Now(),
		glitchProb:  0.0,
		memUsage:    "0 MB",
		activeProcs: 1,
		isTyping:    false,
		modalInput:  mi,
	}
}

func (m AppModel) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.spinner.Tick,
		waitForAgentOutput(m.agentCh),
		tickMatrix(),
		scheduleGlitch(),
		tickSystem(), // Start the HUD heartbeat
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

func scheduleGlitch() tea.Cmd {
	// Random glitch every 2-5s
	delay := time.Duration(2000+(time.Now().UnixNano()%3000)/1e6) * time.Millisecond
	return tea.Tick(delay, func(t time.Time) tea.Msg {
		return GlitchTickMsg{}
	})
}

func resetGlitch() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return GlitchResetMsg{}
	})
}

// Update system stats every 1 second
func tickSystem() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return SystemTickMsg(t)
	})
}

// NEW: Helper for fast typing ticks (20ms = approx 50 chars/sec)
func tickTypewriter() tea.Cmd {
	return tea.Tick(20*time.Millisecond, func(t time.Time) tea.Msg {
		return TypewriterTickMsg{}
	})
}
