package tui

import (
	"fmt"
	"io"
	"strings"

	"helix/internal/agent"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Update handles events (keyframes, keypresses, messages)
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
		spCmd tea.Cmd
	)

	switch msg := msg.(type) {

	// Window Resize Event - CRITICAL for TUI
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.styles = DefaultStyles() // Refresh styles on resize

		// Calculate heights
		headerHeight := lipgloss.Height(m.headerView())
		inputHeight := lipgloss.Height(m.inputView())
		verticalMarginHeight := headerHeight + inputHeight

		// --- Resizing Logic ---
		// Dynamic Input Width: Window width minus borders/padding (approx 5 chars)
		// We ensure it doesn't go below a safe minimum to avoid panics
		newInputWidth := msg.Width - 5
		if newInputWidth < 10 {
			newInputWidth = 10
		}
		m.textInput.Width = newInputWidth

		if !m.ready {
			// Initialize viewport logic on first draw
			m.viewport = viewport.New(msg.Width, msg.Height-verticalMarginHeight)
			m.viewport.YPosition = headerHeight
			m.viewport.SetContent("⚡ INITIALIZING HELIX RED TEAM PROTOCOL...")
			m.ready = true
		} else {
			// Resize existing viewport
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - verticalMarginHeight
		}

		// Keep viewport at bottom on resize if content exists
		if len(m.history) > 0 {
			m.viewport.GotoBottom()
		}

	// Key Press Events
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit

		case tea.KeyEnter:
			// Handle User Input
			input := m.textInput.Value()
			if input != "" {
				// 1. Render User Input immediately (Visual feedback)
				userLine := fmt.Sprintf("%s %s", m.styles.SystemName.Render("USER >"), input)
				m.history = append(m.history, userLine)

				// Update Viewport
				m.viewport.SetContent(strings.Join(m.history, "\n"))
				m.viewport.GotoBottom()

				// Clear Input
				m.textInput.Reset()

				// 2. Trigger the Agent (Async)
				return m, tea.Batch(
					m.spinner.Tick,                // Start spinning
					runAgent(m.agent, input),      // Run logic in background
					waitForAgentOutput(m.agentCh), // Ensure we are listening for the reply
				)
			}
		}

	// Message received from the Agent (via UX channel)
	case AgentMsg:
		// Add the line to history
		m.history = append(m.history, msg.Content)

		// Update Viewport
		m.viewport.SetContent(strings.Join(m.history, "\n"))
		m.viewport.GotoBottom()

		// CONTINUOUS LOOP: Wait for the next line
		return m, waitForAgentOutput(m.agentCh)

	// Agent finished processing the specific input
	case SessionDoneMsg:
		// Ensure the input is focused so user can type immediately
		m.textInput.Focus()
		return m, textinput.Blink
	}

	// Update Bubbles components
	m.textInput, tiCmd = m.textInput.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)
	m.spinner, spCmd = m.spinner.Update(msg)

	return m, tea.Batch(tiCmd, vpCmd, spCmd)
}

// View renders the UI string
func (m AppModel) View() string {
	if !m.ready {
		return "\n  Initializing The Grid..."
	}

	// 1. Render Header
	header := m.headerView()

	// 2. Render Main Content (Viewport)
	content := m.styles.Viewport.Render(m.viewport.View())

	// 3. Render Footer (Input)
	footer := m.inputView()

	// 4. Join vertically
	return fmt.Sprintf("%s\n%s\n%s", header, content, footer)
}

// Sub-view: Header
func (m AppModel) headerView() string {
	// Base Title
	titleText := " HELIX // RED TEAM // Nahasat Nibir "

	// Include spinner in header
	title := m.styles.Header.Render(fmt.Sprintf("%s %s", m.spinner.View(), titleText))

	line := m.styles.Status.Render(strings.Repeat("─", max(0, m.width-lipgloss.Width(title))))
	return lipgloss.JoinHorizontal(lipgloss.Center, title, line)
}

// Sub-view: Input Area
func (m AppModel) inputView() string {
	// Ensure the container width matches the window width minus margins
	// We use max() to prevent negative width panic on very small terminals
	safeWidth := max(0, m.width-4)
	return m.styles.Input.Width(safeWidth).Render(m.textInput.View())
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Entry Point - Updated to accept Agent, Channel, and Output Writer
func Start(ag *agent.Agent, agentCh chan string, output io.Writer) error {
	// Pass dependencies to NewModel
	// tea.WithOutput(output) ensures we write to the REAL terminal, not the hijacked stdout
	p := tea.NewProgram(
		NewModel(ag, agentCh),
		tea.WithAltScreen(),
		tea.WithOutput(output),
	)
	_, err := p.Run()
	return err
}

// Async command to run the Agent logic
func runAgent(ag *agent.Agent, input string) tea.Cmd {
	return func() tea.Msg {
		// This runs in a background goroutine.
		// The Agent will write to the 'agentCh' via the UX SetOutputHandler.
		// We just wait for HandleInput to return to signal completion.
		ag.HandleInput(input)
		return SessionDoneMsg{}
	}
}
