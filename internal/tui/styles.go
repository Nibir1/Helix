package tui

import (
	"math/rand"

	"github.com/charmbracelet/lipgloss"
)

// THEME: HELIX / RED TEAM
// -----------------------
// Primary:   Electric Cyan (#04D9FF)
// Secondary: Neon Magenta (#FF0055)
// Rectifier: Aggressive Red (#FF0000) - MAIN THEME
// Void:      Deep Black (#050505)
// Grid:      Dim Grey (#1A1A1A)

const (
	ColorPrimary   = "#04D9FF"
	ColorSecondary = "#FF0055"
	ColorTertiary  = "#FF9900"
	ColorText      = "#FAFAFA"
	ColorSubtle    = "#444444"
	ColorVoid      = "#050505"
	ColorRectifier = "#FF0000" // Red Team Primary
)

type Styles struct {
	App         lipgloss.Style
	Header      lipgloss.Style
	Status      lipgloss.Style
	Viewport    lipgloss.Style
	SystemName  lipgloss.Style
	AgentName   lipgloss.Style
	Timestamp   lipgloss.Style
	Input       lipgloss.Style
	InputPrompt lipgloss.Style
	ModalBorder lipgloss.Style
	ModalText   lipgloss.Style
}

func DefaultStyles() *Styles {
	s := new(Styles)

	s.App = lipgloss.NewStyle().
		Background(lipgloss.Color(ColorVoid)).
		Foreground(lipgloss.Color(ColorText))

	// Header: Red Background
	s.Header = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorVoid)).
		Background(lipgloss.Color(ColorRectifier)).
		Bold(true).
		Padding(0, 1).
		MarginRight(1)

	s.Status = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorSubtle))

	s.Viewport = lipgloss.NewStyle().
		Padding(0, 1).
		BorderLeft(true).
		BorderStyle(lipgloss.ThickBorder()).
		BorderForeground(lipgloss.Color(ColorSubtle))

	s.SystemName = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorSecondary)).Bold(true)
	s.AgentName = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier)).Bold(true)

	s.Input = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ColorRectifier)).
		Padding(0, 1).
		MarginTop(0)

	s.InputPrompt = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRectifier)).Bold(true)

	s.ModalBorder = lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color(ColorRectifier)).
		Padding(1, 2).
		Align(lipgloss.Center)

	s.ModalText = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorText)).Bold(true)

	return s
}

// Glitch randomly replaces characters in a string with sci-fi symbols
// prob is the probability (0.0 to 1.0) that a character gets swapped.
func Glitch(text string, prob float64) string {
	if prob <= 0 {
		return text
	}

	// Sci-Fi / Math Symbols
	glitchChars := []rune("ΞΣΛΩΓΔΦΨΠθ?@#%&")
	runes := []rune(text)

	for i := range runes {
		// Don't glitch spaces, keeping the shape of words roughly intact
		if runes[i] == ' ' {
			continue
		}
		if rand.Float64() < prob {
			runes[i] = glitchChars[rand.Intn(len(glitchChars))]
		}
	}
	return string(runes)
}
