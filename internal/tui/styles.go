package tui

import "github.com/charmbracelet/lipgloss"

// THEME PALETTE: TRON LEGACY (RED TEAM / CLU)
const (
	ColorVoid      = "#080808" // Deepest black background
	ColorGrid      = "#1a1a1a" // Subtle grid background
	ColorRectifier = "#FF1E1E" // The primary "Bad Guy" red
	ColorText      = "#E0E0E0" // Standard text (off-white)
	ColorSubtle    = "#444444" // Dimmed text
	ColorGlow      = "#550000" // Faint red background for active elements
)

type Styles struct {
	App        lipgloss.Style
	Header     lipgloss.Style
	Border     lipgloss.Style
	Input      lipgloss.Style
	Viewport   lipgloss.Style
	Status     lipgloss.Style
	AgentName  lipgloss.Style
	SystemName lipgloss.Style
}

func DefaultStyles() *Styles {
	s := new(Styles)

	// Base Application - Forces a black background
	s.App = lipgloss.NewStyle().
		Background(lipgloss.Color(ColorVoid)).
		Foreground(lipgloss.Color(ColorText))

	// The Glowing Red Border used on active panels
	s.Border = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorRectifier)).
		Padding(0, 1)

	// Header Bar (Top)
	s.Header = lipgloss.NewStyle().
		Background(lipgloss.Color(ColorRectifier)).
		Foreground(lipgloss.Color(ColorVoid)).
		Bold(true).
		Padding(0, 1).
		MarginBottom(1)

	// Viewport (The central chat log)
	s.Viewport = lipgloss.NewStyle().
		Background(lipgloss.Color(ColorVoid)).
		Padding(0, 1) // Give text some breathing room

	// Text Input (Bottom) - The "Prompt"
	s.Input = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorRectifier)). // Red border
		Foreground(lipgloss.Color(ColorRectifier)).       // Red text
		Padding(0, 1)

	// Status Bar elements
	s.Status = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorSubtle)).
		MarginTop(1)

	// AI/System prefixes
	s.AgentName = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier)).Bold(true)
	s.SystemName = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FFFF")).Bold(true) // Cyan for System interrupts

	return s
}
