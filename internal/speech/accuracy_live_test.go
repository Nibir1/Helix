// internal/speech/accuracy_live_test.go
// Purpose: P7.8 — the §10 metrics run, for the one row this machine can actually
// measure: "STT accuracy (clean speech) — local ≥90%, measured by fixture
// corpus". No corpus existed and no accuracy figure had ever been computed.
//
// The measurement is word accuracy (1 − word error rate) over a corpus of
// synthesized utterances put through the REAL local chain: piper-quality TTS from
// macOS `say` into a stock whisper.cpp server, via Helix's own adapter.
//
// HONEST SCOPE, because this number will be quoted. Synthesized speech is CLEAN
// speech in the most favourable sense: one voice, no room, no background, perfect
// articulation, no accent the model has not heard. It is an UPPER BOUND on what a
// person gets in a kitchen. §10 says "clean speech", so this is the right
// measurement for the row — but it is not evidence about real-world accuracy, and
// a corpus of real recordings would be a different and harder number.
//
// Opt-in via HELIX_LIVE_SIDECAR=1, skipping loudly otherwise (§9 rule 6).
package speech

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// sttCorpus is the fixture set: phrasings Helix actually receives, mixing
// conversational requests with shell-shaped ones, short and long.
//
// Deliberately includes the cases that matter for this project rather than
// generic pangrams: sentences whose first word is a command name (the routing
// bug Phase 2 fixed), a spoken slash command, and a couple of technical nouns
// that small models mangle.
var sttCorpus = []string{
	"the quick brown fox jumps over the lazy dog",
	"list the files in this directory",
	"show me the git status",
	"make a new branch called test",
	"what did I ask you a moment ago",
	"turn off your eyes",
	"switch to manual mode",
	"commit this with the message initial import",
	"how much disk space is left on this machine",
	"read the last twenty lines of the log file",
}

// normalizeWords lowercases and strips punctuation so scoring compares words,
// not typography. Whisper returns leading spaces and a trailing period; neither
// is a recognition error.
func normalizeWords(s string) []string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == ' ':
			b.WriteRune(r)
		case r == '-' || r == '\'':
			// Keep intra-word punctuation out of the comparison entirely.
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Fields(b.String())
}

// wordErrors returns the Levenshtein distance between two word sequences —
// substitutions, insertions and deletions, which is what WER counts.
func wordErrors(want, got []string) int {
	prev := make([]int, len(got)+1)
	cur := make([]int, len(got)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(want); i++ {
		cur[0] = i
		for j := 1; j <= len(got); j++ {
			cost := 1
			if want[i-1] == got[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(got)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// TestLiveSTTAccuracyLocalChain is the §10 measurement.
func TestLiveSTTAccuracyLocalChain(t *testing.T) {
	requireLiveSidecar(t)

	model := whisperModel(t)
	endpoint := startWhisperServer(t, model)
	adapter := NewWhisperLocalSTT("", endpoint)

	const target = 90.0 // §10 local-path floor

	var totalWords, totalErrors int
	var perfect int
	var slowest time.Duration

	for _, want := range sttCorpus {
		clipPath := spokenWAV(t, want)
		raw, err := os.ReadFile(clipPath)
		if err != nil {
			t.Fatalf("read clip: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		start := time.Now()
		tr, err := adapter.Transcribe(ctx, AudioFormat{
			Kind: "wav", SampleRate: 16000, Channels: 1, Bytes: raw,
		})
		elapsed := time.Since(start)
		cancel()
		if err != nil {
			t.Fatalf("transcribe %q: %v", want, err)
		}
		if elapsed > slowest {
			slowest = elapsed
		}

		wantWords := normalizeWords(want)
		gotWords := normalizeWords(tr.Text)
		errs := wordErrors(wantWords, gotWords)

		totalWords += len(wantWords)
		totalErrors += errs
		if errs == 0 {
			perfect++
			t.Logf("  ✓ %q", tr.Text)
		} else {
			t.Logf("  ✗ %d/%d words wrong\n      want: %s\n      got:  %s",
				errs, len(wantWords), want, strings.TrimSpace(tr.Text))
		}
	}

	accuracy := (1 - float64(totalErrors)/float64(totalWords)) * 100
	t.Logf("LOCAL STT accuracy: %.1f%% word accuracy (%d/%d words correct across %d utterances)",
		accuracy, totalWords-totalErrors, totalWords, len(sttCorpus))
	t.Logf("  utterances transcribed perfectly: %d/%d", perfect, len(sttCorpus))
	t.Logf("  slowest transcription: %s", slowest.Round(time.Millisecond))
	t.Logf("  NOTE: synthesized clean speech — an upper bound, not real-room accuracy")

	if accuracy < target {
		t.Errorf("local STT accuracy %.1f%% is below the §10 floor of %.0f%%", accuracy, target)
	}
}

// The scoring itself must be right, or the number above means nothing. These run
// without any sidecar.
func TestWordErrorScoring(t *testing.T) {
	cases := []struct {
		want, got string
		errors    int
	}{
		{"hello world", "hello world", 0},
		{"hello world", "hello  world", 0},    // whitespace
		{"Hello, world.", "hello world", 0},   // case and punctuation
		{"hello world", "hello werld", 1},     // substitution
		{"hello world", "hello", 1},           // deletion
		{"hello world", "hello big world", 1}, // insertion
		// A full reversal of four distinct words costs four substitutions, not
		// two: Levenshtein can preserve at most one alignment here. My first
		// version of this case asserted 2 and the implementation was right —
		// which is the reason the scorer gets tested at all, since a wrong
		// scorer would have made the accuracy figure below meaningless.
		{"a b c d", "d c b a", 4},
		{"turn off your eyes", "turn off your ice", 1},
	}
	for _, c := range cases {
		got := wordErrors(normalizeWords(c.want), normalizeWords(c.got))
		if got != c.errors {
			t.Errorf("wordErrors(%q, %q) = %d, want %d", c.want, c.got, got, c.errors)
		}
	}
}

// An empty transcript must score as total loss rather than as a pass. A provider
// returning nothing is the failure mode that silently flattered every average
// until someone checked.
func TestEmptyTranscriptScoresAsTotalLoss(t *testing.T) {
	want := normalizeWords("list the files in this directory")
	if got := wordErrors(want, normalizeWords("")); got != len(want) {
		t.Fatalf("an empty transcript should cost every word: got %d, want %d", got, len(want))
	}
}
