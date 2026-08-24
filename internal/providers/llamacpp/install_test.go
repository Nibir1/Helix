// internal/providers/llamacpp/install_test.go
// Purpose: "not installed" and "not running" are different problems.
//
// The setup flow used to answer an unreachable endpoint with
// `llama-server -m model.gguf --port 8080`, which on a machine without
// llama.cpp is a command that fails with "command not found" — a user reported
// exactly that. Advice you cannot act on costs more than none.
package llamacpp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServerInstalledFindsBinaryOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX executable bit")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	path, ok := ServerInstalled()
	if !ok {
		t.Fatal("a llama-server on PATH must be found")
	}
	if path != fake {
		t.Errorf("path = %q, want %q", path, fake)
	}
}

// TestServerInstalledFindsLegacyName: llama.cpp's server was called `server`
// before the 2024 rename, and some distro packages still install it that way.
func TestServerInstalledFindsLegacyName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX executable bit")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "server")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if _, ok := ServerInstalled(); !ok {
		t.Error("the legacy `server` binary name must be recognized")
	}
}

func TestServerInstalledReportsAbsence(t *testing.T) {
	// An empty PATH and a temp HOME: nothing to find anywhere.
	t.Setenv("PATH", t.TempDir())

	path, ok := ServerInstalled()
	// The well-known absolute locations are checked too, so on a machine that
	// genuinely has llama.cpp installed via brew this legitimately finds it.
	if ok && path == "" {
		t.Error("ok=true must come with a path")
	}
	if !ok && path != "" {
		t.Errorf("ok=false must come with an empty path, got %q", path)
	}
}

func TestInstallHintIsActionable(t *testing.T) {
	hint := InstallHint()
	if len(hint) == 0 {
		t.Fatal("there must always be some way to obtain llama.cpp")
	}
	joined := strings.Join(hint, "\n")

	// Whatever the platform, the hint must contain something runnable or a URL —
	// not just a statement that it is missing.
	if !strings.Contains(joined, "brew") &&
		!strings.Contains(joined, "git clone") &&
		!strings.Contains(joined, "http") {
		t.Errorf("hint carries no command or link:\n%s", joined)
	}
	if runtime.GOOS == "darwin" && !strings.Contains(joined, "brew install llama.cpp") {
		t.Errorf("on macOS the hint should offer the brew formula:\n%s", joined)
	}
}

// TestDiagnoseUnreachableNamesHostPortNotPath: nothing "listens on" a URL path,
// and printing /v1 invited the reading that the path was the problem.
func TestDiagnoseUnreachableNamesHostPortNotPath(t *testing.T) {
	kind, hint := Diagnose(nil, "http://127.0.0.1:8080/v1")
	if kind != DiagnosisUnreachable {
		t.Fatalf("kind = %v, want unreachable", kind)
	}
	if !strings.Contains(hint, "127.0.0.1:8080") {
		t.Errorf("hint should name host:port:\n%s", hint)
	}
	if strings.Contains(hint, "/v1") {
		t.Errorf("a socket-level message must not carry the URL path:\n%s", hint)
	}
	// The hint must carry a next step, and which one depends on whether the
	// binary exists — never a launch command for something absent.
	if _, installed := ServerInstalled(); installed {
		if !strings.Contains(hint, "llama-server -m") {
			t.Errorf("with the binary present, say how to start it:\n%s", hint)
		}
	} else if !strings.Contains(hint, "NOT INSTALLED") {
		t.Errorf("without the binary, say it is not installed:\n%s", hint)
	}
}

func TestDiagnoseForeignServerUnchanged(t *testing.T) {
	kind, hint := Diagnose(&stubStatusErr{}, "http://127.0.0.1:8080/v1")
	if kind != DiagnosisForeignServer {
		t.Fatalf("an HTTP status should mean a foreign server, got %v", kind)
	}
	if !strings.Contains(hint, "8081") {
		t.Errorf("the foreign-server hint should suggest a free port:\n%s", hint)
	}
}

// stubStatusErr carries the "HTTP " marker Diagnose keys on.
type stubStatusErr struct{}

func (stubStatusErr) Error() string { return "HTTP 403: forbidden" }

func TestHostPort(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:8080/v1": "127.0.0.1:8080",
		"http://127.0.0.1:8080":    "127.0.0.1:8080",
		"https://host/v1":          "host",
		"nonsense":                 "nonsense",
	}
	for in, want := range cases {
		if got := hostPort(in); got != want {
			t.Errorf("hostPort(%q) = %q, want %q", in, got, want)
		}
	}
}

// -------------------------------------------------------
// models on disk
// -------------------------------------------------------

func TestCacheDirsHonorsEnvironment(t *testing.T) {
	t.Setenv("LLAMA_CACHE", "/custom/llama")
	t.Setenv("HF_HUB_CACHE", "/custom/hf")
	t.Setenv("HF_HOME", "/custom/hfhome")

	dirs := CacheDirs()
	for _, want := range []string{"/custom/llama", "/custom/hf", "/custom/hfhome/hub"} {
		if !containsStr(dirs, want) {
			t.Errorf("CacheDirs missing %q: %v", want, dirs)
		}
	}
	// The env-provided locations must come before the defaults, so an explicit
	// override wins.
	if dirs[0] != "/custom/llama" {
		t.Errorf("LLAMA_CACHE should be checked first, got %v", dirs)
	}
}

