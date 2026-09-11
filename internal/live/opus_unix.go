//go:build !windows

// internal/live/opus_unix.go
// Purpose: finding libopus on the platforms purego can dlopen.
package live

import "github.com/ebitengine/purego"

// opusLibNames are tried in order, most portable first.
//
// The bare sonames come first so a correctly configured loader path wins; the
// Homebrew and /usr/local paths follow because macOS's dynamic loader does not
// search them, and "installed it with brew and it still says not found" would
// otherwise be the common report.
var opusLibNames = []string{
	"libopus.so.0",
	"libopus.so",
	"libopus.dylib",
	"libopus.0.dylib",
	"/opt/homebrew/lib/libopus.dylib",
	"/usr/local/lib/libopus.dylib",
	"/usr/lib/libopus.so.0",
}

// opusInstallHint names the package to install.
const opusInstallHint = "brew install opus, or apt install libopus0"

func openOpusLibrary(name string) (uintptr, error) {
	return purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}
