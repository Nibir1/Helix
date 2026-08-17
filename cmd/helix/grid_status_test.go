// cmd/helix/grid_status_test.go
// Purpose: the per-turn status line must describe reality. The QA session
// printed "GRID STATUS :: CLEAR" after every turn while whisper-local was
// unreachable and TTS was over budget, because the line was a constant.
package main

import (
	"strings"
	"testing"

	"helix/internal/speech"
)

func TestGridStatusClearWhenEverythingUsedPrimaries(t *testing.T) {
	got := evaluateGridStatus(gridSignals{
		STT: speech.ChainHealth{Attempted: true, OK: true, Used: "groq"},
		TTS: speech.ChainHealth{Attempted: true, OK: true, Used: "openai"},
	})

	if got.Degraded {
		t.Errorf("a clean turn must not be degraded: %q", got.Line)
	}
	if !strings.HasSuffix(got.Line, "CLEAR") {
		t.Errorf("line = %q, want it to end in CLEAR", got.Line)
	}
}

// A fresh session has run no chains yet. That is "unused", not "broken" — the
// zero ChainHealth must not paint every first turn red.
func TestGridStatusUnusedChainsAreClear(t *testing.T) {
	if got := evaluateGridStatus(gridSignals{}); got.Degraded {
		t.Errorf("an untouched session must report CLEAR, got %q", got.Line)
	}
}

func TestGridStatusDegradedOnSTTFallback(t *testing.T) {
	got := evaluateGridStatus(gridSignals{
		// The QA shape: the primary answered only after whisper-local refused.
		STT: speech.ChainHealth{
			Attempted: true, OK: true, Used: "groq", Failed: []string{"whisper-local"},
		},
	})

	if !got.Degraded {
		t.Fatalf("a chain that needed a fallback is degraded, got %q", got.Line)
	}
	if !strings.Contains(got.Line, "DEGRADED") {
		t.Errorf("line = %q, want DEGRADED", got.Line)
	}
	for _, want := range []string{"stt", "whisper-local", "groq"} {
		if !strings.Contains(got.Line, want) {
			t.Errorf("line = %q, want it to name %q", got.Line, want)
		}
	}
}

func TestGridStatusDegradedOnTotalChainFailure(t *testing.T) {
	got := evaluateGridStatus(gridSignals{
		STT: speech.ChainHealth{Attempted: true, Failed: []string{"groq", "whisper-local"}},
	})

	if !got.Degraded {
		t.Fatal("a chain where every provider failed must be degraded")
	}
	if !strings.Contains(got.Line, "chain failed") {
		t.Errorf("line = %q, want it to say the chain failed outright", got.Line)
	}
}

// The brief's headline case: a degraded STT chain plus offline mode reports both
// reasons on one line.
func TestGridStatusReportsEveryReasonOnOneLine(t *testing.T) {
	got := evaluateGridStatus(gridSignals{
		STT:      speech.ChainHealth{Attempted: true, OK: true, Used: "whisper-local", Failed: []string{"groq"}},
		Offline:  true,
		LocalLLM: true,
	})

	if !got.Degraded {
		t.Fatal("expected DEGRADED")
	}
	if strings.Contains(got.Line, "\n") {
		t.Errorf("the status must stay one line, got:\n%s", got.Line)
	}
	for _, want := range []string{"brain: local model", "offline mode", "stt fallback"} {
		if !strings.Contains(got.Line, want) {
			t.Errorf("line = %q, want it to mention %q", got.Line, want)
		}
	}
}

// The prefix is the branding — the change is the verdict, not the layout.
func TestGridStatusKeepsThePrefix(t *testing.T) {
	for _, sig := range []gridSignals{{}, {Offline: true}} {
		if got := evaluateGridStatus(sig); !strings.HasPrefix(got.Line, gridStatusPrefix) {
			t.Errorf("line = %q, want the %q prefix", got.Line, gridStatusPrefix)
		}
	}
}

func TestChainHealthReasonIsEmptyWhenClean(t *testing.T) {
	cases := map[string]speech.ChainHealth{
		"never ran":     {},
		"clean primary": {Attempted: true, OK: true, Used: "groq"},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			if h.Degraded() {
				t.Errorf("%v must not be degraded", h)
			}
			if r := h.Reason(); r != "" {
				t.Errorf("reason = %q, want empty", r)
			}
		})
	}
}
