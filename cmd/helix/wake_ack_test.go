// cmd/helix/wake_ack_test.go
// Purpose: the two properties the spoken wake acknowledgement has to hold.
package main

import (
	"strings"
	"testing"
)

// TestWakeAcknowledgementNeverRepeatsBackToBack is the reason the picker is not
// a bare rand.IntN: hearing the same four words twice running reads as a
// stutter or a double trigger, which is the doubt the feature exists to remove.
func TestWakeAcknowledgementNeverRepeatsBackToBack(t *testing.T) {
	prev := pickWakeAcknowledgement()
	if prev == "" {
		t.Fatal("the acknowledgement set is empty; a voice wake would be silent")
	}
	seen := map[string]bool{prev: true}
	for i := 0; i < 500; i++ {
		next := pickWakeAcknowledgement()
		if next == "" {
			t.Fatalf("pick %d returned nothing", i)
		}
		if next == prev {
			t.Fatalf("pick %d repeated %q back to back", i, next)
		}
		seen[next] = true
		prev = next
	}
	// 500 draws over this set should reach all of it; a line that can never be
	// chosen is a line that is not really in the vocabulary.
	if len(seen) != len(wakeAcknowledgements) {
		t.Errorf("only %d of %d acknowledgements were ever picked",
			len(seen), len(wakeAcknowledgements))
	}
}

// TestWakeAcknowledgementsAreShort guards the latency this speech sits in: it
// plays between the user deciding to talk and the recorder opening, so a long
// line is a pause the user waits through before they may speak.
func TestWakeAcknowledgementsAreShort(t *testing.T) {
	for _, line := range wakeAcknowledgements {
		if strings.TrimSpace(line) == "" {
			t.Error("an empty acknowledgement would wake silently")
			continue
		}
		if n := len(strings.Fields(line)); n > 4 {
			t.Errorf("%q is %d words — the acknowledgement becomes the wait", line, n)
		}
		// The energy engine does not know what was said, so nothing here may
		// claim it did. See the file comment.
		if strings.Contains(strings.ToLower(line), "hey helix") {
			t.Errorf("%q names the wake phrase, which the default engine cannot hear", line)
		}
	}
}
