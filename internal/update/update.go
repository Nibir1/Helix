// internal/update/update.go
//
// Purpose: Helix updating itself — finding a newer build, proving it is what it
// claims to be, and putting it in place.
//
// This package downloads a file from the internet and makes it the program the
// user runs. That is the highest-consequence thing in the codebase, so the
// controls are structural rather than advisory and each one is written down
// where it is enforced:
//
//   - **HTTPS to a pinned host.** Only api.github.com and GitHub's own release
//     download hosts. A redirect off them is refused rather than followed
//     (github.go).
//   - **The checksum is mandatory, never best-effort.** A release with no
//     checksums asset, or an asset missing from it, is not installable. An
//     updater that falls back to "no checksum available, continuing" has no
//     integrity control at all — it has a comment (download.go).
//   - **The payload must be a Helix binary for THIS platform**, proved by
//     reading its Go build info rather than by trusting the filename
//     (inspect.go).
//   - **Installation is atomic and reversible.** Rename over the target, keep
//     the previous binary, and roll back automatically if the replacement
//     cannot start (install.go, and the supervisor in cmd/helix).
//
// What it deliberately does NOT do: verify the Sigstore signatures the release
// pipeline produces. Keyless verification needs the right identity and issuer
// constraints, and a check that runs with the wrong ones reports "verified"
// while proving nothing — which is worse than an honest checksum, because it
// buys confidence it has not earned. `Advisory` carries the exact command to
// verify a release by hand, and the UI prints it.
package update

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"
)

// Source says where a candidate came from.
type Source string

const (
	// SourceGitHub is a published release.
	SourceGitHub Source = "github"

	// SourceLocal is a binary you built yourself.
	SourceLocal Source = "local"
)

// Channel is the configured update policy.
const (
	// ChannelGitHub checks published releases only.
	ChannelGitHub = "github"

	// ChannelLocal checks local builds only — for someone developing Helix,
	// where the newest binary is the one they just compiled.
	ChannelLocal = "local"

	// ChannelAuto checks both and prefers whichever is newer, with a local
	// build winning a tie. Someone who has both a checkout and a release
	// installed is developing; the thing they just built is the thing they
	// meant to run.
	ChannelAuto = "auto"

	// ChannelOff disables update checks entirely.
	ChannelOff = "off"
)

// DefaultRepo is the project this build updates from.
const DefaultRepo = "Nibir1/Helix"

// Options configures a check.
type Options struct {
	// Current is the running version.
	Current Version

	// Channel is one of the Channel* constants; empty means ChannelAuto.
	Channel string

	// Repo is "owner/name"; empty means DefaultRepo.
	Repo string

	// LocalPaths are candidate locations for a locally built binary. Empty
	// means the conventional ones (see LocalCandidatePaths).
	LocalPaths []string

	// SelfPath is the running binary, excluded from the local search — a
	// binary cannot be an update to itself.
	SelfPath string
}

// Candidate is a newer Helix that could be installed.
type Candidate struct {
	Source  Source
	Version Version

	// Tag is the release tag, for the GitHub source.
	Tag string

	// Notes is the release title or a one-line description of a local build.
	Notes string

	// URL is the archive to download (GitHub) — empty for a local build.
	URL string

	// SHA256 is the expected checksum of the DOWNLOAD (GitHub). Mandatory for
	// a remote candidate; empty is a refusal, not a permission.
	SHA256 string

	// Size is the download size in bytes when the release reports one.
	Size int64

	// Path is the file to install, for a local build.
	Path string

	// Published is when the release was made, or the local build's mtime.
	Published time.Time

	// VersionKnown is false for a local build with no stamped version, where
	// the comparison fell back to modification time. The UI must say so: "newer
	// file" and "newer version" are different claims.
	VersionKnown bool
}

// Describe renders a one-line summary for a panel.
func (c Candidate) Describe() string {
	if c.Source == SourceLocal {
		if !c.VersionKnown {
			return "a local build from " + c.Published.Format("2006-01-02 15:04")
		}
		return "local build " + c.Version.String()
	}
	return "release " + c.Version.String()
}

// Advisory is the manual verification instruction shown alongside a remote
// candidate, because this package checks the checksum and not the signature.
func (c Candidate) Advisory(repo string) string {
	if c.Source != SourceGitHub {
		return ""
	}
	if repo == "" {
		repo = DefaultRepo
	}
	return "signatures are published but not checked here — verify by hand with: " +
		"cosign verify-blob --certificate-identity-regexp 'https://github.com/" +
		repo + "/' --certificate-oidc-issuer https://token.actions.githubusercontent.com <asset>"
}

// Check resolves the newest installable Helix, or nil when the running one is
// already it.
//
// Errors are returned rather than swallowed, but a caller on the /reboot path
// treats them as "no update" and restarts anyway: a GitHub outage must never be
// able to stop the shell from restarting.
func Check(ctx context.Context, opts Options) (*Candidate, error) {
	channel := strings.TrimSpace(strings.ToLower(opts.Channel))
	if channel == "" {
		channel = ChannelAuto
	}
	if channel == ChannelOff {
		return nil, nil
	}

	var best *Candidate
	var firstErr error

	consider := func(c *Candidate, err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if c == nil {
			return
		}
		switch {
		case best == nil:
			best = c
		case c.Version.Newer(best.Version):
			best = c
		case c.Source == SourceLocal && !best.Version.Newer(c.Version):
			// A tie goes to the local build: someone with both is developing,
			// and the binary they just compiled is the one they meant to run.
			best = c
		}
	}

	if channel == ChannelGitHub || channel == ChannelAuto {
		consider(checkGitHub(ctx, opts))
	}
	if channel == ChannelLocal || channel == ChannelAuto {
		consider(checkLocal(opts))
	}

	if best == nil {
		return nil, firstErr
	}
	// A candidate that is not actually newer is not a candidate. The per-source
	// checks apply this too; repeating it here means a new source cannot forget.
	if !best.Version.Newer(opts.Current) && best.VersionKnown {
		return nil, firstErr
	}
	return best, firstErr
}

// platformAssetHints returns the fragments a release asset for this machine
// must contain, in goreleaser's naming.
//
// Derived rather than hardcoded per platform, so a new GOARCH does not silently
// match nothing: goreleaser title-cases the OS and rewrites amd64 as x86_64,
// and those two rules are the whole mapping.
func platformAssetHints() (osHint, archHint string) {
	osHint = strings.ToUpper(runtime.GOOS[:1]) + runtime.GOOS[1:]
	switch runtime.GOARCH {
	case "amd64":
		archHint = "x86_64"
	case "386":
		archHint = "i386"
	default:
		archHint = runtime.GOARCH
	}
	return osHint, archHint
}

// errNoAsset is returned when a release exists but publishes nothing for this
// machine — a real and reportable state, not a failure to reach GitHub.
var errNoAsset = fmt.Errorf("no release asset for %s/%s", runtime.GOOS, runtime.GOARCH)
