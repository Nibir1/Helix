// internal/update/version.go
//
// Purpose: comparing two Helix versions, which is the whole basis of deciding
// whether an update exists.
//
// Deliberately a small hand-rolled semver rather than a dependency: the only
// versions this ever compares are Helix's own, they are produced by one
// goreleaser config, and a self-updater is the last place to widen the
// dependency surface — a compromised comparison library decides which binary
// you install.
package update

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version.
//
// Pre is the pre-release suffix ("rc.1", "beta"). It is compared only far
// enough to satisfy the one rule that matters here: a pre-release is OLDER than
// the release it precedes, so 1.6.0-rc.1 never looks newer than 1.6.0.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

// String renders the canonical form, without a leading "v".
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// ParseVersion reads a version, tolerating the forms that actually appear.
//
// Tags carry a leading "v", the compiled-in constant does not, and build
// metadata after "+" is not part of precedence. Anything else is rejected
// rather than guessed at: a version this cannot read must not be silently
// treated as 0.0.0, because that would make every unreadable tag look like an
// ancient release and hide a real update forever.
func ParseVersion(s string) (Version, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return Version{}, fmt.Errorf("empty version")
	}
	// Build metadata does not affect precedence (semver §10).
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}

	var pre string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre, s = s[i+1:], s[:i]
	}

	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return Version{}, fmt.Errorf("not a version: %q", s)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("not a version: %q", s)
		}
		nums[i] = n
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2], Pre: pre}, nil
}

// Newer reports whether v is strictly newer than other.
//
// Strictly, on purpose. An update mechanism that treats "same version" as
// "newer" reinstalls the binary you are already running on every restart, and
// one that treats "older" as newer downgrades you — both are worse than doing
// nothing, which is why this returns false for anything but a genuine advance.
func (v Version) Newer(other Version) bool {
	switch {
	case v.Major != other.Major:
		return v.Major > other.Major
	case v.Minor != other.Minor:
		return v.Minor > other.Minor
	case v.Patch != other.Patch:
		return v.Patch > other.Patch
	}
	// Same numbers: a release beats a pre-release of itself, and two
	// pre-releases are ordered lexically, which is right for the "rc.1" /
	// "rc.2" / "beta" forms goreleaser produces and is not asked to do more.
	switch {
	case v.Pre == other.Pre:
		return false
	case v.Pre == "":
		return true // 1.6.0 > 1.6.0-rc.1
	case other.Pre == "":
		return false
	default:
		return v.Pre > other.Pre
	}
}
