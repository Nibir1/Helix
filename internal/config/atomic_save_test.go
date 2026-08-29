// internal/config/atomic_save_test.go
//
// Purpose: a failed save must not destroy the settings it was replacing.
//
// SavePreferences used os.WriteFile, which opens with O_TRUNC: the file is
// emptied and then written. When the write fails halfway the user is left with
// nothing — no provider, no voice chain, no preferences — and the way it fails
// in practice is a full disk, which is exactly the moment several other things
// are failing too and nobody is reading carefully. Observed one step from
// happening:
//
//	✘ preferences  could not be saved: open ~/.helix/config.json:
//	  no space left on device
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A save must replace the file by rename, leaving no partial state.
func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := &Config{ConfigPath: path, Provider: "openai"}
	if err := cfg.SavePreferences(); err != nil {
		t.Fatalf("first save: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(first) {
		t.Fatal("the saved config is not valid JSON")
	}

	cfg.Provider = "anthropic"
	if err := cfg.SavePreferences(); err != nil {
		t.Fatalf("second save: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(second), "anthropic") {
		t.Error("the second save did not take effect")
	}

	// No debris: a temp file left behind would accumulate one per save.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" {
			t.Errorf("save left %q behind", e.Name())
		}
	}
}

// The replacement must be a rename, not a truncate-and-write.
//
// Checked in the source because simulating a full disk portably is not
// practical, and the property is structural: O_TRUNC is the bug, rename is the
// fix, and no assertion about file CONTENT can tell them apart on a run where
// the write happens to succeed.
func TestSaveReplacesByRenameNotTruncate(t *testing.T) {
	src, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(string(src), "\r\n", "\n")
	i := strings.Index(body, "func (cfg *Config) SavePreferences()")
	if i < 0 {
		t.Fatal("SavePreferences not found")
	}
	fn := body[i:]
	if end := strings.Index(fn[10:], "\nfunc "); end > 0 {
		fn = fn[:end+10]
	}
	// Strip comments: this function's own doc names os.WriteFile as the thing
	// it replaced, and a naive grep reads that as the bug still being present.
	var code strings.Builder
	for _, line := range strings.Split(fn, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		code.WriteString(line)
		code.WriteString("\n")
	}

	if strings.Contains(code.String(), "os.WriteFile") {
		t.Error("SavePreferences writes in place; O_TRUNC empties the file " +
			"before writing, so a failure loses every setting")
	}
	if !strings.Contains(code.String(), "os.Rename") {
		t.Error("SavePreferences does not finish with a rename, so the " +
			"replacement is not atomic")
	}
	if !strings.Contains(code.String(), "Sync()") {
		t.Error("the temp file is not fsynced before the rename; after a crash " +
			"the directory entry can point at an empty file")
	}
	// The temp file must live beside the target: rename cannot cross a
	// filesystem, and TMPDIR is frequently a different one.
	if !strings.Contains(code.String(), "CreateTemp(dir") {
		t.Error("the temp file is not created in the config's own directory, so " +
			"the rename may cross a filesystem and stop being atomic")
	}
}

// A pre-existing config must survive a save that cannot complete.
func TestExistingConfigSurvivesAFailedSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := &Config{ConfigPath: path, Provider: "openai"}
	if err := cfg.SavePreferences(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Make the directory unwritable so the temp file cannot be created. The
	// existing config must be exactly as it was.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("cannot make the directory read-only here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := cfg.SavePreferences(); err == nil {
		t.Skip("the save unexpectedly succeeded; cannot test the failure path")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the config is gone after a failed save: %v", err)
	}
	if string(after) != string(before) {
		t.Error("a failed save modified the existing config")
	}
	if !json.Valid(after) {
		t.Error("a failed save left invalid JSON behind")
	}
}
