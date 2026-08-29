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
		// Outside ~/.helix, and the reason this list grew: a /purge that
		// reported a clean sweep left 6 GB of CSM weights in the Hugging Face
		// cache and whatever Ollama had pulled. "Wipe ALL local Helix data" was
		// untrue by tens of gigabytes.
		"models--sesame--csm-1b",
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
	// Isolate the stores that live OUTSIDE ~/.helix.
	//
	// purgeWeightTargets now also offers Ollama's blobs and the CSM weights in
	// the Hugging Face cache, which are real paths on a real machine — so
	// without this the result depends on whether the developer happens to have
	// downloaded a model, which is not a property of the code.
	isolated := t.TempDir()
	t.Setenv("HOME", isolated)
	t.Setenv("USERPROFILE", isolated)
	t.Setenv("HF_HUB_CACHE", filepath.Join(isolated, "no-hub"))
	t.Setenv("OLLAMA_MODELS", filepath.Join(isolated, "no-ollama"))

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

// The two model stores that live outside ~/.helix must be reachable.
//
// /purge described itself as wiping all local Helix data while leaving the
// largest downloads on disk: Ollama's blobs, and the CSM weights in the Hugging
// Face cache. Verified on a machine where ~/.helix had been purged down to two
// lock files and 6 GB of weights remained.
func TestPurgeReachesTheExternalModelStores(t *testing.T) {
	helixDir := filepath.Join(t.TempDir(), ".helix")
	var sawOllama, sawCSMWeights bool
	for _, tg := range purgeWeightTargets(helixDir) {
		if strings.Contains(tg.path, filepath.Join(".ollama", "models")) ||
			strings.Contains(strings.ToLower(tg.desc), "ollama") {
			sawOllama = true
		}
		if strings.Contains(tg.path, "models--sesame--csm-1b") {
			sawCSMWeights = true
		}
	}
	if !sawOllama {
		t.Error("Ollama's model store is not offered; a user who pulled a local " +
			"brain keeps gigabytes after a full purge")
	}
	if !sawCSMWeights {
		t.Error("the CSM weights in the Hugging Face cache are not offered; they " +
			"are the single largest thing the voice wizard downloads")
	}
}

// A shared store must say that it is shared.
//
// Ollama's directory holds whatever the user pulled with Ollama directly, and
// the manifest is the only place they find that out before answering.
func TestSharedStoresAreLabelledAsShared(t *testing.T) {
	for _, tg := range purgeWeightTargets(filepath.Join(t.TempDir(), ".helix")) {
		if !strings.Contains(strings.ToLower(tg.desc), "ollama") {
			continue
		}
		if !strings.Contains(strings.ToLower(tg.desc), "shared") {
			t.Errorf("the Ollama target is described as %q without saying it is "+
				"shared with the user's own Ollama use", tg.desc)
		}
	}
}

// The Hugging Face cache must be resolved the way huggingface_hub resolves it.
//
// Guessing ~/.cache/huggingface unconditionally would miss the weights on a
// machine that moved them, and /purge would report a clean sweep having left
// 6 GB behind — the exact failure this whole change is about.
func TestHuggingFaceCacheHonoursItsEnvironment(t *testing.T) {
	t.Setenv("HF_HUB_CACHE", "/custom/hub")
	if got := huggingFaceHubDir(); got != "/custom/hub" {
		t.Errorf("HF_HUB_CACHE ignored: got %q", got)
	}

	t.Setenv("HF_HUB_CACHE", "")
	t.Setenv("HF_HOME", "/custom/hf")
	if got := huggingFaceHubDir(); got != filepath.Join("/custom/hf", "hub") {
		t.Errorf("HF_HOME ignored: got %q", got)
	}

	t.Setenv("HF_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got := huggingFaceHubDir(); got != filepath.Join(home, ".cache", "huggingface", "hub") {
		t.Errorf("default cache path wrong: got %q", got)
	}
}

