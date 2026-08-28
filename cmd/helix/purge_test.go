// cmd/helix/purge_test.go
// Purpose: /purge's second prompt has to delete what it says it deletes, and
// the manifest has to name what a purge actually costs.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bug this pins: the weights prompt offered ~/.helix/models alone, while
// every download Helix makes through its own wizard lands in whisper-models,
// piper-voices or piper. A user who set the machine up entirely through
// /blackbox setup and answered YES deleted nothing at all.
func TestPurgeWeightTargetsCoverEveryDirectoryHelixDownloadsInto(t *testing.T) {
	helixDir := filepath.Join(t.TempDir(), ".helix")
	got := map[string]bool{}
	for _, tg := range purgeWeightTargets(helixDir) {
		got[filepath.Base(tg.path)] = true
		if strings.TrimSpace(tg.desc) == "" {
			t.Errorf("%s has no description; the prompt asks about it by name", tg.path)
		}
	}
	// Every directory the wizard creates under ~/.helix and fills with something
	// large. This list has to grow WITH the wizard: it is a copy of a fact that
	// lives elsewhere, so a new download reaching gigabytes while this stayed at
	// four entries is exactly how /purge came to delete nothing the first time.
	for _, want := range []string{
		"models", "whisper-models", "piper-voices", "piper", "csm.rs",
	} {
		if !got[want] {
			t.Errorf("~/.helix/%s is downloaded by Helix but not offered for deletion", want)
		}
	}
}

// config.DefaultConfig honours HELIX_MODEL_DIR, so purging the default path on a
// machine that keeps its GGUFs elsewhere would silently miss the largest
// directory on the disk while reporting the weights as deleted.
func TestPurgeHonoursTheModelDirOverride(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("HELIX_MODEL_DIR", custom)

	var found bool
	for _, tg := range purgeWeightTargets(filepath.Join(t.TempDir(), ".helix")) {
		found = found || tg.path == custom
	}
	if !found {
		t.Error("HELIX_MODEL_DIR must be the directory offered for deletion")
	}
}

// A size is the whole reason anyone answers yes to the downloads prompt, so it
// has to be measured rather than guessed — and an unreadable entry must not
// take the number away.
func TestExistingWeightsMeasuresWhatIsOnDisk(t *testing.T) {
	helixDir := filepath.Join(t.TempDir(), ".helix")
	dir := filepath.Join(helixDir, "whisper-models")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}

	weights := existingWeights(purgeWeightTargets(helixDir))
	if len(weights) != 1 {
		t.Fatalf("expected only the directory that exists, got %d", len(weights))
	}
	if weights[0].size != 2048 {
		t.Errorf("size = %d, want 2048", weights[0].size)
	}
	if dirSize(filepath.Join(helixDir, "does-not-exist")) != 0 {
		t.Error("an absent directory measures zero rather than failing")
	}
}

// Grouping is a safety property, not decoration: credentials must not be able
// to end up filed under a group that describes itself as harmless.
func TestCredentialsAreNeverGroupedWithRecreatableState(t *testing.T) {
	home := t.TempDir()
	helixDir := filepath.Join(home, ".helix")

	for _, tg := range purgeTargets(home, helixDir) {
		base := filepath.Base(tg.path)
		if base == "secrets.json" || base == "openai_api_key" {
			if tg.group != groupCredentials {
				t.Errorf("%s is a credential but filed under %q", base, tg.group.title())
			}
		}
	}
	// And the group's note must not tell the reader it comes back by itself.
	note := strings.ToLower(groupCredentials.note())
	if strings.Contains(note, "recreated") || strings.Contains(note, "rebuilt") {
		t.Errorf("the credentials note must not imply recovery: %q", note)
	}
}

// Paths shorten against $HOME, and only against $HOME: anything outside it
// keeps its absolute form rather than acquiring a misleading "~/" prefix.
func TestShortPurgePathOnlyAbbreviatesUnderHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if got := shortPurgePath(home, filepath.Join(home, ".helix", "secrets.json")); got !=
		"~/.helix/secrets.json" {
		t.Errorf("got %q, want ~/.helix/secrets.json", got)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere", "weights")
	if got := shortPurgePath(home, outside); got != outside {
		t.Errorf("a path outside $HOME must stay absolute, got %q", got)
	}
}
