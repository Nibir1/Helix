// internal/speech/conversation.go
// Purpose: the conversational context CSM-1B is conditioned on — recent turns as
// (speaker, text, audio) — held in memory and nowhere else.
//
// WHY THIS EXISTS. CSM's distinguishing capability is not its voice, it is that
// its prosody is conditioned on how the conversation has gone: the model card
// says it "sounds best when provided with context", and the reference API takes
// prior turns as Segment(text, speaker, audio). Synthesizing each sentence in
// isolation — which is what every ordinary TTS provider does, and what Helix did
// before this — throws that away and leaves CSM sounding like a very good
// single-shot voice rather than a participant in a conversation.
//
// THE PRIVACY PROBLEM, AND THE DECISION. Context needs AUDIO of prior turns, and
// Helix's standing guarantee is that captured audio is deleted the moment it is
// read (P1.7) and never written to disk (guardrail §12 #6, threat V5). Retaining
// audio at all is therefore a real change and it is made deliberately, with the
// guarantee that actually matters left intact:
//
//   - MEMORY ONLY. Nothing here touches the filesystem. The "never written to
//     disk" promise is unchanged, and there is nothing new for /purge to wipe
//     because there is nothing new on disk.
//   - BOUNDED. A few turns and a few seconds, evicted oldest-first. This is not
//     a recording of your day; it is the tail of the current exchange.
//   - SCOPED TO THE MODE. Clear() runs when live mode ends, so leaving voice
//     mode drops the audio rather than leaving it resident.
//   - OFF UNLESS ASKED. The store is only populated when a context-capable TTS
//     provider is configured with a non-zero turn budget.
//
// The audio held here was already in memory a moment earlier — it is the clip
// STT just transcribed and the reply the speaker just played. What changes is how
// long it lives, which is why the bounds above are the design rather than a
// detail.
package speech

import "sync"

// Speaker ids follow CSM's convention: the assistant is 0 and the user is 1.
// They are conditioning tokens, not voice selections — CSM was trained on
// multi-speaker conversation with the speaker encoded in the text stream.
const (
	SpeakerAssistant = 0
	SpeakerUser      = 1
)

// ConversationTurn is one segment of context: who spoke, what was said, and how
// it sounded.
//
// Audio may be empty. A turn with text but no audio is still useful — it tells
// the model what was said — and is what happens when a reply was printed but not
// spoken because TTS was off.
type ConversationTurn struct {
	Speaker int
	Text    string
	Audio   AudioFormat
}

// ConversationContext is a bounded, in-memory ring of recent turns.
//
// The zero value is unusable on purpose: NewConversationContext takes explicit
// bounds so no caller can accidentally create an unbounded one.
type ConversationContext struct {
	mu    sync.Mutex
	turns []ConversationTurn

	maxTurns int
	maxBytes int
}

// Context defaults. Small on purpose.
//
// CSM was trained with a 2048-token sequence (~2 minutes of audio), so more
// context is not free: every turn is base64-encoded into the synthesis request,
// and 10 seconds of 24kHz mono WAV is already ~640 KB before encoding. Four
// turns is enough for the model to hear the shape of the exchange; more mostly
// buys latency.
const (
	DefaultContextTurns = 4
	DefaultContextBytes = 4 << 20 // 4 MiB of retained audio, total
)

// NewConversationContext builds a bounded store. Non-positive bounds fall back
// to the defaults rather than becoming unbounded.
func NewConversationContext(maxTurns, maxBytes int) *ConversationContext {
	if maxTurns <= 0 {
		maxTurns = DefaultContextTurns
	}
	if maxBytes <= 0 {
		maxBytes = DefaultContextBytes
	}
	return &ConversationContext{maxTurns: maxTurns, maxBytes: maxBytes}
}

// Append records a turn and evicts until both bounds hold.
//
// A nil receiver is a no-op so call sites in the voice loop need no guard: when
// context is disabled the store simply does not exist.
func (c *ConversationContext) Append(t ConversationTurn) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.turns = append(c.turns, t)
	c.evictLocked()
}

// evictLocked drops oldest turns until the count and byte budgets are met.
//
// Byte eviction runs after count eviction because a single long turn can exceed
// the budget on its own; dropping to one turn and stopping is correct there —
// the alternative is refusing to remember anything at all.
func (c *ConversationContext) evictLocked() {
	if len(c.turns) > c.maxTurns {
		c.turns = append([]ConversationTurn(nil), c.turns[len(c.turns)-c.maxTurns:]...)
	}
	for len(c.turns) > 1 && c.bytesLocked() > c.maxBytes {
		c.turns = append([]ConversationTurn(nil), c.turns[1:]...)
	}
}

// bytesLocked totals retained audio.
func (c *ConversationContext) bytesLocked() int {
	total := 0
	for _, t := range c.turns {
		total += len(t.Audio.Bytes)
	}
	return total
}

// Recent returns up to n turns, oldest first, as a copy.
//
// A copy because the caller serializes these into a request while the voice loop
// may be appending the next turn — handing out the backing slice would be a data
// race in the one place where the audio is largest.
func (c *ConversationContext) Recent(n int) []ConversationTurn {
	if c == nil || n <= 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	start := 0
	if len(c.turns) > n {
		start = len(c.turns) - n
	}
	return append([]ConversationTurn(nil), c.turns[start:]...)
}

// Len reports how many turns are retained.
func (c *ConversationContext) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.turns)
}

// Bytes reports how much audio is retained, for status reporting.
func (c *ConversationContext) Bytes() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytesLocked()
}

// Clear drops everything. Called when live mode ends.
func (c *ConversationContext) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.turns = nil
}