// `make deep-clean` and /purge must agree about what Helix downloaded.
//
// They answer the same question — "remove everything Helix put on this disk" —
// from two different places, and the way that fails is silently: /purge grew
// csm.rs and the Hugging Face weights while the Makefile still knew only about
// models/, so a "deep clean" left several gigabytes behind and said it was
// finished. Whichever list grows next, this fails until the other one does.
func TestDeepCleanCoversTheSamePathsAsPurge(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	body := strings.ReplaceAll(string(raw), "\r\n", "\n")
	i := strings.Index(body, "deep-clean:")
	if i < 0 {
		t.Fatal("no deep-clean target")
	}
	target := body[i:]
	if end := strings.Index(target, "\n\n"); end > 0 {
		target = target[:end]
	}

	// Every ~/.helix directory /purge offers must appear in deep-clean. The
	// external stores are deliberately excluded: a Makefile cannot show sizes
	// and ask, and those are shared with other tools.
	for _, tg := range purgeWeightTargets("$(HELIX_HOME)") {
		base := filepath.Base(tg.path)
		if base == "models--sesame--csm-1b" || strings.Contains(tg.path, ".ollama") {
			// Shared stores: deep-clean must NAME them rather than delete them.
			if !strings.Contains(target, "NOT removed") {
				t.Error("deep-clean does not tell the user about the model stores " +
					"it leaves behind")
			}
			continue
		}
		if !strings.Contains(target, base) {
			t.Errorf("/purge offers %s and `make deep-clean` does not remove it — "+
				"a deep clean that leaves gigabytes is not one", base)
		}
	}
}

// make clean must never take the API keys with it.
func TestCleanPreservesCredentials(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(string(raw), "\r\n", "\n")
	i := strings.Index(body, "\nclean:")
	if i < 0 {
		t.Fatal("no clean target")
	}
	target := body[i+1:]
	if end := strings.Index(target, "\n# Everything clean does"); end > 0 {
		target = target[:end]
	}

	// The specific shape that caused it: a blanket delete of every .json.
	for _, line := range strings.Split(target, "\n") {
		code := line
		if h := strings.Index(code, "#"); h >= 0 {
			code = code[:h]
		}
		if strings.Contains(code, `-name "*.json"`) && strings.Contains(code, "-delete") {
			t.Error("clean deletes every .json under ~/.helix, which takes " +
				"secrets.json — the API keystore — with it")
		}
		if strings.Contains(code, "secrets.json") && strings.Contains(code, "rm") {
			t.Error("clean removes secrets.json; re-entering API keys is not " +
				"part of cleaning a build")
		}
	}
}

// delete-secrets must reach every credential Helix stores, and stop there.
//
// It is separate from clean and deep-clean deliberately: clean must never take
// credentials (it used to, through a blanket .json delete), but revoking what is
// on this machine is a real thing to want on its own — handing a laptop on,
// filing a bug with a transcript, or after a key has leaked.
func TestDeleteSecretsCoversEveryCredentialStore(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(string(raw), "\r\n", "\n")
	i := strings.Index(body, "\ndelete-secrets:")
	if i < 0 {
		t.Fatal("no delete-secrets target")
	}
	target := body[i:]
	if end := strings.Index(target, "\n\n# "); end > 0 {
		target = target[:end]
	}

	// Every file in the tree that holds a credential.
	for _, want := range []struct{ path, what string }{
		{"secrets.json", "the provider API keystore"},
		{"daemon.conn.json", "the daemon's auth token"},
		{"voice_log", "spoken transcripts"},
	} {
		if !strings.Contains(target, want.path) {
			t.Errorf("delete-secrets does not remove %s (%s)", want.path, want.what)
		}
	}

	// It must not quietly widen into a purge.
	for _, keep := range []string{"models", "whisper-models", "piper-voices", "csm.rs"} {
		if strings.Contains(target, `"$(HELIX_HOME)/`+keep+`"`) {
			t.Errorf("delete-secrets removes %s; deleting models is deep-clean's "+
				"job and this target is about credentials", keep)
		}
	}

	// A shared credential with its own revoke must be named, not half-removed.
	if !strings.Contains(target, "hf auth logout") {
		t.Error("the Hugging Face token is not mentioned; it lives in a shared " +
			"cache and deleting the file leaves a half-removed login")
	}
	// Environment keys are not files, and silence about that is misleading.
	if !strings.Contains(strings.ToLower(target), "environment") {
		t.Error("delete-secrets does not say that keys set in the environment " +
			"survive it")
	}
}
