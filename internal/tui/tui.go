package tui

import (
	"fmt"
	"io"
	"math/rand"
	"strings"
	"time"

	"helix/internal/agent"
	"helix/internal/config"
	"helix/internal/ux"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
		spCmd tea.Cmd
	)

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.styles = DefaultStyles()

		headerHeight := lipgloss.Height(m.headerView())
		footerHeight := lipgloss.Height(m.inputView())
		verticalMarginHeight := headerHeight + footerHeight

		inputBoxWidth := m.width - 4
		m.textInput.Width = inputBoxWidth - 4

		if m.matrixDrops == nil || len(m.matrixDrops) != m.width {
			m.matrixDrops = make([]int, m.width)
			for i := range m.matrixDrops {
				m.matrixDrops[i] = rand.Intn(m.height) * -1
			}
		}

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-verticalMarginHeight)
			m.viewport.YPosition = headerHeight
			m.history = append(m.history, m.welcomeView())
			m.viewport.SetContent(m.renderHistory())
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - verticalMarginHeight
			m.viewport.SetContent(m.renderHistory())
		}
		if len(m.history) > 0 {
			m.viewport.GotoBottom()
		}

	case MatrixTickMsg:
		if m.uiState == StateLoading {
			if time.Since(m.bootTime) > 6*time.Second {
				m.uiState = StateMain
				return m, nil
			}
			for i := range m.matrixDrops {
				m.matrixDrops[i]++
				if m.matrixDrops[i] > m.height+rand.Intn(5) {
					m.matrixDrops[i] = rand.Intn(10) * -1
				}
			}
			return m, tickMatrix()
		}

	// ---------------------------------------------------------
	// GLITCH LOGIC
	// ---------------------------------------------------------
	case GlitchTickMsg:
		m.glitchProb = 0.15 // SPIKE

		// FIX: Regenerate the banner with the new glitch probability
		if len(m.history) > 0 {
			m.history[0] = m.welcomeView()
			m.viewport.SetContent(m.renderHistory())
		}

		return m, resetGlitch()

	case GlitchResetMsg:
		m.glitchProb = 0.0 // RESET

		// FIX: Regenerate the banner (clean it up)
		if len(m.history) > 0 {
			m.history[0] = m.welcomeView()
			m.viewport.SetContent(m.renderHistory())
		}

		return m, scheduleGlitch()

	case ux.ConfirmationRequest:
		m.uiState = StateConfirm
		m.confirmMsg = msg.Question
		m.confirmReply = msg.ReplyChan
		return m, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		if m.uiState == StateLoading {
			m.uiState = StateMain
			return m, nil
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
					m.viewport.SetContent(m.renderHistory())
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

	case AgentMsg:
		m.history = append(m.history, msg.Content)
		m.viewport.SetContent(m.renderHistory())
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

func (m AppModel) renderHistory() string {
	var b strings.Builder
	wrapWidth := m.viewport.Width - 4
	if wrapWidth < 10 {
		wrapWidth = 10
	}
	wrapper := lipgloss.NewStyle().Width(wrapWidth)
	for _, line := range m.history {
		b.WriteString(wrapper.Render(line))
		b.WriteString("\n")
	}
	return b.String()
}

func (m AppModel) View() string {
	if !m.ready {
		return "\n  Initializing Red Team Protocols..."
	}
	if m.uiState == StateLoading {
		return m.matrixView()
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

func (m AppModel) matrixView() string {
	var b strings.Builder
	chars := []string{"ﾊ", "ﾐ", "ﾋ", "ｰ", "ｳ", "ｼ", "ﾅ", "ﾓ", "ﾆ", "ｻ", "ﾜ", "ﾂ", "ｵ", "ﾘ", "ｱ", "ﾎ", "ﾃ", "ﾏ", "ｹ", "ﾒ", "ｴ", "ｶ", "ｷ", "ﾑ", "ﾕ", "ﾗ", "ｾ", "ﾈ", "ｽ", "ﾀ", "ﾇ", "ﾍ", "0", "1", "2", "3", "4", "5", "7", "8", "9", ":", ".", "=", "*", "+", "-", "<", ">"}
	styleHead := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier)).Bold(true)
	styleTail := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier)).Faint(true)
	styleDim := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorSubtle))

	for y := 0; y < m.height; y++ {
		for x := 0; x < m.width; x++ {
			if x >= len(m.matrixDrops) {
				b.WriteString(" ")
				continue
			}
			dropHead := m.matrixDrops[x]
			if y == dropHead {
				char := chars[rand.Intn(len(chars))]
				b.WriteString(styleHead.Render(char))
			} else if y < dropHead && y > dropHead-6 {
				char := chars[rand.Intn(len(chars))]
				if y > dropHead-3 {
					b.WriteString(styleTail.Render(char))
				} else {
					b.WriteString(styleDim.Render(char))
				}
			} else {
				b.WriteString(" ")
			}
		}
		if y < m.height-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m AppModel) headerView() string {
	// Original Text
	baseText := " HELIX // RED TEAM // Nahasat Nibir ^;;^"

	// APPLY GLITCH EFFECT
	// This will occasionally return corrupt strings like " HΞLIX // RΣD TΞAM ..."
	glitchedText := Glitch(baseText, m.glitchProb)

	title := m.styles.Header.Render(fmt.Sprintf("%s %s", m.spinner.View(), glitchedText))
	lineWidth := max(0, m.width-lipgloss.Width(title))
	line := m.styles.Status.Render(strings.Repeat("━", lineWidth))
	return lipgloss.JoinHorizontal(lipgloss.Center, title, line)
}

// Sub-view: The Welcome Banner
func (m AppModel) welcomeView() string {
	magenta := m.styles.SystemName
	white := m.styles.ModalText

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

	// 1. Glitch the Version
	vText := fmt.Sprintf("Helix v%s - AI-Powered CLI Assistant", config.HelixVersion)
	versionLine := Glitch(vText, m.glitchProb)

	// 2. Glitch the Author
	aText := "Creator - Nahasat Nibir"
	authorLine := Glitch(aText, m.glitchProb)

	// 3. Glitch the Manifesto (Always keep a tiny bit of corruption 0.01)
	prob := m.glitchProb
	if prob < 0.01 {
		prob = 0.01
	}
	qText := "- We scream truth through broken amps while empires rot in silence -"
	quote := Glitch(qText, prob)

	bannerContent := lipgloss.JoinVertical(
		lipgloss.Center,
		magenta.Render(art),
		"",
		white.Render(versionLine),
		magenta.Render(authorLine),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(ColorTertiary)).Italic(true).Render(quote),
		"",
		m.styles.Status.Render(strings.Repeat("─", 50)),
		"",
	)
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center, bannerContent)
}

func (m AppModel) inputView() string {
	return m.styles.Input.Width(m.width - 2).Render(m.textInput.View())
}

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