func TestCacheDirsFallsBackToDefaults(t *testing.T) {
	t.Setenv("LLAMA_CACHE", "")
	t.Setenv("HF_HUB_CACHE", "")
	t.Setenv("HF_HOME", "")

	dirs := CacheDirs()
	if len(dirs) == 0 {
		t.Fatal("there must always be default cache locations to check")
	}
	// Both historical locations matter: -hf downloads moved from llama.cpp's own
	// cache into the shared Hugging Face hub cache, and a model pulled by either
	// version must be found.
	var haveLlama, haveHF bool
	for _, d := range dirs {
		if strings.Contains(d, filepath.Join(".cache", "llama.cpp")) {
			haveLlama = true
		}
		if strings.Contains(d, filepath.Join(".cache", "huggingface", "hub")) {
			haveHF = true
		}
	}
	if !haveLlama || !haveHF {
		t.Errorf("both cache generations must be checked: %v", dirs)
	}
}

// isolateCaches points every cache root CacheDirs() consults at a temp dir.
//
// Setting LLAMA_CACHE/HF_HUB_CACHE/HF_HOME is not enough: CacheDirs also adds
// $HOME/.cache/llama.cpp and $HOME/.cache/huggingface/hub from os.UserHomeDir(),
// so without overriding HOME these tests scan the developer's REAL model cache.
// They then pass or fail depending on whether that machine happens to hold a
// GGUF — which is how they behaved until a genuine model download made them fail.
// A test whose result depends on what else is installed is not testing the code.
func isolateCaches(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("LLAMA_CACHE", root)
	t.Setenv("HF_HUB_CACHE", "")
	t.Setenv("HF_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	return root
}

func TestCachedModelsFindsGGUFAtDepth(t *testing.T) {
	root := isolateCaches(t)

	// The HF hub layout: models--org--name/snapshots/<hash>/file.gguf
	deep := filepath.Join(root, "models--ggml-org--gemma", "snapshots", "abc123")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(deep, "gemma-4-e2b-it-Q4_K_M.gguf")
	if err := os.WriteFile(big, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(root, "tiny.gguf")
	if err := os.WriteFile(small, make([]byte, 512), 0o644); err != nil {
		t.Fatal(err)
	}
	// Not a GGUF; must be ignored.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := CachedModels()
	if len(got) != 2 {
		t.Fatalf("found %d models, want 2: %+v", len(got), got)
	}
	// Largest first: the biggest model a machine holds is usually the most
	// capable one it can run.
	if got[0].Name != "gemma-4-e2b-it-Q4_K_M.gguf" {
		t.Errorf("largest should sort first, got %q", got[0].Name)
	}
	if got[0].SizeBytes != 4096 {
		t.Errorf("size = %d, want 4096", got[0].SizeBytes)
	}
}

// TestCachedModelsDeduplicatesSymlinks: the HF cache stores each blob once and
// symlinks snapshots at it, so the same weights appear under two paths.
func TestCachedModelsDeduplicatesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ")
	}
	root := isolateCaches(t)

	blobs := filepath.Join(root, "blobs")
	snaps := filepath.Join(root, "snapshots", "abc")
	for _, d := range []string{blobs, snaps} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	real := filepath.Join(blobs, "weights.gguf")
	if err := os.WriteFile(real, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(snaps, "model.gguf")); err != nil {
		t.Fatal(err)
	}

	if got := CachedModels(); len(got) != 1 {
		t.Errorf("the same weights must be listed once, got %d: %+v", len(got), got)
	}
}

func TestCachedModelsHandlesMissingDirs(t *testing.T) {
	isolateCaches(t)
	t.Setenv("LLAMA_CACHE", filepath.Join(t.TempDir(), "absent"))
	// Must not panic or error — never having pulled anything is normal.
	if got := CachedModels(); len(got) != 0 {
		t.Errorf("expected no models, got %+v", got)
	}
}

func TestPullAndServeCommands(t *testing.T) {
	// -hf is the answer to "can llama.cpp fetch models itself?" — it can, and
	// caches the result, so no second runtime is needed to obtain weights.
	if got := PullCommand("ggml-org/gemma-4-E2B-it-GGUF", "8081"); got !=
		"llama-server -hf ggml-org/gemma-4-E2B-it-GGUF --port 8081" {
		t.Errorf("PullCommand = %q", got)
	}
	if got := PullCommand("org/repo", ""); !strings.Contains(got, "--port 8080") {
		t.Errorf("an empty port should default to 8080, got %q", got)
	}
	if got := ServeCommand("/models/x.gguf", "8080"); got !=
		"llama-server -m /models/x.gguf --port 8080" {
		t.Errorf("ServeCommand = %q", got)
	}
}

func TestInstallCommandRequiresBrew(t *testing.T) {
	// Without brew on PATH there is no single unambiguous command, and building
	// llama.cpp involves GPU-backend choices Helix must not make for the user.
	t.Setenv("PATH", t.TempDir())
	if cmd, ok := InstallCommand(); ok {
		t.Errorf("without brew there should be no offer, got %q", cmd)
	}
}

func TestSizeGBRendering(t *testing.T) {
	if got := (CachedModel{SizeBytes: 6 << 30}).SizeGB(); got != 6 {
		t.Errorf("SizeGB = %v, want 6", got)
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
