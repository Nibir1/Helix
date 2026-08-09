//go:build darwin

// internal/confinement/confine_darwin.go
// Purpose: Seatbelt (sandbox-exec) backend. Apple deprecated sandbox-exec but
// it still ships and enforces file-write policy in the kernel; we treat it as
// a best-effort backend and document that status. Profiles are written per
// session with symlink-resolved paths and -D parameters.
// Dependencies: stdlib only.
package confinement

import (
	"fmt"
	"os"
	"path/filepath"
)

func detectBackend() Backend {
	if lookPath("sandbox-exec") {
		return BackendSeatbelt
	}
	return BackendNone
}

// wrapCommand prefixes argv with sandbox-exec and the generated profile.
//
// Args: argv: command argv; p: resolved profile.
// Returns: confined argv + ok. Complexity: O(profile write).
func wrapCommand(argv []string, p Profile) ([]string, bool) {
	if Detect() != BackendSeatbelt {
		return nil, false
	}
	profilePath := filepath.Join(os.TempDir(), fmt.Sprintf("helix-seatbelt-%d.sb", os.Getpid()))
	if err := os.WriteFile(profilePath, []byte(BuildSeatbeltProfile(p)), 0o600); err != nil {
		return nil, false
	}
	args := []string{"sandbox-exec", "-f", profilePath, "-D", "HELIX_ROOT=" + p.Root}
	for i, e := range p.ExtraRW {
		args = append(args, "-D", fmt.Sprintf("HELIX_EXTRA_%d=%s", i, e))
	}
	return append(args, argv...), true
}
