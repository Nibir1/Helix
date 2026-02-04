package tui

import "github.com/charmbracelet/lipgloss"

// THEME: TRON / CYBERPUNK
// -----------------------
// Primary:   Electric Cyan (#04D9FF) - The Agent / Success
// Secondary: Neon Magenta (#FF0055)  - Warnings / User
// Tertiary:  Solar Orange (#FF9900)  - System / Info
// Void:      Deep Black (#050505)    - Background
// Grid:      Dim Grey (#1A1A1A)      - UI Elements

const (
	ColorPrimary   = "#04D9FF" // Cyan
	ColorSecondary = "#FF0055" // Magenta
	ColorTertiary  = "#FF9900" // Orange
	ColorText      = "#FAFAFA" // White-ish
	ColorSubtle    = "#444444" // Grey
	ColorVoid      = "#050505" // Black
	ColorRectifier = "#FF0000" // Red (For the Red Team theme)
)

type Styles struct {
	// Base
	App    lipgloss.Style
	Header lipgloss.Style
	Status lipgloss.Style

	// Content
	Viewport   lipgloss.Style
	SystemName lipgloss.Style
	AgentName  lipgloss.Style
	Timestamp  lipgloss.Style

	// Input
	Input       lipgloss.Style
	InputPrompt lipgloss.Style

	// Modal
	ModalBorder lipgloss.Style
	ModalText   lipgloss.Style
}

func DefaultStyles() *Styles {
	s := new(Styles)

	// Base Application Style
	s.App = lipgloss.NewStyle().
		Background(lipgloss.Color(ColorVoid)).
		Foreground(lipgloss.Color(ColorText))

	// Header: " HELIX // RED TEAM // Nahasat Nibir ^;;^ "
	s.Header = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorVoid)).
		Background(lipgloss.Color(ColorRectifier)).
		Bold(true).
		Padding(0, 1).
		MarginRight(1)

	// Status Line (The separator)
	s.Status = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorSubtle))

	// Viewport (The Chat Area)
	// We add a subtle left border to define the "feed"
	s.Viewport = lipgloss.NewStyle().
		Padding(0, 1).
		BorderLeft(true).
		BorderStyle(lipgloss.ThickBorder()).
		BorderForeground(lipgloss.Color(ColorSubtle))

	// Entity Names
	s.SystemName = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorRectifier)).
		Bold(true)

	s.AgentName = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorPrimary)).
		Bold(true)

	// Input Area
	s.Input = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorPrimary)).
		Padding(0, 1).
		MarginTop(0)

	s.InputPrompt = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorPrimary)).
		Bold(true)

	// Modal (The Popup)
	s.ModalBorder = lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color(ColorRectifier)).
		Padding(1, 2).
		Align(lipgloss.Center)

	s.ModalText = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorText)).
		Bold(true)

	return s
}
