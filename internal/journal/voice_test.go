// internal/journal/voice_test.go
// Purpose: P2.8 — prove the opt-in voice log's privacy contract. The most
// important test here is the negative one: disabled must mean an untouched
// filesystem, not an empty file.
package journal

import (
	"os"
	"path/filepath"
	"testing"
)

// The headline guarantee (threat V5, guardrail #6): off means nothing on disk.
func TestDisabledVoiceLogTouchesNothing(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "voice_log")

	vl, err := OpenVoiceLog(dir, false, Options{})
	if err != nil {
		t.Fatalf("OpenVoiceLog(disabled): %v", err)
	}
	if vl.Enabled() {
		t.Fatal("a disabled voice log must report Enabled() == false")
	}

	// Every method must be a safe no-op on the nil log, so no call site needs
	// to guard — a forgotten guard is how an opt-out leaks.
	vl.Heard("delete everything", "groq", 0.91, OutcomePlanner)
	vl.Spoke("I will not do that")
	vl.Note("capture failed")
	if got := vl.Tail(10); got != nil {
		t.Fatalf("disabled tail = %v, want nil", got)
	}
	if got := vl.Path(); got != "" {
		t.Fatalf("disabled path = %q, want empty", got)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("disabled voice log must not create %s (stat err = %v)", dir, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("disabled voice log wrote %d entries into the home dir: %v", len(entries), entries)
	}
}

// Enabling it must record both directions with the metadata that makes the log
// an audit: which provider produced the transcript, how confident it was, and
// what the pipeline did about it.
func TestEnabledVoiceLogRecordsBothDirections(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "voice_log")
	vl, err := OpenVoiceLog(dir, true, Options{})
	if err != nil {
		t.Fatalf("OpenVoiceLog: %v", err)
	}
	if !vl.Enabled() {
		t.Fatal("enabled voice log must report Enabled() == true")
	}

	vl.Heard("what is on my list", "groq", 0.93, OutcomeCommand)
	vl.Spoke("Three things.")

	got := vl.Tail(10)
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(got), got)
	}
	heard := got[0]
	if heard.Dir != DirHeard || heard.Text != "what is on my list" {
		t.Fatalf("heard entry wrong: %+v", heard)
	}
	if heard.Provider != "groq" || heard.Confidence != 0.93 {
		t.Fatalf("heard entry lost its STT metadata: %+v", heard)
	}
	if heard.Outcome != OutcomeCommand {
		t.Fatalf("heard outcome = %q, want %q", heard.Outcome, OutcomeCommand)
	}
	if got[1].Dir != DirSpoke || got[1].Text != "Three things." {
		t.Fatalf("spoke entry wrong: %+v", got[1])
	}
	if got[1].TS.IsZero() {
		t.Fatal("entries must carry a timestamp")
	}
}

// The log records TEXT and metadata only. There is no field for audio, and no
// recorded value may be a path to a captured clip — clips are deleted right
// after they are read, so a stored reference would be a privacy liability
// pointing at nothing.
func TestVoiceLogCarriesNoAudioReference(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "voice_log")
	vl, err := OpenVoiceLog(dir, true, Options{})
	if err != nil {
		t.Fatalf("OpenVoiceLog: %v", err)
	}
	vl.Heard("hello", "whisper-local", 0, OutcomePlanner)

	data, err := os.ReadFile(vl.Path())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	for _, bad := range []string{".wav", ".pcm", ".mp3", "helix-clip", "/tmp/"} {
		if containsFold(string(data), bad) {
			t.Fatalf("voice log line references audio (%q): %s", bad, data)
		}
	}
}

// Redaction applies to the log, not just to the journal: a transcript is
// attacker-influenced text (V1), and terminal escapes in it must not survive
// to a later `cat`.
func TestVoiceLogRedactsControlCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "voice_log")
	vl, err := OpenVoiceLog(dir, true, Options{})
	if err != nil {
		t.Fatalf("OpenVoiceLog: %v", err)
	}
	vl.Heard("run \x1b[31mrm -rf\x1b[0m now", "groq", 0.5, OutcomeRefused)

	got := vl.Tail(1)
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	for _, r := range got[0].Text {
		if r < 32 && r != '\t' {
			t.Fatalf("control character %q survived redaction: %q", r, got[0].Text)
		}
	}
}

func containsFold(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		indexFold(haystack, needle) >= 0
}

func indexFold(s, sub string) int {
	lower := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + 32
		}
		return b
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			if lower(s[i+j]) != lower(sub[j]) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
