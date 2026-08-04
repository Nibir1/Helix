// internal/tui/terminal_input.go

package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// keyToTerminalBytes translates Bubble Tea key events to terminal byte sequences.
func keyToTerminalBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyBackspace:
		return []byte{127}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyEsc:
		return []byte{27}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyCtrlA:
		return []byte{1}
	case tea.KeyCtrlB:
		return []byte{2}
	case tea.KeyCtrlC:
		return []byte{3}
	case tea.KeyCtrlD:
		return []byte{4}
	case tea.KeyCtrlE:
		return []byte{5}
	case tea.KeyCtrlF:
		return []byte{6}
	case tea.KeyCtrlK:
		return []byte{11}
	case tea.KeyCtrlL:
		return []byte{12}
	case tea.KeyCtrlN:
		return []byte{14}
	case tea.KeyCtrlP:
		return []byte{16}
	case tea.KeyCtrlU:
		return []byte{21}
	case tea.KeyCtrlW:
		return []byte{23}
	case tea.KeyCtrlZ:
		return []byte{26}
	}

	if msg.Type == tea.KeyRunes {
		return []byte(string(msg.Runes))
	}

	return nil
}

// mouseToTerminalBytes translates Bubble Tea mouse events to SGR mouse tracking sequences.
func mouseToTerminalBytes(msg tea.MouseMsg) []byte {
	var cb int
	switch msg.Button {
	case tea.MouseButtonLeft:
		cb = 0
	case tea.MouseButtonMiddle:
		cb = 1
	case tea.MouseButtonRight:
		cb = 2
	case tea.MouseButtonWheelUp:
		cb = 64
	case tea.MouseButtonWheelDown:
		cb = 65
	default:
		cb = 3 // Release/Unknown
	}

	if msg.Shift {
		cb += 4
	}
	if msg.Alt {
		cb += 8
	}
	if msg.Ctrl {
		cb += 16
	}

	x := msg.X + 1
	y := msg.Y + 1

	var action byte
	if msg.Action == tea.MouseActionPress {
		action = 'M'
	} else if msg.Action == tea.MouseActionRelease {
		action = 'm'
	} else {
		action = 'M'
	}

	return []byte(fmt.Sprintf("\x1b[<%d;%d;%d%c", cb, x, y, action))
}
