// internal/terminal/pty.go
package terminal

import "io"

// PTY defines the contract for a pseudo-terminal across platforms.
type PTY interface {
	io.ReadWriteCloser
	Resize(rows, cols uint16) error
	Name() string
}
