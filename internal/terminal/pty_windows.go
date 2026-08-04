// internal/terminal/pty_windows.go
//go:build windows
// +build windows

package terminal

import (
	"github.com/UserExistsError/conpty"
)

// WindowsPTY wraps a Windows ConPTY pseudo-terminal.
type WindowsPTY struct {
	cpty *conpty.ConPty
}

// StartPTY spawns a shell inside a Windows ConPTY.
func StartPTY(shell string, env []string) (PTY, error) {
	if shell == "" {
		shell = "powershell.exe"
	}
	// conpty.Start inherits the current process environment by default.
	cpty, err := conpty.Start(shell)
	if err != nil {
		return nil, err
	}
	return &WindowsPTY{cpty: cpty}, nil
}

func (p *WindowsPTY) Read(b []byte) (int, error)  { return p.cpty.Read(b) }
func (p *WindowsPTY) Write(b []byte) (int, error) { return p.cpty.Write(b) }
func (p *WindowsPTY) Name() string                { return "conpty" }

// Resize updates the ConPTY dimensions. Note: conpty.Resize takes (cols, rows).
func (p *WindowsPTY) Resize(rows, cols uint16) error {
	return p.cpty.Resize(int(cols), int(rows))
}

func (p *WindowsPTY) Close() error {
	if p.cpty != nil {
		return p.cpty.Close()
	}
	return nil
}
