// internal/speech/tts_pin.go
//
// Purpose: one reply, one voice.
//
// A spoken reply is synthesized SENTENCE BY SENTENCE so the first words start
// playing while the rest are still being made. Each sentence was resolved
// against the provider chain independently, with no memory of what spoke the
// last one — so a primary that failed on sentence three handed that sentence to
// the fallback, and the user heard the voice change halfway through an answer.
// Reported from a live session: "first two sentences speaks someone and then
// the rest two sentence speaks someone else."
//
// It also made the reply slow in exactly the case where it was already
// degraded. Nothing remembered the failure, so sentences four, five and six
// each paid the failing provider's full timeout again before falling back.
//
// The pin fixes both by holding one decision for the length of an utterance:
// whichever provider speaks the first sentence speaks the rest, and a provider
// that has already failed this utterance is not asked again. At most one voice
// change per reply, and no repeated waiting on something known to be down.
package speech

import (
	"context"
	"sync"
)

type ttsPinKey struct{}

// ttsPin is the per-utterance decision about who is speaking.
type ttsPin struct {
	mu      sync.Mutex
	name    string          // provider that spoke the last sentence
	retired map[string]bool // providers that failed during this utterance
}

// WithUtterance scopes a TTS pin to one reply.
//
// Called once where a reply begins, not per sentence — the whole point is that
// the decision outlives the sentence that made it.
func WithUtterance(ctx context.Context) context.Context {
	if ctx.Value(ttsPinKey{}) != nil {
		return ctx // already scoped; nesting must not reset the choice
	}
	return context.WithValue(ctx, ttsPinKey{}, &ttsPin{retired: map[string]bool{}})
}

func pinFrom(ctx context.Context) *ttsPin {
	p, _ := ctx.Value(ttsPinKey{}).(*ttsPin)
	return p
}

// chainFor returns the provider order to try for one sentence.
//
// With no pin (a one-off /blackbox say, a test) it is the configured chain,
// unchanged. Within an utterance it is the chain with two adjustments:
//
//   - providers that already failed THIS utterance are dropped, so a dead
//     primary is not re-tried once per sentence; and
//   - the provider that spoke the last sentence comes first, so the voice does
//     not switch back if the primary recovers mid-reply.
//
// Both rules only ever REMOVE work or REORDER it. A sentence that could have
// been spoken by some provider still can be — the pin cannot make a reply fail
// that would otherwise have succeeded, which is the property that makes it safe
// to apply to a path with a fallback.
func (r *Registry) chainFor(ctx context.Context) []string {
	chain := r.TTSChain()
	p := pinFrom(ctx)
	if p == nil {
		return chain
	}

	p.mu.Lock()
	current, retired := p.name, p.retired
	p.mu.Unlock()

	out := make([]string, 0, len(chain))
	// The incumbent first, if it is still in the chain.
	if current != "" && contains(chain, current) && !retired[current] {
		out = append(out, current)
	}
	for _, name := range chain {
		if name == current || retired[name] {
			continue
		}
		out = append(out, name)
	}
	// Never hand back nothing: if every provider has been retired, fall back to
	// the configured chain and let the caller fail with a real error rather
	// than with "no TTS provider configured", which would be a lie.
	if len(out) == 0 {
		return chain
	}
	return out
}

// spokeWith records who just succeeded.
func spokeWith(ctx context.Context, name string) {
	p := pinFrom(ctx)
	if p == nil {
		return
	}
	p.mu.Lock()
	p.name = name
	p.mu.Unlock()
}

// retire records a provider that failed during this utterance, so the remaining
// sentences do not wait on it again.
func retire(ctx context.Context, name string) {
	p := pinFrom(ctx)
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.retired == nil {
		p.retired = map[string]bool{}
	}
	p.retired[name] = true
	if p.name == name {
		p.name = "" // the incumbent is gone; the next success sets a new one
	}
	p.mu.Unlock()
}
