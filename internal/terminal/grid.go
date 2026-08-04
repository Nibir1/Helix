// internal/terminal/grid.go
package terminal

import "github.com/ActiveState/vt10x"

// Grid manages the terminal cell state and ANSI parsing using vt10x.
type Grid struct {
	state *vt10x.State
	term  *vt10x.VT
	rows  int
	cols  int
}

// NewGrid creates a new terminal grid.
func NewGrid(rows, cols int) *Grid {
	state := &vt10x.State{}
	// We pass nil for in/out because we will manually feed data via term.Write()
	term, _ := vt10x.New(state, nil, nil)

	// vt10x.Resize takes (cols, rows)
	term.Resize(cols, rows)

	return &Grid{
		state: state,
		term:  term,
		rows:  rows,
		cols:  cols,
	}
}

// Process feeds raw PTY output into the VT100 parser.
func (g *Grid) Process(data []byte) {
	// Write parses input and updates the state
	_, _ = g.term.Write(data)
}

// Resize updates the grid dimensions.
func (g *Grid) Resize(rows, cols int) {
	g.rows = rows
	g.cols = cols
	// vt10x.Resize takes (cols, rows)
	g.term.Resize(cols, rows)
}

// Render returns the grid as a plain text string (ANSI codes are parsed into visual layout).
func (g *Grid) Render() string {
	g.state.Lock()
	defer g.state.Unlock()
	return g.state.String()
}

// CursorPos returns the current cursor position (row, col).
func (g *Grid) CursorPos() (int, int) {
	g.state.Lock()
	defer g.state.Unlock()
	return g.state.Cursor()
}
