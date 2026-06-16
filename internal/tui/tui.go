// internal/tui/tui.go

package tui

import (
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"strings"
	"time"

	"helix/internal/agent"
	"helix/internal/audio" // NEW: Import Audio Engine
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

	// ---------------------------------------------------------
	// WINDOW RESIZE
	// ---------------------------------------------------------
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.styles = DefaultStyles()

		headerHeight := lipgloss.Height(m.headerView())
		footerHeight := lipgloss.Height(m.inputView())
		verticalMarginHeight := headerHeight + footerHeight

		inputBoxWidth := m.width - 4
		m.textInput.Width = inputBoxWidth - 4

		// Initialize Matrix Drops if needed
		if m.matrixDrops == nil || len(m.matrixDrops) != m.width {
			m.matrixDrops = make([]int, m.width)
			for i := range m.matrixDrops {
				m.matrixDrops[i] = rand.Intn(m.height) * -1
			}
		}

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-verticalMarginHeight)
			m.viewport.YPosition = headerHeight

			// Initial Banner Log (Info Type)
			m.history = append(m.history, LogEntry{
				Type:      LogTypeInfo,
				Content:   m.welcomeView(),
				Timestamp: time.Now(),
			})

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

	// ---------------------------------------------------------
	// SYSTEM / HUD HEARTBEAT
	// ---------------------------------------------------------
	case SystemTickMsg:
		var mStats runtime.MemStats
		runtime.ReadMemStats(&mStats)
		// Convert bytes to MB
		m.memUsage = fmt.Sprintf("%d MB", mStats.Alloc/1024/1024)
		// Count Goroutines (Neural Threads)
		m.activeProcs = runtime.NumGoroutine()
		// Schedule next update
		return m, tickSystem()

	// ---------------------------------------------------------
	// TYPEWRITER EFFECT
	// ---------------------------------------------------------
	case TypewriterTickMsg:
		if !m.isTyping {
			return m, nil
		}

		// Move one rune from pending to typed
		if len(m.pendingText) > 0 {
			// Convert to rune slice to handle multi-byte chars correctly
			runes := []rune(m.pendingText)
			char := string(runes[0])
			m.typedSoFar += char
			m.pendingText = string(runes[1:])

			// SOUND: Play click for every character (skip spaces to sound natural)
			if char != " " {
				go audio.PlayClick()
			}

			// Update view (renders the "Ghost Line")
			m.viewport.SetContent(m.renderHistory())
			m.viewport.GotoBottom()

			// Keep ticking
			return m, tickTypewriter()
		}

		// Finished Typing!
		m.isTyping = false

		// Commit to history permanently
		m.history = append(m.history, LogEntry{
			Type:      m.typingType,
			Content:   m.typedSoFar,
			Timestamp: m.typingStart,
		})

		// Reset buffers
		m.typedSoFar = ""
		m.pendingText = ""

		m.viewport.SetContent(m.renderHistory())
		m.viewport.GotoBottom()

		// Resume listening for new messages
		return m, waitForAgentOutput(m.agentCh)

	// ---------------------------------------------------------
	// MATRIX ANIMATION LOOP
	// ---------------------------------------------------------
	case MatrixTickMsg:
		if m.uiState == StateLoading {
			// Boot Sequence Duration: 6 Seconds
			if time.Since(m.bootTime) > 6*time.Second {
				m.uiState = StateMain
				// Trigger initial glitch spike on transition
				return m, func() tea.Msg { return GlitchTickMsg{} }
			}
			// Update physics
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
		// Regenerate banner to look corrupted
		if len(m.history) > 0 {
			m.history[0].Content = m.welcomeView()
			m.viewport.SetContent(m.renderHistory())
		}
		return m, resetGlitch()

	case GlitchResetMsg:
		m.glitchProb = 0.0 // RESTORE
		// Regenerate banner to clean state
		if len(m.history) > 0 {
			m.history[0].Content = m.welcomeView()
			m.viewport.SetContent(m.renderHistory())
		}
		return m, scheduleGlitch()

	// ---------------------------------------------------------
	// INTERACTION
	// ---------------------------------------------------------
	case ux.ConfirmationRequest:
		// SOUND: Play Alert Tone
		go audio.PlayAlert()

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
				// Force an immediate frame update and resume listening for agent output
				return m, tea.Batch(
					tea.Tick(time.Millisecond, func(t time.Time) tea.Msg { return nil }),
					waitForAgentOutput(m.agentCh),
				)
			case "n", "N":
				m.confirmReply <- false
				m.uiState = StateMain
				return m, tea.Batch(
					tea.Tick(time.Millisecond, func(t time.Time) tea.Msg { return nil }),
					waitForAgentOutput(m.agentCh),
				)
			}
			return m, nil
		}
		if m.uiState == StateMain {
			if msg.Type == tea.KeyEnter {
				input := m.textInput.Value()
				if input != "" {
					// Add USER log
					m.history = append(m.history, LogEntry{
						Type:      LogTypeUser,
						Content:   input,
						Timestamp: time.Now(),
					})
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

	// ---------------------------------------------------------
	// AGENT MESSAGES (Parsed & Categorized & Streamed)
	// ---------------------------------------------------------
	case AgentMsg:
		content := msg.Content
		logType := LogTypeAgent

		// Heuristic Parsing of Agent Output
		upperContent := strings.ToUpper(content)

		if strings.Contains(upperContent, "[ERROR]") {
			logType = LogTypeError
			// SOUND: Play Error Buzz
			go audio.PlayError()
		} else if strings.Contains(upperContent, "[SUCCESS]") {
			logType = LogTypeSuccess
		} else if strings.Contains(upperContent, "[WARNING]") || strings.Contains(upperContent, "⚠️") {
			logType = LogTypeWarning
		} else if strings.Contains(upperContent, "[DEBUG]") || strings.Contains(upperContent, "PLANNER RAW OUTPUT") {
			logType = LogTypeDebug
		} else if strings.Contains(upperContent, "[INFO]") || strings.Contains(upperContent, "[SYSTEM]") {
			logType = LogTypeInfo
		}

		// LOGIC CHANGE: Stream the text via Typewriter
		// If it's a Debug message or User message, show instantly to avoid UX lag.
		if logType == LogTypeDebug || logType == LogTypeUser {
			m.history = append(m.history, LogEntry{
				Type:      logType,
				Content:   content,
				Timestamp: time.Now(),
			})
			m.viewport.SetContent(m.renderHistory())
			m.viewport.GotoBottom()
			return m, waitForAgentOutput(m.agentCh)
		}

		// Initialize Typing Sequence
		m.isTyping = true
		m.pendingText = content
		m.typedSoFar = ""
		m.typingType = logType
		m.typingStart = time.Now()

		return m, tickTypewriter()

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

// ---------------------------------------------------------
// RENDERERS
// ---------------------------------------------------------

// renderHistory converts structured LogEntry slice into a styled string
func (m AppModel) renderHistory() string {
	var b strings.Builder

	// Safe width calculation
	wrapWidth := m.viewport.Width - 4
	if wrapWidth < 10 {
		wrapWidth = 10
	}
	wrapper := lipgloss.NewStyle().Width(wrapWidth)

	// --- STYLES ---
	// User: White & Bold
	styleUser := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorText)).Bold(true)

	// Agent/Success: Cyan (Primary)
	styleAgent := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorPrimary))
	styleSuccess := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorPrimary)).Bold(true)

	// Error: Red (Rectifier)
	styleError := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier)).Bold(true)

	// Warning: Orange (Tertiary)
	styleWarning := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorTertiary))

	// Info: Magenta (Secondary)
	styleInfo := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorSecondary))

	// Debug: Dark Grey (Subtle) - Good for filtered noise
	styleDebug := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorSubtle)).Faint(true)

	// Timestamp: Dim Grey
	styleTimestamp := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorSubtle)).Faint(true)

	// 1. Render Commited History
	for _, entry := range m.history {
		ts := styleTimestamp.Render(fmt.Sprintf("[%s] ", entry.Timestamp.Format("15:04:05")))

		var content string
		switch entry.Type {
		case LogTypeUser:
			userLabel := m.styles.SystemName.Render("[USER]")
			content = fmt.Sprintf("%s › %s", userLabel, styleUser.Render(entry.Content))
		case LogTypeError:
			content = styleError.Render(entry.Content)
		case LogTypeSuccess:
			content = styleSuccess.Render(entry.Content)
		case LogTypeWarning:
			content = styleWarning.Render(entry.Content)
		case LogTypeDebug:
			content = styleDebug.Render(entry.Content)
		case LogTypeInfo:
			content = styleInfo.Render(entry.Content)
		default:
			content = styleAgent.Render(entry.Content)
		}

		// Handle Banner (no timestamp)
		if entry.Type == LogTypeInfo && strings.Contains(entry.Content, "╔") {
			b.WriteString(wrapper.Render(entry.Content))
		} else {
			// Combine Timestamp + Content before wrapping to ensure alignment
			fullLine := ts + content
			b.WriteString(wrapper.Render(fullLine))
		}
		b.WriteString("\n")
	}

	// 2. Render Active Typing Line (The "Ghost" Line)
	if m.isTyping {
		ts := styleTimestamp.Render(fmt.Sprintf("[%s] ", m.typingStart.Format("15:04:05")))

		var content string
		switch m.typingType {
		case LogTypeError:
			content = styleError.Render(m.typedSoFar)
		case LogTypeSuccess:
			content = styleSuccess.Render(m.typedSoFar)
		case LogTypeWarning:
			content = styleWarning.Render(m.typedSoFar)
		case LogTypeInfo:
			content = styleInfo.Render(m.typedSoFar)
		default:
			// Add a cursor block █ to the end of the streaming text for effect
			cursor := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier)).Render("█")
			content = styleAgent.Render(m.typedSoFar) + cursor
		}

		fullLine := ts + content
		b.WriteString(wrapper.Render(fullLine))
		b.WriteString("\n")
	}

	return b.String()
}

