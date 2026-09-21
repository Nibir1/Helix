// cmd/helix/duplex_stderr.go
// Purpose: the one indirection duplex.go needs to be testable without
// redirecting the process's stderr.
package main

import (
	"io"
	"os"
)

func defaultStderr() io.Writer { return os.Stderr }
