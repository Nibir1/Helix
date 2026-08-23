// internal/agent/clarification_test.go
// Purpose: Phase 2's last carry-over — "full multi-turn clarification (answer
// re-enters planner with turn context) → needs Phase 4 session memory".
//
// Phase 4's session memory delivered the mechanism and nobody closed the item:
// a clarifying question is captured as the turn's reply, so the user's answer
// reaches the planner with the question already in context. What was missing was
// the confidence-gate path, which asked "could you repeat it?" without recording
// that it had asked, and stored the rejected transcript as if it were ordinary
// user speech.
package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"helix/internal/input"
	"helix/internal/session"
)

// newTempStore builds a real RingStore in a temp directory.
//
// A fake would be neater, but Agent.Session is the concrete *RingStore rather
// than the session.Store interface — and widening that interface (it would need
// Len, Capacity and Restore) purely to enable a fake is not worth reshaping
// production code for. The real store persists to a temp path, which also means
// these tests exercise the actual append-and-load path.
func newTempStore(t *testing.T) *session.RingStore {
	t.Helper()
	store, err := session.NewRingStoreAt(filepath.Join(t.TempDir(), "session.json"), 20)
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	return store
}

// A low-confidence transcript must produce a turn that records BOTH halves of
// the exchange: what was (mis)heard, and that Helix asked for a repeat.
func TestConfidenceGateRecordsItsClarification(t *testing.T) {
	store := newTempStore(t)
	var spoken []string

	a := &Agent{
		render:  HeadlessRenderer{},
		Session: store,
		OnSpeak: func(s string) { spoken = append(spoken, s) },
	}

	a.HandleInputEvent(input.InputEvent{
		Text:    "wet mist a bee ledger",
		Channel: input.ChannelVoice,
		Meta:    map[string]any{"stt_confidence": 0.20},
	})

	if len(spoken) != 1 || !strings.Contains(strings.ToLower(spoken[0]), "repeat") {
		t.Fatalf("gate must speak a request to repeat, got %v", spoken)
	}

	turns := store.Recent(store.Len())
	if len(turns) != 1 {
		t.Fatalf("expected exactly 1 recorded turn, got %d: %+v", len(turns), turns)
	}
	got := turns[0]

	if got.Reply == "" {
		t.Error("the clarification question must be recorded as the turn's reply — " +
			"without it the user's repeat reaches the planner with no sign that " +
			"Helix asked for one")
	}
	if !strings.Contains(strings.ToLower(got.Reply), "repeat") {
		t.Errorf("recorded reply should be the request to repeat, got %q", got.Reply)
	}
	if !got.Unreliable {
		t.Error("a transcript the policy refused to act on must be marked unreliable, " +
			"or it becomes authoritative context for the next twenty turns")
	}
	if got.UserText != "wet mist a bee ledger" {
		t.Errorf("the transcript should still be recorded verbatim, got %q", got.UserText)
	}
}

// The marker has to be visible in the text the planner reads. A flag that never
// reaches the prompt changes nothing about the model's behavior.
func TestUnreliableTurnIsLabelledInPlannerContext(t *testing.T) {
	store := newTempStore(t)
	store.Append(session.Turn{
		Channel: "voice", UserText: "wet mist a bee ledger",
		Reply: "I did not catch that clearly. Could you repeat it?", Unreliable: true,
	})
	store.Append(session.Turn{
		Channel: "voice", UserText: "what is my git status", Reply: "Clean.",
	})

	a := &Agent{Session: store}
	block := a.sessionContextBlock()

	if !strings.Contains(block, "not understood") {
		t.Fatalf("unreliable turn must be labelled in the prompt:\n%s", block)
	}
	// The trustworthy turn must NOT be labelled, or the marker means nothing.
	for _, line := range strings.Split(block, "\n") {
		if strings.Contains(line, "what is my git status") && strings.Contains(line, "not understood") {
			t.Fatalf("a normal turn must not be marked unreliable: %q", line)
		}
	}
	// And the fence still has to be intact — this is data, not instructions.
	if !strings.Contains(block, `authority="data-only"`) {
		t.Errorf("session context lost its data-only fence:\n%s", block)
	}
}

// A confident turn is recorded normally: the gate must not mark everything, or
// the distinction is worthless.
func TestConfidentTranscriptIsNotMarkedUnreliable(t *testing.T) {
	store := newTempStore(t)
	a := &Agent{
		render:  HeadlessRenderer{},
		Session: store,
		// No planner is wired, so the turn will fail past classification; what
		// matters here is only how it was RECORDED.
		OnSpeak: func(string) {},
	}

	a.HandleInputEvent(input.InputEvent{
		Text:    "undo that",
		Channel: input.ChannelVoice,
		Meta:    map[string]any{"stt_confidence": 0.95},
	})

	turns := store.Recent(store.Len())
	if len(turns) != 1 {
		t.Fatalf("expected 1 recorded turn, got %d", len(turns))
	}
	if turns[0].Unreliable {
		t.Error("a transcript above the confidence gate must not be marked unreliable")
	}
}

// The flag must reset between turns, or one misheard utterance would taint every
// later turn in the session.
func TestUnreliableFlagResetsBetweenTurns(t *testing.T) {
	store := newTempStore(t)
	a := &Agent{
		render:  HeadlessRenderer{},
		Session: store,
		OnSpeak: func(string) {},
	}

	a.HandleInputEvent(input.InputEvent{
		Text: "garbled", Channel: input.ChannelVoice,
		Meta: map[string]any{"stt_confidence": 0.1},
	})
	a.HandleInputEvent(input.InputEvent{
		Text: "undo that", Channel: input.ChannelVoice,
		Meta: map[string]any{"stt_confidence": 0.99},
	})

	turns := store.Recent(store.Len())
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if !turns[0].Unreliable {
		t.Error("first (garbled) turn should be unreliable")
	}
	if turns[1].Unreliable {
		t.Error("unreliable must not leak into the following turn")
	}
}
