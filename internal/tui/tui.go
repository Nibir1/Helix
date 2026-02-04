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

		// Calculate heights dynamically based on styles
		headerHeight := lipgloss.Height(m.headerView())
		footerHeight := lipgloss.Height(m.inputView()) // Now includes border height

		// Total vertical chrome
		verticalMarginHeight := headerHeight + footerHeight

		// Dynamic Input Width calculation
		// We subtract borders (2) and padding (2) from the input container style
		inputBoxWidth := m.width - 4
		m.textInput.Width = inputBoxWidth - 4 // Internal text width

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-verticalMarginHeight)
			m.viewport.YPosition = headerHeight
			// Initial Sci-Fi Boot Message
			m.viewport.SetContent(m.styles.SystemName.Render("⚡ SYSTEM ONLINE: INITIALIZING NEURAL LINK..."))
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - verticalMarginHeight
		}

		if len(m.history) > 0 {
			m.viewport.GotoBottom()
		}

	// ---------------------------------------------------------
	// Handle Confirmation Request (Modal Trigger)
	// ---------------------------------------------------------
	case ux.ConfirmationRequest:
		m.uiState = StateConfirm
		m.confirmMsg = msg.Question
		m.confirmReply = msg.ReplyChan
		return m, nil

	// ---------------------------------------------------------
	// Handle Key Presses
	// ---------------------------------------------------------
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}

		// MODAL MODE
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

		// CHAT MODE
		if m.uiState == StateMain {
			if msg.Type == tea.KeyEnter {
				input := m.textInput.Value()
				if input != "" {
					// Render User Input with "Iso" styling
					// [USER] > input
					userLabel := m.styles.SystemName.Render("[USER]")
					userLine := fmt.Sprintf("%s › %s", userLabel, input)

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

	// Message from Agent
	case AgentMsg:
		m.history = append(m.history, msg.Content)
		m.viewport.SetContent(strings.Join(m.history, "\n"))
		m.viewport.GotoBottom()
		return m, waitForAgentOutput(m.agentCh)

	case SessionDoneMsg:
		m.textInput.Focus()
		return m, textinput.Blink
	}

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

	header := m.headerView()
	content := m.styles.Viewport.Render(m.viewport.View())
	footer := m.inputView()

	// Join vertically
	baseView := lipgloss.JoinVertical(lipgloss.Left, header, content, footer)

	if m.uiState == StateConfirm {
		return m.overlayView(baseView)
	}

	return baseView
}

// Sub-view: Header
func (m AppModel) headerView() string {
	titleText := " HELIX // Creator - Nahasat Nibir ^;;^ "
	title := m.styles.Header.Render(fmt.Sprintf("%s %s", m.spinner.View(), titleText))

	// Create a line that fills the rest of the width
	lineWidth := max(0, m.width-lipgloss.Width(title))
	line := m.styles.Status.Render(strings.Repeat("━", lineWidth))

	return lipgloss.JoinHorizontal(lipgloss.Center, title, line)
}

// Sub-view: Input Area
func (m AppModel) inputView() string {
	// The border takes up 2 width, padding 2 width
	return m.styles.Input.Width(m.width - 2).Render(m.textInput.View())
}

// Sub-view: Modal Overlay
func (m AppModel) overlayView(base string) string {
	msg := m.styles.ModalText.Render(m.confirmMsg)

	dialog := m.styles.ModalBorder.Render(fmt.Sprintf(
		"⚠️  CRITICAL DECISION REQUIRED  ⚠️\n\n%s\n\n[Y] EXECUTE    [N] ABORT",
		msg,
	))

	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		dialog,
		lipgloss.WithWhitespaceChars(" "),
	)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

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
