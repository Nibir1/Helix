package tui

import (
	"fmt"
	"io"
	"strings"

	"helix/internal/agent"
	"helix/internal/ux"

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

	// Window Resize Event
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.styles = DefaultStyles()

		headerHeight := lipgloss.Height(m.headerView())
		inputHeight := lipgloss.Height(m.inputView())
		verticalMarginHeight := headerHeight + inputHeight

		// Dynamic Input Width
		newInputWidth := msg.Width - 5
		if newInputWidth < 10 {
			newInputWidth = 10
		}
		m.textInput.Width = newInputWidth

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-verticalMarginHeight)
			m.viewport.YPosition = headerHeight
			m.viewport.SetContent("⚡ INITIALIZING HELIX RED TEAM PROTOCOL...")
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - verticalMarginHeight
		}

		if len(m.history) > 0 {
			m.viewport.GotoBottom()
		}

	// ---------------------------------------------------------
	// NEW: Handle Confirmation Request (Modal Trigger)
	// ---------------------------------------------------------
	case ux.ConfirmationRequest:
		m.uiState = StateConfirm
		m.confirmMsg = msg.Question
		m.confirmReply = msg.ReplyChan
		// We return immediately to render the modal overlay
		return m, nil

	// ---------------------------------------------------------
	// Handle Key Presses (Context Dependent)
	// ---------------------------------------------------------
	case tea.KeyMsg:
		// Global Quit
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}

		// MODE: CONFIRMATION (Modal)
		if m.uiState == StateConfirm {
			switch msg.String() {
			case "y", "Y":
				m.confirmReply <- true
				m.uiState = StateMain
			case "n", "N":
				m.confirmReply <- false
				m.uiState = StateMain
			}
			return m, nil
		}

		// MODE: MAIN (Chat)
		if m.uiState == StateMain {
			if msg.Type == tea.KeyEnter {
				input := m.textInput.Value()
				if input != "" {
					userLine := fmt.Sprintf("%s %s", m.styles.SystemName.Render("USER >"), input)
					m.history = append(m.history, userLine)

					m.viewport.SetContent(strings.Join(m.history, "\n"))
					m.viewport.GotoBottom()
					m.textInput.Reset()

					return m, tea.Batch(
						m.spinner.Tick,
						runAgent(m.agent, input),
						waitForAgentOutput(m.agentCh),
					)
				}
			}
		}

	// Message received from the Agent (via UX channel)
	case AgentMsg:
		m.history = append(m.history, msg.Content)
		m.viewport.SetContent(strings.Join(m.history, "\n"))
		m.viewport.GotoBottom()
		return m, waitForAgentOutput(m.agentCh)

	// Agent finished processing
	case SessionDoneMsg:
		m.textInput.Focus()
		return m, textinput.Blink
	}

	// Update Bubbles components (Only update input if in Main mode)
	if m.uiState == StateMain {
		m.textInput, tiCmd = m.textInput.Update(msg)
	}
	m.viewport, vpCmd = m.viewport.Update(msg)
	m.spinner, spCmd = m.spinner.Update(msg)

	return m, tea.Batch(tiCmd, vpCmd, spCmd)
}

// View renders the UI string
func (m AppModel) View() string {
	if !m.ready {
		return "\n  Initializing The Grid..."
	}

	// Base Layer
	header := m.headerView()
	content := m.styles.Viewport.Render(m.viewport.View())
	footer := m.inputView()
	baseView := fmt.Sprintf("%s\n%s\n%s", header, content, footer)

	// Overlay Layer (Modal)
	if m.uiState == StateConfirm {
		return m.overlayView(baseView)
	}

	return baseView
}

// Sub-view: Header
func (m AppModel) headerView() string {
	titleText := " HELIX // Creator - Nahasat Nibir "
	title := m.styles.Header.Render(fmt.Sprintf("%s %s", m.spinner.View(), titleText))
	line := m.styles.Status.Render(strings.Repeat("─", max(0, m.width-lipgloss.Width(title))))
	return lipgloss.JoinHorizontal(lipgloss.Center, title, line)
}

// Sub-view: Input Area
func (m AppModel) inputView() string {
	safeWidth := max(0, m.width-4)
	return m.styles.Input.Width(safeWidth).Render(m.textInput.View())
}

// Sub-view: Modal Overlay
func (m AppModel) overlayView(base string) string {
	// Create the dialog box
	dialog := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color(ColorRectifier)).
		Background(lipgloss.Color(ColorVoid)).
		Padding(1, 2).
		Align(lipgloss.Center).
		Render(fmt.Sprintf(
			"⚠️  CONFIRM INSTRUCTION  ⚠️\n\n%s\n\n[Y] Proceed    [N] Abort",
			m.styles.AgentName.Render(m.confirmMsg),
		))

	// Center it over the entire screen
	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		dialog,
		lipgloss.WithWhitespaceChars(" "),
		// We don't dim the background because it's hard to read;
		// instead, the popup just floats over the existing text.
	)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Entry Point
func Start(ag *agent.Agent, agentCh chan tea.Msg, output io.Writer) error {
	p := tea.NewProgram(
		NewModel(ag, agentCh),
		tea.WithAltScreen(),
		tea.WithOutput(output),
	)
	_, err := p.Run()
	return err
}

func runAgent(ag *agent.Agent, input string) tea.Cmd {
	return func() tea.Msg {
		ag.HandleInput(input)
		return SessionDoneMsg{}
	}
}
