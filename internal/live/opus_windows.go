// internal/live/opus_windows.go
// Purpose: the same libopus lookup on Windows, where purego has no Dlopen.
//
// purego's RegisterLibFunc is cross-platform; only the loading is not, and on
// Windows the handle comes from LoadLibrary instead. golang.org/x/sys is
// already a direct dependency, so this costs nothing new.
//
// HONESTLY LABELLED: this path is typechecked by `GOOS=windows go vet` and has
// NOT been run on a Windows machine — there is not one here. What is verified
// is that it compiles and that the failure when opus.dll is missing is the same
// named ErrNoOpus every other platform reports, which is the case an untested
// path is most likely to get wrong.
package live

import "golang.org/x/sys/windows"

// opusLibNames are the DLL names an opus build ships under. libopus-0.dll is
// the name the official MSYS2/mingw builds use; opus.dll covers a hand-built or
// vcpkg copy dropped next to the binary.
var opusLibNames = []string{
	"libopus-0.dll",
	"libopus.dll",
	"opus.dll",
}

// opusInstallHint names where to get it.
const opusInstallHint = "install libopus (pacman -S mingw-w64-x86_64-opus) or put opus.dll beside helix.exe"

func openOpusLibrary(name string) (uintptr, error) {
	h, err := windows.LoadLibrary(name)
	if err != nil {
		return 0, err
	}
	return uintptr(h), nil
}
