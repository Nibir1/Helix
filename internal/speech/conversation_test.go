// internal/speech/conversation_test.go
// Purpose: pin the bounds that make retaining conversation audio acceptable.
//
// The store exists to give CSM the context its prosody depends on; the reason it
// is allowed to hold audio at all is that it is bounded, memory-only and cleared
// with the mode. Those are the properties worth testing — a leak here is a
// privacy regression, not a performance one.
package speech

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func turnWithAudio(speaker int, text string, bytes int) ConversationTurn {
	return ConversationTurn{
		Speaker: speaker,
		Text:    text,
		Audio:   AudioFormat{Kind: KindWAV, SampleRate: 24000, Channels: 1, Bytes: make([]byte, bytes)},
	}
}

func TestConversationKeepsRecentTurnsOldestFirst(t *testing.T) {
	c := NewConversationContext(3, 0)
	for _, s := range []string{"one", "two", "three", "four"} {
		c.Append(ConversationTurn{Speaker: SpeakerUser, Text: s})
	}

	got := c.Recent(10)
	if len(got) != 3 {
		t.Fatalf("expected the 3-turn bound to hold, got %d", len(got))
	}
	if got[0].Text != "two" || got[2].Text != "four" {
		t.Fatalf("expected oldest-first [two three four], got %v", []string{
			got[0].Text, got[1].Text, got[2].Text})
	}
}

// The byte budget is the bound that actually protects memory: turn count says
// nothing when one turn is a 30-second utterance.
func TestConversationEvictsOnByteBudget(t *testing.T) {
	c := NewConversationContext(10, 1000)
	for i := 0; i < 5; i++ {
		c.Append(turnWithAudio(SpeakerUser, "turn", 400))
	}
	if got := c.Bytes(); got > 1000 {
		t.Fatalf("retained %d bytes, budget is 1000", got)
	}
	if c.Len() == 0 {
		t.Fatal("eviction must not empty the store entirely")
	}
}

// A single turn larger than the whole budget must still be kept: dropping to
// nothing would mean a long sentence erases all context, which is worse than
// briefly exceeding a soft cap.
func TestConversationKeepsOneOversizeTurn(t *testing.T) {
	c := NewConversationContext(4, 100)
	c.Append(turnWithAudio(SpeakerUser, "very long", 5000))
	if c.Len() != 1 {
		t.Fatalf("an oversize turn should still be retained, got %d turns", c.Len())
	}
}

// Recent must hand back a copy: the caller base64-encodes these into a request
// while the voice loop may be appending the next turn.
func TestConversationRecentReturnsACopy(t *testing.T) {
	c := NewConversationContext(4, 0)
	c.Append(ConversationTurn{Speaker: SpeakerUser, Text: "original"})

	got := c.Recent(1)
	got[0].Text = "mutated"

	if again := c.Recent(1); again[0].Text != "original" {
		t.Fatal("Recent handed out the backing array; a caller mutated the store")
	}
}

// Leaving live mode must drop the audio rather than leaving it resident.
func TestConversationClearDropsEverything(t *testing.T) {
	c := NewConversationContext(4, 0)
	c.Append(turnWithAudio(SpeakerUser, "something", 2048))
	c.Clear()

	if c.Len() != 0 || c.Bytes() != 0 {
		t.Fatalf("Clear must drop turns and audio, got %d turns / %d bytes",
			c.Len(), c.Bytes())
	}
}

// A nil store is the disabled state and every method must tolerate it, so the
// voice loop can record unconditionally instead of guarding at each call site —
// a forgotten guard is how an opt-out leaks.
func TestNilConversationIsSafe(t *testing.T) {
	var c *ConversationContext
	c.Append(turnWithAudio(SpeakerUser, "ignored", 128)) // must not panic
	c.Clear()
	if c.Len() != 0 || c.Bytes() != 0 || c.Recent(5) != nil {
		t.Fatal("a nil store must behave as empty")
	}
}

// Bounds must never be unbounded, however a caller constructs the store.
func TestConversationRejectsUnboundedConstruction(t *testing.T) {
	for _, c := range []*ConversationContext{
		NewConversationContext(0, 0),
		NewConversationContext(-1, -1),
	} {
		for i := 0; i < DefaultContextTurns*3; i++ {
			c.Append(ConversationTurn{Speaker: SpeakerUser, Text: "x"})
		}
		if c.Len() > DefaultContextTurns {
			t.Fatalf("non-positive bounds must fall back to the defaults, got %d turns", c.Len())
		}
	}
}

// THE PRIVACY CONTRACT. This file may not touch the filesystem: retaining audio
// is only acceptable because it stays in memory, and "never written to disk"
// (guardrail §12 #6, threat V5) is unchanged by this feature.
func TestConversationNeverTouchesDisk(t *testing.T) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, filepath.Join(".", "conversation.go"), nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse conversation.go: %v", err)
	}
	for _, imp := range af.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		switch {
		case p == "os", p == "io/ioutil", strings.HasPrefix(p, "os/"),
			strings.HasPrefix(p, "path/"), p == "net", strings.HasPrefix(p, "net/"):
			t.Fatalf("conversation context must stay in memory and off the network, "+
				"but imports %q", p)
		}
	}
}

// The package-level entry points are what the voice loop calls, so they carry
// the guarantees that matter: off by default, populated when enabled, dropped on
// disable.
func TestConversationEntryPointsRespectTheToggle(t *testing.T) {
	t.Cleanup(func() { EnableConversationContext(0, 0) })

	// Off by default: recording must be a no-op, not a silent accumulation.
	EnableConversationContext(0, 0)
	RecordUserTurn("ignored", AudioFormat{})
	RecordAssistantTurn("also ignored", AudioFormat{})
	if turns, bytes := ConversationStats(); turns != 0 || bytes != 0 {
		t.Fatalf("context must stay empty while disabled, got %d turns / %d bytes", turns, bytes)
	}
	if currentContext() != nil {
		t.Fatal("disabled context must contribute nothing to a request")
	}

	// Enabled: both speakers land, oldest first.
	EnableConversationContext(4, 0)
	RecordUserTurn("did the build pass", AudioFormat{
		Kind: KindWAV, SampleRate: 24000, Channels: 1, Bytes: make([]byte, 512)})
	RecordAssistantTurn("two tests failed", AudioFormat{})

	got := currentContext()
	if len(got) != 2 {
		t.Fatalf("expected 2 retained turns, got %d", len(got))
	}
	if got[0].Speaker != SpeakerUser || got[1].Speaker != SpeakerAssistant {
		t.Errorf("speakers wrong: %d then %d", got[0].Speaker, got[1].Speaker)
	}
	if got[0].Text != "did the build pass" {
		t.Errorf("oldest turn should come first, got %q", got[0].Text)
	}

	// Disabling drops it — this is what leaving live mode does.
	EnableConversationContext(0, 0)
	if turns, bytes := ConversationStats(); turns != 0 || bytes != 0 {
		t.Fatalf("disabling must drop retained audio, got %d turns / %d bytes", turns, bytes)
	}
}
