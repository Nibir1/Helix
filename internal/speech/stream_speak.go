// internal/speech/stream_speak.go
// Purpose: Low-latency spoken output. A long reply is split into sentences
// and synthesized one-ahead: sentence N+1 is fetched from the TTS chain WHILE
// sentence N is playing, so time-to-first-audio is one short sentence's
// synthesis instead of the whole paragraph's. This is the single biggest
// perceived-latency win in the voice loop (the "JARVIS starts talking
// immediately" feel).
package speech

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"helix/internal/audio"
	"helix/internal/utils"
)

// maxSpokenSentence caps a single synthesis request so one runaway sentence
// can't blow past provider limits; longer runs are hard-split on this bound.
const maxSpokenSentence = 350

// SpeakStream synthesizes and plays text sentence-by-sentence with one-ahead
// pipelining. Playback order is preserved. It blocks until the whole reply
// has been spoken or ctx is cancelled (barge-in). Falls back to whole-text
// Speak when there's only one chunk.
func SpeakStream(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	// Barge-in v2 (P12.5): derive a cancellable context and publish it, so a
	// wake event, a keypress, or Ctrl+C can stop the reply MID-SENTENCE rather
	// than at the next sentence boundary. Registering with the interrupt
	// manager here — rather than at each call site — means every caller
	// (interactive shell, daemon, ambient responses) gets it.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	unreg := utils.RegisterOperation(cancel)
	defer unreg()
	release := beginSpeaking(cancel)
	defer release()

	// Record the assistant's half of the conversational context ONCE per reply,
	// before synthesis, so the next turn's context includes what was just said.
	//
	// Per reply rather than per sentence: this path pipelines sentences, and
	// appending each one would fill a four-turn context with fragments of a
	// single answer. Text-only by design — the reply is synthesized in pieces and
	// there is no one clip to retain, and CSM conditions on text as well as
	// audio, so the transcript is the part worth keeping whole.
	RecordAssistantTurn(text, AudioFormat{})

	sentences := SplitSentences(text)
	if len(sentences) <= 1 {
		return speakOnce(ctx, text)
	}

	// P7.2c: the FIRST sentence is streamed, the rest are pipelined.
	//
	// Time-to-first-audio is set entirely by sentence 1 — nothing is playing to
	// hide its synthesis behind. Streaming it drops that from a full synthesis
	// (~2.3s measured) to the ~150ms preroll. Sentences 2..N stay buffered
	// because their latency is ALREADY hidden: they are fetched while an
	// earlier sentence plays, so streaming them would add complexity for no
	// perceptible gain.
	if err := speakOnce(ctx, sentences[0]); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	sentences = sentences[1:]

	// Producer: synthesize sentences in order into a bounded channel so at
	// most one sentence is buffered ahead of playback (memory-bounded, and
	// keeps synthesis close to the current playback position for barge-in).
	type synth struct {
		audio AudioFormat
		err   error
	}
	out := make(chan synth, 1)
	go func() {
		defer close(out)
		for _, s := range sentences {
			if ctx.Err() != nil {
				return
			}
			// synthesizeChain, not Synthesize: these sentences play BEHIND
			// sentence 1, so their latency is already hidden and must not
			// overwrite the reply's time-to-first-audio metric (which sentence 1
			// owns, streamed or buffered).
			a, err := synthesizeChain(ctx, s)
			select {
			case out <- synth{a, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	// Consumer: play each synthesized sentence in order.
	for item := range out {
		if item.err != nil {
			return item.err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// PlaySpeechContext stops within one audio buffer (~50ms) when the
		// context is cancelled, so a barge-in cuts the current sentence
		// instead of waiting for it to finish (P12.5).
		if err := audio.PlaySpeechContext(ctx, audio.SpeechFormat{
			Kind:       string(item.audio.Kind),
			SampleRate: item.audio.SampleRate,
			Channels:   item.audio.Channels,
			Data:       item.audio.Bytes,
		}, 1.0); err != nil {
			return err
		}
		// The speaker is idle right here and nowhere else, which is the only
		// moment a microphone can be trusted without echo cancellation. Opt-in;
		// a no-op and zero added latency when disabled.
		if BargeInProbe(ctx) {
			cancel()
			return context.Canceled
		}
	}
	// Report interruption rather than nil: the producer exits silently on a
	// cancelled context (closing the channel with nothing sent), so without
	// this a barge-in — or a reply cancelled before its first word — would be
	// indistinguishable from a reply that was spoken in full.
	return ctx.Err()
}

// bargeInEnabled gates voice interruption of a spoken reply.
//
// OFF by default, and that default is a judgement rather than caution. The
// detector below is honest but partial (see BargeInProbe), and a false trigger
// is worse than the problem it solves: Helix cutting itself off because a chair
// scraped is more disruptive than finishing a sentence you did not want.
var bargeInEnabled atomic.Bool

// EnableBargeIn turns sentence-boundary voice interruption on or off.
func EnableBargeIn(on bool) { bargeInEnabled.Store(on) }

// BargeInEnabled reports the current setting, for status output.
func BargeInEnabled() bool { return bargeInEnabled.Load() }

// Barge-in probe geometry.
const (
	// bargeSettleDelay lets the speaker cone, the desk and the microphone's AGC
	// stop ringing before the probe listens. Same physics as micSettleDelay in
	// the voice loop: without it the probe hears the tail of the sentence Helix
	// just finished and interrupts itself on its own voice — the exact failure
	// the chime/"you" bug was.
	bargeSettleDelay = 150 * time.Millisecond

	// bargeWindow is how long the probe listens. Long enough to catch the onset
	// of a word, short enough that the two together stay inside the pause a
	// listener already expects between sentences (~300-500ms in ordinary
	// speech), so the gap reads as prosody rather than as a stall.
	bargeWindow = 250 * time.Millisecond

	// bargeRMSFloor is deliberately well above speechRMSFloor. This probe runs
	// with no transcription behind it, so a false positive silences Helix with
	// no way to tell it was wrong. Requiring a clearly-audible level trades
	// some sensitivity — you may have to speak up to interrupt — for not being
	// cut off by a room.
	bargeRMSFloor = 0.02
)

// BargeInProbe listens in the silence BETWEEN sentences and reports whether
// someone started talking.
//
// This is what makes voice interruption possible without acoustic echo
// cancellation, which is the reason it was parked: with the speaker playing,
// the microphone hears Helix, and separating the two needs AEC — which needs
// CGO, which the default build does not have (guardrail #8). In the gap the
// speaker is idle, so there is nothing to cancel and an ordinary energy gate is
// enough.
//
// What this genuinely does NOT do, stated plainly because the difference
// matters: you cannot cut in mid-sentence. The probe runs at sentence
// boundaries, so a long sentence plays to its end before Helix can notice you.
// It is interruption at the pace of punctuation, not full duplex.
//
// Returns false on any error: a probe that cannot listen must not manufacture
// an interrupt.
func BargeInProbe(ctx context.Context) bool {
	if !bargeInEnabled.Load() {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(bargeSettleDelay):
	}

	probeCtx, cancel := context.WithTimeout(ctx, bargeWindow+2*time.Second)
	defer cancel()

	clip, err := RecordClip(probeCtx, CaptureOptions{
		MaxDuration: bargeWindow,
		// No silence gating: the whole point is to sample a window that is
		// EXPECTED to be quiet and measure it. With gating, a quiet window
		// returns an error instead of a measurable clip.
		NoSilenceStop: true,
	})
	if err != nil {
		return false
	}
	return ClipRMS(clip) >= bargeRMSFloor
}

// speakingState tracks the in-flight spoken reply so an external trigger can
// interrupt it. Only one reply is ever spoken at a time (the speaker is a
// single owned device, ADR-007), so a single cancel handle is sufficient.
var (
	speakingMu     sync.Mutex
	speakingCancel context.CancelFunc
)

// beginSpeaking publishes the cancel handle and returns its release func.
func beginSpeaking(cancel context.CancelFunc) func() {
	speakingMu.Lock()
	speakingCancel = cancel
	speakingMu.Unlock()
	return func() {
		speakingMu.Lock()
		// Clear only if still ours: a later reply may have taken over.
		if speakingCancel != nil {
			speakingCancel = nil
		}
		speakingMu.Unlock()
	}
}

// Speaking reports whether a spoken reply is currently in flight.
func Speaking() bool {
	speakingMu.Lock()
	defer speakingMu.Unlock()
	return speakingCancel != nil
}

// StopSpeaking interrupts the in-flight spoken reply (barge-in v2, P12.5).
//
// It is safe to call when nothing is speaking. Playback stops within about one
// audio buffer; the speaking call returns context.Canceled.
//
// Callers: the interrupt manager (Ctrl+C), and — once the half-duplex
// constraint is lifted — a wake event detected during playback.
func StopSpeaking() {
	speakingMu.Lock()
	cancel := speakingCancel
	speakingMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// speakOnce synthesizes and plays a single chunk under ctx. It is the
// cancellable twin of Speak, used inside SpeakStream so the single-sentence
// path is interruptible too.
//
// P7.2c: it tries STREAMED playback first. The buffered path waits for the
// entire synthesis before the first sample reaches the speaker — measured at
// 2,280 ms against an 800 ms budget on a real OpenAI round trip — whereas
// streaming starts after a ~150 ms preroll. Any failure BEFORE audio plays
// falls back to the buffered path, so this can only be faster, never a new way
// to be silent.
func speakOnce(ctx context.Context, text string) error {
	if err := speakOnceStreamed(ctx, text); err == nil {
		return nil
	} else if ctx.Err() != nil {
		// A barge-in is not a streaming failure; do not re-synthesize.
		return err
	}

	f, err := Synthesize(ctx, text)
	if err != nil {
		return err
	}
	return audio.PlaySpeechContext(ctx, audio.SpeechFormat{
		Kind:       string(f.Kind),
		SampleRate: f.SampleRate,
		Channels:   f.Channels,
		Data:       f.Bytes,
	}, 1.0)
}

// speakOnceStreamed plays one chunk via the streaming path, recording true
// TIME-TO-FIRST-AUDIO — the number the first_byte_ms budget is about.
//
// Timing the whole call would measure the entire utterance instead, so a longer
// reply would report a worse latency even though the user heard the first word
// just as quickly. The audio layer signals the first-audio instant explicitly.
func speakOnceStreamed(ctx context.Context, text string) error {
	reg := Default()
	if reg == nil {
		return errors.New("speech not initialized")
	}

	start := time.Now()
	stream, _, err := reg.SynthesizeStream(ctx, text, SynthesisOptions{
		Voice:   reg.ActiveConfig().TTS.Voice,
		Context: currentContext(),
	})
	if err != nil {
		return err
	}

	return audio.PlaySpeechStream(ctx, audio.StreamFormat{
		SampleRate: stream.SampleRate,
		Channels:   stream.Channels,
	}, stream.Body, audio.StreamPlayback{
		OnFirstAudio: func() {
			lastSynthMs.Store(time.Since(start).Milliseconds())
			lastSpeechStreamed.Store(true)
		},
	})
}

// SplitSentences breaks text into speakable chunks on sentence terminators,
// keeping each chunk under maxSpokenSentence characters. It is deliberately
// simple (no NLP): terminator + following space starts a new chunk; a chunk
// that grows too long is split on the last space before the cap.
func SplitSentences(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var chunks []string
	var cur strings.Builder
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			chunks = append(chunks, s)
		}
		cur.Reset()
	}

	runes := []rune(text)
	for i, r := range runes {
		cur.WriteRune(r)
		isTerm := r == '.' || r == '!' || r == '?' || r == '\n'
		nextIsSpace := i+1 >= len(runes) || runes[i+1] == ' ' || runes[i+1] == '\n'
		if isTerm && nextIsSpace {
			flush()
			continue
		}
		if cur.Len() >= maxSpokenSentence && r == ' ' {
			flush()
		}
	}
	flush()
	return chunks
}
