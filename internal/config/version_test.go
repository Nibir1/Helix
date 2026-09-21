// internal/config/version_test.go
// Purpose: stop the version constant from claiming a release that does not exist.
//
// It said "1.5.0" for a stretch when no v1.5.0 tag existed — the tag had been
// withdrawn to finish polishing first — so every source build reported itself
// as a release nobody could download, and `/version` lied about which Helix was
// running. Nothing caught it because nothing was comparing the constant to the
// one file that says whether a release has been cut.
package config

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const releaseNotes = "../../docs/RELEASE_NOTES.md"

// The release script's own gate, mirrored: it refuses to tag while an
// "Unreleased" heading is present.
var unreleasedHeading = regexp.MustCompile(`(?mi)^#{1,3}[[:space:]]+unreleased`)

func notesAreUnreleased(t *testing.T) bool {
	t.Helper()
	b, err := os.ReadFile(releaseNotes)
	if err != nil {
		t.Fatalf("read %s: %v", releaseNotes, err)
	}
	return unreleasedHeading.Match(b)
}

// While the notes say "Unreleased", the constant must carry a pre-release
// suffix. internal/update orders 1.5.0-dev below 1.5.0, so a machine on a
// source build is correctly offered the release on the day it is published —
// where a bare "1.5.0" would tell it that it already has one.
func TestVersionIsPreReleaseWhileNotesAreUnreleased(t *testing.T) {
	if !notesAreUnreleased(t) {
		t.Skip("release notes carry no Unreleased heading; a plain version is correct")
	}
	if !strings.Contains(HelixVersion, "-") {
		t.Errorf("HelixVersion is %q, but %s still has an Unreleased heading.\n"+
			"A source build would report itself as a published release that does not exist.\n"+
			"Either give it a pre-release suffix (e.g. %q) or rename the heading and cut the release.",
			HelixVersion, releaseNotes, HelixVersion+"-dev")
	}
}

// And the other direction: once the heading is gone, a -dev suffix would tag
// the release itself as a pre-release (scripts/release.sh derives the tag from
// this constant), which is not what cutting a release means.
func TestVersionIsNotPreReleaseOnceNotesAreCut(t *testing.T) {
	if notesAreUnreleased(t) {
		t.Skip("still unreleased")
	}
	if strings.Contains(HelixVersion, "-") {
		t.Errorf("HelixVersion is %q but %s no longer has an Unreleased heading; "+
			"scripts/release.sh would tag v%s, publishing the release as a pre-release",
			HelixVersion, releaseNotes, HelixVersion)
	}
}

// The constant must be readable by the updater at all. One that does not parse
// is worse than a wrong one: internal/update rejects it rather than guessing,
// so /reboot stops being able to tell whether an update exists.
func TestVersionIsParseableSemver(t *testing.T) {
	// Kept local rather than importing internal/update, which imports config.
	semver := regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
	if !semver.MatchString(HelixVersion) {
		t.Errorf("HelixVersion = %q is not a version scripts/release.sh or "+
			"internal/update can read", HelixVersion)
	}
}
