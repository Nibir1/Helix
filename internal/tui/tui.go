package tui

import (
	"fmt"
	"io"
	"strings"

	"helix/internal/agent"
	"helix/internal/config"
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
		footerHeight := lipgloss.Height(m.inputView())
		verticalMarginHeight := headerHeight + footerHeight

		inputBoxWidth := m.width - 4
		m.textInput.Width = inputBoxWidth - 4

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-verticalMarginHeight)
			m.viewport.YPosition = headerHeight

			// --- CHANGE: Load the Welcome Banner at startup ---
			banner := m.welcomeView()
			m.viewport.SetContent(banner)
			// We treat the banner as the first entry in history so it doesn't disappear on first input
			m.history = append(m.history, banner)

			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - verticalMarginHeight
		}

		if len(m.history) > 0 {
			m.viewport.GotoBottom()
		}

	// Handle Confirmation Request
	case ux.ConfirmationRequest:
		m.uiState = StateConfirm
		m.confirmMsg = msg.Question
		m.confirmReply = msg.ReplyChan
		return m, nil

	// Handle Key Presses
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}

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

		if m.uiState == StateMain {
			if msg.Type == tea.KeyEnter {
				input := m.textInput.Value()
				if input != "" {
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

	baseView := lipgloss.JoinVertical(lipgloss.Left, header, content, footer)

	if m.uiState == StateConfirm {
		return m.overlayView(baseView)
	}

	return baseView
}

// Sub-view: Header (Fixed at the top)
func (m AppModel) headerView() string {
	titleText := " HELIX // RED TEAM // Nahasat Nibir ^;;^"
	title := m.styles.Header.Render(fmt.Sprintf("%s %s", m.spinner.View(), titleText))

	lineWidth := max(0, m.width-lipgloss.Width(title))
	line := m.styles.Status.Render(strings.Repeat("━", lineWidth))

	return lipgloss.JoinHorizontal(lipgloss.Center, title, line)
}

// Sub-view: The Welcome Banner (Displayed in viewport at startup)
func (m AppModel) welcomeView() string {
	// Colors
	// cyan := m.styles.AgentName
	magenta := m.styles.SystemName
	white := m.styles.ModalText

	// The ASCII Art
	art := `
╔════════════════════════════════════════════════════════════════╗
║                                                                ║
║               ██╗  ██╗███████╗██╗     ██╗██╗  ██╗              ║
║               ██║  ██║██╔════╝██║     ██║╚██╗██╔╝              ║
║               ███████║█████╗  ██║     ██║ ╚███╔╝               ║
║               ██╔══██║██╔══╝  ██║     ██║ ██╔██╗               ║
║               ██║  ██║███████╗███████╗██║██╔╝ ██╗              ║
║               ╚═╝  ╚═╝╚══════╝╚══════╝╚═╝╚═╝  ╚═╝              ║
║                                                                ║
╚════════════════════════════════════════════════════════════════╝`

	// Info Lines
	versionLine := fmt.Sprintf("Helix v%s - AI-Powered CLI Assistant", config.HelixVersion)
	authorLine := "Creator - Nahasat Nibir"

	// The Manifesto / Quote
	quote := "- We scream truth through broken amps while empires rot in silence -"

	// Compose the layout centered
	// We use lipgloss.PlaceHorizontal to center each line relative to the window width

	bannerContent := lipgloss.JoinVertical(
		lipgloss.Center,
		magenta.Render(art),
		"", // spacer
		white.Render(versionLine),
		magenta.Render(authorLine),
		"", // spacer
		lipgloss.NewStyle().Foreground(lipgloss.Color(ColorTertiary)).Italic(true).Render(quote),
		"", // spacer
		m.styles.Status.Render(strings.Repeat("─", 50)),
		"",
	)

	// Center the whole block in the available width
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center, bannerContent)
}

// Sub-view: Input Area
func (m AppModel) inputView() string {
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
