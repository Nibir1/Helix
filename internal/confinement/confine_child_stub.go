//go:build !linux

// internal/confinement/confine_child_stub.go
// Purpose: Re-exec child entrypoint exists only on Linux (Landlock). Other
// platforms never generate --confined-child argv; this stub keeps main.go
// compiling everywhere.
package confinement

import (
	"fmt"
	"os"
)

// RunConfinedChild is unreachable on non-Linux platforms.
func RunConfinedChild(args []string) int {
	fmt.Fprintln(os.Stderr, "helix: --confined-child is only supported on Linux")
	return 97
}
