// internal/update/inspect.go
//
// Purpose: proving a file is a Helix binary for THIS machine, without running
// it.
//
// The obvious way to ask a binary its version is to execute it with --version.
// An updater must not: the whole question at that moment is whether the file
// can be trusted, and running it to find out answers it in the worst possible
// order. Go binaries carry their build metadata in a readable section, so the
// module path, the toolchain, and the ldflags a release stamps its version with
// can all be read from the file as data.
package update

import (
	"debug/buildinfo"
	"fmt"
	"strings"
)

// moduleName is the Go module every Helix binary is built from.
const moduleName = "helix"

// BinaryInfo is what a candidate file says about itself.
type BinaryInfo struct {
	// Version is the stamped release version, empty for an unstamped build.
	Version string

	// GOOS and GOARCH are the platform it was built for.
	GOOS, GOARCH string
}

// Inspect reads a binary's Go build info.
//
// Returns an error when the file is not a Go binary at all, which is the check
// that matters: a truncated download, an HTML error page saved as an archive,
// or a file that is simply something else all fail here rather than being
// renamed over the shell the user runs.
func Inspect(path string) (BinaryInfo, error) {
	bi, err := buildinfo.ReadFile(path)
	if err != nil {
		return BinaryInfo{}, fmt.Errorf("not a Go binary: %w", err)
	}
	if bi.Main.Path != "" && bi.Main.Path != moduleName {
		return BinaryInfo{}, fmt.Errorf(
			"built from module %q, not %q — refusing to install it as Helix",
			bi.Main.Path, moduleName)
	}

	info := BinaryInfo{}
	for _, s := range bi.Settings {
		switch s.Key {
		case "GOOS":
			info.GOOS = s.Value
		case "GOARCH":
			info.GOARCH = s.Value
		case "-ldflags":
			info.Version = versionFromLdflags(s.Value)
		}
	}
	return info, nil
}

// versionFromLdflags digs the stamped version out of the recorded link flags.
//
// The release pipeline sets `-X helix/internal/config.HelixVersion={{.Version}}`,
// and Go records the whole flag string verbatim in the build info. A local
// `go build` sets no ldflags at all, which is why an empty result here is a
// normal answer rather than a failure — see checkLocal, which falls back to the
// file's modification time and says so.
func versionFromLdflags(flags string) string {
	const key = "helix/internal/config.HelixVersion="
	i := strings.Index(flags, key)
	if i < 0 {
		return ""
	}
	rest := flags[i+len(key):]
	// The value ends at the next separator; quotes appear when the flag string
	// was assembled with them.
	end := strings.IndexAny(rest, " '\"\t\n")
	if end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// RequirePlatform rejects a binary built for a different machine.
//
// Worth its own check because the failure it prevents is silent on some
// systems and baffling on all of them: installing a linux/amd64 build over a
// darwin/arm64 Helix produces a shell that will not start, with an error from
// the kernel rather than from Helix.
func (b BinaryInfo) RequirePlatform(goos, goarch string) error {
	if b.GOOS != "" && b.GOOS != goos {
		return fmt.Errorf("built for %s, this machine is %s", b.GOOS, goos)
	}
	if b.GOARCH != "" && b.GOARCH != goarch {
		return fmt.Errorf("built for %s/%s, this machine is %s/%s",
			b.GOOS, b.GOARCH, goos, goarch)
	}
	return nil
}
