// internal/speech/oneline_test.go
//
// Purpose: hold the shape of a spoken utterance.
//
// From a live session, whisper-local returned an utterance as two segments:
//
//	❯ (coughing)
//	 What can you do for me?
//
// which is one thing the user said, on two lines, echoed twice and with the
// provider label stranded. The display was the visible half; the half that
// mattered is that this text is SUBMITTED — it reaches the classifier and the
// shell as input, where a newline is a second line of something.
package speech

import (
	"context"
	"path/filepath"
	"testing"

	"helix/internal/providers"
)

func TestOneLineCollapsesWhisperSegments(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			"the reported case",
			"(coughing)\n What can you do for me?",
			"(coughing) What can you do for me?",
		},
		{
			"a sentence split across segments",
			"When you rebooted, did you download the latest binaries and\n install them?",
			"When you rebooted, did you download the latest binaries and install them?",
		},
		{"windows line endings", "one\r\ntwo", "one two"},
		{"leading and trailing space", "  hello there  ", "hello there"},
		{"whisper's double spacing", "one.  two.", "one. two."},
		{"tabs", "one\ttwo", "one two"},
		{"already clean", "just one line", "just one line"},
		{"only whitespace", " \n\t ", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := OneLine(c.in); got != c.want {
				t.Errorf("OneLine(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// The invariant the submitted-input side depends on.
func TestOneLineNeverReturnsANewline(t *testing.T) {
	for _, in := range []string{
		"a\nb", "a\r\nb", "a\n\n\nb", "\na\n", "a\rb", "a\vb", "a\fb",
	} {
		got := OneLine(in)
		for _, r := range got {
			if r == '\n' || r == '\r' {
				t.Errorf("OneLine(%q) = %q, which still contains a line break", in, got)
				break
			}
		}
	}
}

// multiLineSTT returns whisper.cpp-shaped output: one segment per line.
type multiLineSTT struct{ name string }

func (m *multiLineSTT) Name() string         { return m.name }
func (m *multiLineSTT) DisplayName() string  { return m.name }
func (m *multiLineSTT) SetAPIKey(string)     {}
func (m *multiLineSTT) RequiresAPIKey() bool { return false }
func (m *multiLineSTT) IsLocal() bool        { return true }
func (m *multiLineSTT) DefaultModel() string { return m.name }
func (m *multiLineSTT) Transcribe(context.Context, AudioFormat) (Transcript, error) {
	return Transcript{Text: "(coughing)\n What can you do for me?", Provider: m.name}, nil
}
func (m *multiLineSTT) HealthCheck(context.Context) error { return nil }

// The chain must hand back one line, so the fix is not merely available but
// applied — a helper that works and is never called fixes nothing.
func TestTranscribeReturnsASingleLine(t *testing.T) {
	keys, err := providers.NewKeyStoreAt(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatalf("keystore: %v", err)
	}
	r := NewRegistry(keys, providers.NewHTTPClient(5e9))
	r.RegisterSTT(&multiLineSTT{name: "whisper-local"})
	r.SetConfig(Config{STT: STTConfig{Provider: "whisper-local"}})

	got, err := r.Transcribe(context.Background(), AudioFormat{Bytes: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	const want = "(coughing) What can you do for me?"
	if got.Text != want {
		t.Errorf("Transcribe returned %q, want %q\n"+
			"a transcript with a newline is submitted to the classifier as two "+
			"lines of input", got.Text, want)
	}
}
