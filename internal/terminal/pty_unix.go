// internal/terminal/pty_unix.go

//go:build !windows

package terminal

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// UnixPTY wraps a Unix pseudo-terminal.
type UnixPTY struct {
	file *os.File
	cmd  *exec.Cmd
}

// StartPTY spawns a shell inside a Unix PTY.
func StartPTY(shell string, env []string) (PTY, error) {
	cmd := exec.Command(shell)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	return &UnixPTY{file: ptmx, cmd: cmd}, nil
}

func (p *UnixPTY) Read(b []byte) (int, error)  { return p.file.Read(b) }
func (p *UnixPTY) Write(b []byte) (int, error) { return p.file.Write(b) }
func (p *UnixPTY) Name() string                { return p.file.Name() }

func (p *UnixPTY) Resize(rows, cols uint16) error {
	return pty.Setsize(p.file, &pty.Winsize{Rows: rows, Cols: cols})
}

func (p *UnixPTY) Close() error {
	_ = p.file.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return p.cmd.Wait()
}
