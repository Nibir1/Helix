// internal/terminal/session.go
package terminal

import (
	"os"
	"runtime"
)

// Session represents a single interactive terminal session.
type Session struct {
	pty  PTY
	grid *Grid
}

// NewSession starts a new shell session with the given shell.
func NewSession(shell string, rows, cols int) (*Session, error) {
	if shell == "" {
		shell = os.Getenv("SHELL")
		if shell == "" {
			if runtime.GOOS == "windows" {
				shell = "powershell.exe"
			} else {
				shell = "/bin/sh"
			}
		}
	}

	env := os.Environ()
	p, err := StartPTY(shell, env)
	if err != nil {
		return nil, err
	}

	return &Session{
		pty:  p,
		grid: NewGrid(rows, cols),
	}, nil
}

func (s *Session) Read(b []byte) (int, error)  { return s.pty.Read(b) }
func (s *Session) Write(b []byte) (int, error) { return s.pty.Write(b) }
func (s *Session) Close() error                { return s.pty.Close() }

// Process updates the visual grid with new PTY output.
func (s *Session) Process(data []byte) {
	s.grid.Process(data)
}

// Resize updates both the PTY and the visual grid.
func (s *Session) Resize(rows, cols uint16) error {
	s.grid.Resize(int(rows), int(cols))
	return s.pty.Resize(rows, cols)
}

// Grid returns the underlying visual grid.
func (s *Session) Grid() *Grid {
	return s.grid
}
