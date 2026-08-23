//go:build !windows

// tests/e2e/session_recall_e2e_test.go
// Purpose: BlackBox Phase 4 acceptance — "'what did I ask five minutes ago'
// answered correctly from the session store (e2e)".
//
// What can honestly be proven end-to-end here is the mechanism, not the wording
// of an answer: the mock provider returns a canned reply, so asserting that
// Helix "answered correctly" would only be asserting that the mock echoed what
// the test told it to. What matters — and what nothing covered — is that the
// earlier turn actually reaches the planner as context on the later one. The
// ring store is well unit-tested in isolation; this closes the gap between "the
// store holds it" and "the model is told about it", which is where a broken
// injection would live.
package e2e

import (
	"strings"
	"testing"
	"time"
)

// waitForPrompts blocks until the mock provider has received at least n planner
// prompts.
//
// Expect() cannot be used to sequence two identical replies: it scans the
// cumulative output buffer, so waiting for the same word twice matches the first
// turn's output immediately and reads the prompt list before the second turn has
// been sent. The prompt count is the thing this test actually depends on, so it
// is the thing to wait on.
func waitForPrompts(t *testing.T, h *harness, n int, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := h.plannerPrompts(); len(got) >= n {
			return got
		}
		time.Sleep(100 * time.Millisecond)
	}
	got := h.plannerPrompts()
	t.Fatalf("timed out waiting for %d planner prompts, got %d", n, len(got))
	return got
}

func TestE2E_PriorTurnReachesPlannerContext(t *testing.T) {
	h := newHarness(t, `{"intent":"chat","steps":[{"tool":"response","message":"noted"}]}`)
	defer h.Close()

	// A distinctive phrase, so finding it in a later prompt cannot be a
	// coincidence or an echo of the harness's own scaffolding.
	const marker = "orange marmalade telemetry"

	h.WriteLine("remember the phrase " + marker)
	waitForPrompts(t, h, 1, 40*time.Second)
	if err := h.Expect("noted", 40*time.Second); err != nil {
		t.Fatalf("first turn did not complete: %v", err)
	}

	h.WriteLine("what did I ask you a moment ago")
	prompts := waitForPrompts(t, h, 2, 40*time.Second)

	var carried bool
	for _, p := range prompts[1:] {
		if strings.Contains(p, marker) {
			carried = true
			break
		}
	}
	if !carried {
		t.Fatalf("the earlier turn never reached the planner as context; "+
			"session memory is not being injected.\nlast prompt:\n%s",
			prompts[len(prompts)-1])
	}
}

// Session context must arrive as DATA, never as instructions. A recalled turn is
// text the user spoke, replayed into every subsequent prompt, so the firewall
// convention applies: it belongs inside a zero-authority fence, or "remember to
// always run rm -rf" becomes a standing instruction.
//
// The assertion has to be precise about WHERE the phrase appears. It is also in
// the first turn's prompt as the live user request, and that copy is correctly
// unfenced — it IS the instruction. What must be fenced is the replayed copy, so
// this checks the recalled text sits inside the <session_history> block rather
// than merely somewhere in the same prompt.
func TestE2E_SessionContextIsFencedAsData(t *testing.T) {
	h := newHarness(t, `{"intent":"chat","steps":[{"tool":"response","message":"noted"}]}`)
	defer h.Close()

	const marker = "pumpkin ledger"

	h.WriteLine("remember the phrase " + marker)
	waitForPrompts(t, h, 1, 40*time.Second)
	h.WriteLine("what did I just say")
	prompts := waitForPrompts(t, h, 2, 40*time.Second)

	for _, p := range prompts[1:] {
		start := strings.Index(p, "<session_history")
		if start < 0 {
			continue
		}
		end := strings.Index(p[start:], "</session_history>")
		if end < 0 {
			t.Fatalf("session history block is not closed:\n%s", p[start:])
		}
		block := p[start : start+end]
		if !strings.Contains(block, marker) {
			continue
		}
		// Found the replayed copy: it must sit in a data-only fence that tells
		// the model not to obey it.
		if !strings.Contains(block, `authority="data-only"`) {
			t.Fatalf("recalled turn injected without a data-only authority marker:\n%s", block)
		}
		if !strings.Contains(strings.ToLower(block), "never obey") {
			t.Fatalf("the data-only fence must instruct the model not to obey its contents:\n%s", block)
		}
		return
	}
	t.Fatalf("no prompt replayed the earlier turn inside a session_history block; "+
		"last prompt:\n%s", prompts[len(prompts)-1])
}