func (m AppModel) headerView() string {
	// LEFT: Title with Glitch
	baseText := " HELIX // RED TEAM // Nahasat Nibir ^;;^"
	glitchedText := Glitch(baseText, m.glitchProb)
	title := m.styles.Header.Render(fmt.Sprintf("%s %s", m.spinner.View(), glitchedText))

	// RIGHT: HUD Stats
	statStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorTertiary)).
		Background(lipgloss.Color(ColorVoid)).
		Padding(0, 1).
		Bold(true)

	// Display Mem Usage and Active Goroutines (Neural Threads)
	stats := fmt.Sprintf(" [ MEM: %s ] [ NEURAL: %d ] ", m.memUsage, m.activeProcs)
	statsBlock := statStyle.Render(stats)

	// CENTER: Flexible Spacer
	totalWidth := lipgloss.Width(title) + lipgloss.Width(statsBlock)
	lineWidth := max(0, m.width-totalWidth)
	line := m.styles.Status.Render(strings.Repeat("━", lineWidth))

	return lipgloss.JoinHorizontal(lipgloss.Center, title, line, statsBlock)
}

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

	// 3. Glitch the Manifesto
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

func (m AppModel) matrixView() string {
	var b strings.Builder
	chars := []string{"ﾊ", "ﾐ", "ﾋ", "ｰ", "ｳ", "ｼ", "ﾅ", "ﾓ", "ﾆ", "ｻ", "ﾜ", "ﾂ", "ｵ", "ﾘ", "ｱ", "ﾎ", "ﾃ", "ﾏ", "ｹ", "ﾒ", "ｴ", "ｶ", "ｷ", "ﾑ", "ﾕ", "ﾗ", "ｾ", "ﾈ", "ｽ", "ﾀ", "ﾇ", "ﾍ", "0", "1", "2", "3", "4", "5", "7", "8", "9", ":", ".", "=", "*", "+", "-", "<", ">"}

	// Red Team Styles
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

func (m AppModel) inputView() string {
	return m.styles.Input.Width(m.width - 2).Render(m.textInput.View())
}

func (m AppModel) overlayView(base string) string {
	msg := m.styles.ModalText.Render(m.confirmMsg)
	dialog := m.styles.ModalBorder.Render(fmt.Sprintf(
		"⚠️  CRITICAL DECISION REQUIRED ⚠️\n\n%s\n\n[Y] EXECUTE    [N] ABORT",
		msg,
	))
	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		dialog,
		lipgloss.WithWhitespaceChars(" "),
	)
}

func (m AppModel) View() string {
	if !m.ready {
		return "\n  Initializing Red Team Protocols..."
	}
	// 1. Matrix Loader
	if m.uiState == StateLoading {
		return m.matrixView()
	}

	// 2. Main Dashboard
	header := m.headerView()
	content := m.styles.Viewport.Render(m.viewport.View())
	footer := m.inputView()

	baseView := lipgloss.JoinVertical(lipgloss.Left, header, content, footer)

	// 3. Modal Overlay
	if m.uiState == StateConfirm {
		return m.overlayView(baseView)
	}

	return baseView
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func Start(ag *agent.Agent, agentCh chan tea.Msg, output io.Writer) error {
	// Initialize Audio Engine
	// We ignore error (e.g. no audio device) so the app remains robust
	_ = audio.Init()

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
