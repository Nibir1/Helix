package tui

import (
	"fmt"
	"strings"

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

		if !m.ready {
			// Initialize viewport logic on first draw
			m.viewport = viewport.New(msg.Width, msg.Height-verticalMarginHeight)
			m.viewport.YPosition = headerHeight
			m.viewport.SetContent("⚡ INITIALIZING HELIX RED TEAM PROTOCOL...")
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - verticalMarginHeight
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
				// Add to history (Placeholder behavior)
				m.history = append(m.history, fmt.Sprintf("%s %s", m.styles.SystemName.Render("USER >"), input))
				m.viewport.SetContent(strings.Join(m.history, "\n"))
				m.textInput.Reset()
				m.viewport.GotoBottom()
			}
		}
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

	// 3. Render Footer (Spinner + Input)
	footer := m.inputView()

	// 4. Join vertically
	return fmt.Sprintf("%s\n%s\n%s", header, content, footer)
}

// Sub-view: Header
func (m AppModel) headerView() string {
	title := m.styles.Header.Render(" HELIX // RED TEAM // Nahasat Nibir ")
	line := m.styles.Status.Render(strings.Repeat("─", max(0, m.width-lipgloss.Width(title))))
	return lipgloss.JoinHorizontal(lipgloss.Center, title, line)
}

// Sub-view: Input Area
func (m AppModel) inputView() string {
	return m.styles.Input.Width(m.width - 4).Render(m.textInput.View())
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Entry Point
func Start() error {
	p := tea.NewProgram(NewModel(), tea.WithAltScreen()) // WithAltScreen uses full terminal buffer
	_, err := p.Run()
	return err
}
