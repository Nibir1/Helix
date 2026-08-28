package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultWhisperModelIsAccurateEnough pins a measured decision.
//
// base.en transcribed "Helix voice loop is online" as "He'll expose Looper's
// online" on this project's own vocabulary — a shell that cannot hear its own
// name is not a working voice interface. small.en gets it right and still runs
// at roughly 9x real time on Apple Silicon, so the cost is disk, not latency.
func TestDefaultWhisperModelIsAccurateEnough(t *testing.T) {
	if defaultWhisperModel == "base.en" {
		t.Error("base.en mishears the command vocabulary; it must not be the default")
	}

	var found bool
	for _, m := range whisperModels() {
		if m.Name == defaultWhisperModel {
			found = true
			if m.SizeMB < 200 {
				t.Errorf("default %s is %d MB — suspiciously small for the accuracy claim",
					m.Name, m.SizeMB)
			}
		}
	}
	if !found {
		t.Fatalf("default %q is not in the model table", defaultWhisperModel)
	}
}

func TestWhisperModelsAreOrderedAndComplete(t *testing.T) {
	models := whisperModels()
	if len(models) < 2 {
		t.Fatal("there must be a real choice of models")
	}
	for i, m := range models {
		if m.Name == "" || m.File == "" || m.Accuracy == "" {
			t.Errorf("model %d is incomplete: %+v", i, m)
		}
		if !strings.HasPrefix(m.File, "ggml-") || !strings.HasSuffix(m.File, ".bin") {
			t.Errorf("model %q has an unexpected filename %q", m.Name, m.File)
		}
		if i > 0 && m.SizeMB <= models[i-1].SizeMB {
			t.Errorf("models must be ordered smallest first: %s (%d MB) after %s (%d MB)",
				m.Name, m.SizeMB, models[i-1].Name, models[i-1].SizeMB)
		}
	}
}

// TestInstalledWhisperModelPrefersTheMostAccurate: someone who deliberately
// fetched a larger model must not be silently downgraded to a smaller one that
// happens to also be on disk.
func TestInstalledWhisperModelPrefersTheMostAccurate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows

	dir := filepath.Join(home, ".helix", "whisper-models")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Nothing installed: falls back to the default.
	if _, ok := installedWhisperModel(); ok {
		t.Error("no model on disk must report none installed")
	}
	if got := chosenWhisperModel().Name; got != defaultWhisperModel {
		t.Errorf("with nothing installed, chose %q, want the default %q", got, defaultWhisperModel)
	}

	// Only the smallest present.
	writeFile(t, filepath.Join(dir, "ggml-base.en.bin"))
	if got := chosenWhisperModel().Name; got != "base.en" {
		t.Errorf("chose %q, want the installed base.en", got)
	}

	// A larger one alongside it wins.
	writeFile(t, filepath.Join(dir, "ggml-medium.en.bin"))
	if got := chosenWhisperModel().Name; got != "medium.en" {
		t.Errorf("chose %q, want the more accurate medium.en", got)
	}
	if !strings.HasSuffix(whisperModelPath(), "ggml-medium.en.bin") {
		t.Errorf("model path = %q, want the medium model", whisperModelPath())
	}
}

func TestWhisperModelURLFor(t *testing.T) {
	got := whisperModelURLFor("ggml-small.en.bin")
	if !strings.HasPrefix(got, "https://huggingface.co/") ||
		!strings.HasSuffix(got, "ggml-small.en.bin") {
		t.Errorf("URL = %q", got)
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
