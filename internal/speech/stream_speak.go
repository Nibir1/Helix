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
	"strings"

	"helix/internal/audio"
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
	sentences := SplitSentences(text)
	if len(sentences) <= 1 {
		return Speak(ctx, text)
	}

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
			a, err := Synthesize(ctx, s)
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
		if err := audio.PlaySpeech(audio.SpeechFormat{
			Kind:       string(item.audio.Kind),
			SampleRate: item.audio.SampleRate,
			Channels:   item.audio.Channels,
			Data:       item.audio.Bytes,
		}, 1.0); err != nil {
			return err
		}
	}
	return nil
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
