// internal/update/local.go
//
// Purpose: adopting a Helix you built yourself.
//
// This is the channel for whoever is working on Helix. `make current` writes
// `dist/helix`, and until now the way to run it was to quit the shell and start
// the new binary by hand. The local channel closes that loop: `/reboot` finds
// the build, checks it really is Helix for this machine, and restarts into it.
package update

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// LocalCandidatePaths returns the conventional places a local build lands.
//
// Relative to the working directory, because that is where someone who just ran
// `make current` is standing. `scripts/build.sh` writes `dist/helix` (and
// `dist/helix.exe` on Windows); a plain `go build` in the repo root writes
// `./helix`.
func LocalCandidatePaths() []string {
	name := "helix"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return []string{
		filepath.Join("dist", name),
		name,
		filepath.Join("bin", name),
	}
}

// checkLocal finds the newest usable local build.
//
// "Newest" has two meanings here and the difference is reported rather than
// hidden. A build stamped with a version — a goreleaser artifact someone
// downloaded and left in dist/ — is compared by version. An ordinary
// `go build`, which stamps nothing, can only be compared by modification time,
// and a candidate found that way sets VersionKnown=false so the UI can say
// "newer file" instead of claiming a version it does not know.
func checkLocal(opts Options) (*Candidate, error) {
	paths := opts.LocalPaths
	if len(paths) == 0 {
		paths = LocalCandidatePaths()
	}

	selfPath, selfInfo := resolveSelf(opts.SelfPath)

	var best *Candidate
	var firstErr error
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if selfPath != "" && sameFile(abs, selfPath) {
			// A binary cannot be an update to itself. This is the ordinary case
			// when Helix is run straight out of dist/.
			continue
		}
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() || st.Size() == 0 {
			continue
		}

		info, err := Inspect(abs)
		if err != nil {
			// A file called "helix" that is not a Helix binary is worth
			// reporting once, not worth failing the whole check over.
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", p, err)
			}
			continue
		}
		if err := info.RequirePlatform(runtime.GOOS, runtime.GOARCH); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", p, err)
			}
			continue
		}

		c := &Candidate{
			Source:    SourceLocal,
			Path:      abs,
			Notes:     "built locally at " + abs,
			Published: st.ModTime(),
			Size:      st.Size(),
		}
		if v, verr := ParseVersion(info.Version); verr == nil {
			c.Version, c.VersionKnown = v, true
			if !v.Newer(opts.Current) {
				continue
			}
		} else {
			// No stamped version: fall back to "is this file newer than the one
			// I am running". Requires knowing our own mtime; without it there
			// is no honest comparison to make, so the candidate is skipped
			// rather than assumed to be an upgrade.
			if selfInfo == nil || !st.ModTime().After(selfInfo.ModTime()) {
				continue
			}
			c.Version = opts.Current
			c.VersionKnown = false
		}

		if best == nil || c.Published.After(best.Published) {
			best = c
		}
	}
	return best, firstErr
}

// resolveSelf returns the absolute path and stat of the running binary.
func resolveSelf(hint string) (string, os.FileInfo) {
	path := hint
	if path == "" {
		if exe, err := os.Executable(); err == nil {
			path = exe
		}
	}
	if path == "" {
		return "", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	st, err := os.Stat(abs)
	if err != nil {
		return abs, nil
	}
	return abs, st
}

// sameFile reports whether two paths name the same file, following symlinks.
//
// os.SameFile on the stat results rather than a string comparison: an installed
// Helix is commonly a symlink into dist/, and comparing paths would miss that
// and offer the running binary as an update to itself.
func sameFile(a, b string) bool {
	if a == b {
		return true
	}
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	if ra == rb {
		return true
	}
	sa, err := os.Stat(ra)
	if err != nil {
		return false
	}
	sb, err := os.Stat(rb)
	if err != nil {
		return false
	}
	return os.SameFile(sa, sb)
}
