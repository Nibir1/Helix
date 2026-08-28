// internal/speech/stream_speak_test.go
// Purpose: Sentence splitter behavior — the basis of one-ahead TTS
// pipelining. Playback needs a device; the splitter is pure and tested here.
package speech

import "testing"

func TestSplitSentences(t *testing.T) {
	got := SplitSentences("Systems nominal. Deploying now! Ready?")
	want := []string{"Systems nominal.", "Deploying now!", "Ready?"}
	if len(got) != len(want) {
		t.Fatalf("want %d sentences, got %d: %q", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sentence %d: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestSplitSentencesSingle(t *testing.T) {
	if got := SplitSentences("just one clause with no terminator"); len(got) != 1 {
		t.Fatalf("a single clause must stay one chunk, got %q", got)
	}
}

func TestSplitSentencesEmpty(t *testing.T) {
	if got := SplitSentences("   "); got != nil {
		t.Fatalf("blank input must yield no chunks, got %q", got)
	}
}

func TestSplitSentencesLongClauseIsBounded(t *testing.T) {
	long := ""
	for i := 0; i < 100; i++ {
		long += "word "
	}
	for _, c := range SplitSentences(long) {
		if len([]rune(c)) > maxSpokenSentence+10 {
			t.Fatalf("chunk exceeded bound: %d runes", len([]rune(c)))
		}
	}
}

func TestSplitSentencesDecimalNotSplit(t *testing.T) {
	// "3.14" has no space after the dot, so it must not split mid-number.
	got := SplitSentences("Pi is 3.14 exactly.")
	if len(got) != 1 {
		t.Fatalf("decimal should not split the sentence, got %q", got)
	}
}
