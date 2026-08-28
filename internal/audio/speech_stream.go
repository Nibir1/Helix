// internal/audio/speech_stream.go
//
// Purpose: incremental speech playback (BlackBox P7.2c).
//
// PlaySpeech buffers the ENTIRE synthesis before a single sample reaches the
// speaker, so time-to-first-audio equals the provider's whole generation time —
// measured at 2,280 ms against an 800 ms budget on a real OpenAI round trip.
// This file plays audio as it arrives instead, cutting first-audio to roughly
// the pre-buffer.
//
// Two constraints shape the design:
//
//  1. **The speaker callback must never block.** beep pulls samples from a
//     mixer goroutine; a Stream() that waits on the network would stall the
//     whole audio device. So a producer goroutine decodes into a queue and
//     Stream() only ever takes what is already there.
//
//  2. **Underruns are worse than latency.** Starting playback the instant the
//     first byte lands guarantees gaps whenever the network hiccups. A short
//     pre-buffer (prerollBytes) is filled before playback begins, which costs a
//     fixed ~150 ms and removes almost all of them. If the stream still runs
//     dry mid-utterance the streamer emits silence rather than ending, so a
//     stall sounds like a pause and not a truncated sentence.
package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/gopxl/beep/v2"
)

// StreamFormat describes a raw PCM stream (16-bit signed little-endian).
// Headerless by construction: the streaming path requests raw PCM precisely so
// there is no container to parse incrementally.
type StreamFormat struct {
	SampleRate int
	Channels   int
}

// preroll is how much audio is buffered before playback starts — the entire
// latency cost of streaming, against ~2,280 ms for the buffered path.
//
// 250 ms rather than a tighter number: an underrun is now padded with silence
// (see Stream), which is audible as a gap, so a little extra lead is cheaper
// than a stutter. Still an order of magnitude better than waiting for the whole
// synthesis.
const prerollMillis = 250

// maxStreamSeconds bounds a hostile or stuck stream, matching the buffered
// path's maxSpeechSeconds guarantee.
const maxStreamSeconds = maxSpeechSeconds

// StreamPlayback carries optional playback settings.
type StreamPlayback struct {
	// Volume is 0..1 gain (outside the range, or zero, means full).
	Volume float64

	// OnFirstAudio fires once, at the instant playback is about to begin —
	// i.e. the preroll is filled and the stream is being handed to the speaker.
	//
	// It exists because time-to-first-audio is the number that matters and the
	// only one the first_byte_ms budget is about. Measuring around the whole
	// PlaySpeechStream call would time the entire UTTERANCE instead, reporting
	// a worse figure the longer Helix speaks.
	OnFirstAudio func()
}

// PlaySpeechStream plays raw PCM as it arrives, returning when playback ends,
// the stream is exhausted, or ctx is cancelled (barge-in, P12.5).
//
// Args:
//   - ctx: cancels both the download and playback.
//   - f: the PCM format the reader supplies.
//   - r: the audio body; always closed before returning.
//   - opts: volume and the first-audio hook.
//
// Returns: ctx.Err() when interrupted, a read error, or nil.
// Complexity: O(duration).
func PlaySpeechStream(ctx context.Context, f StreamFormat, r io.ReadCloser, opts StreamPlayback) error {
	if r == nil {
		return errors.New("speech stream: nil reader")
	}
	defer func() { _ = r.Close() }()

	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !IsEnabled() {
		return errors.New("audio output is disabled (use /audio on)")
	}
	if err := Init(); err != nil {
		return fmt.Errorf("audio device: %w", err)
	}
	if !IsReady() {
		return errors.New("audio engine not ready")
	}
	if f.SampleRate <= 0 {
		return errors.New("speech stream: missing sample rate")
	}
	if f.Channels <= 0 {
		f.Channels = 1
	}
	volume := opts.Volume
	if volume <= 0 || volume > 1 {
		volume = 1
	}

	src := newPCMStream(ctx, f, r)

	// Fill the lead before handing the stream to the speaker. A read error
	// here — the common case, since auth and model errors surface on the first
	// read — is still recoverable by the caller's buffered fallback, because
	// nothing has played yet.
	if err := src.preroll(); err != nil {
		return err
	}

	// The preroll is filled and the speaker is about to receive samples: this
	// is the first-audio instant, within a few milliseconds.
	if opts.OnFirstAudio != nil {
		opts.OnFirstAudio()
	}

	var s beep.Streamer = src
	if sr := beep.SampleRate(f.SampleRate); sr != beep.SampleRate(SampleRate) {
		s = beep.Resample(3, sr, beep.SampleRate(SampleRate), s)
	}
	if volume < 1 {
		base, v := s, volume
		s = beep.StreamerFunc(func(samples [][2]float64) (int, bool) {
			n, ok := base.Stream(samples)
			for i := range samples[:n] {
				samples[i][0] *= v
				samples[i][1] *= v
			}
			return n, ok
		})
	}

	if err := backendPlaySpeech(beep.Take(SampleRate*maxStreamSeconds, s)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return src.err()
}

// pcmStream converts a byte stream into beep samples without ever blocking the
// speaker callback.
type pcmStream struct {
	ctx      context.Context
	format   StreamFormat
	reader   io.Reader
	frameLen int // bytes per frame (2 bytes × channels)

	mu      sync.Mutex
	buf     []byte // undelivered PCM
	done    bool   // producer finished
	readErr error

	ready chan struct{} // closed once preroll is satisfied or the stream ends
	once  sync.Once
}

func newPCMStream(ctx context.Context, f StreamFormat, r io.Reader) *pcmStream {
	s := &pcmStream{
		ctx:      ctx,
		format:   f,
		reader:   r,
		frameLen: 2 * f.Channels,
		ready:    make(chan struct{}),
	}
	go s.produce()
	return s
}

// prerollBytes is the byte count that satisfies prerollMillis for this format.
func (s *pcmStream) prerollBytes() int {
	return s.format.SampleRate * s.frameLen * prerollMillis / 1000
}

// produce reads the body into the queue until EOF, error, or cancellation.
func (s *pcmStream) produce() {
	defer s.signalReady()

	chunk := make([]byte, 8192)
	for {
		if err := s.ctx.Err(); err != nil {
			s.finish(err)
			return
		}
		n, err := s.reader.Read(chunk)
		if n > 0 {
			s.mu.Lock()
			s.buf = append(s.buf, chunk[:n]...)
			enough := len(s.buf) >= s.prerollBytes()
			s.mu.Unlock()
			if enough {
				s.signalReady()
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			s.finish(err)
			return
		}
	}
}

func (s *pcmStream) finish(err error) {
	s.mu.Lock()
	s.done = true
	if err != nil && s.readErr == nil {
		s.readErr = err
	}
	s.mu.Unlock()
}

func (s *pcmStream) signalReady() { s.once.Do(func() { close(s.ready) }) }

// preroll waits for the initial lead, the end of the stream, or cancellation.
func (s *pcmStream) preroll() error {
	select {
	case <-s.ready:
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A stream that ended during preroll without producing audio is a failed
	// synthesis, not a very short one — report it so the caller can fall back.
	if s.readErr != nil {
		return s.readErr
	}
	if s.done && len(s.buf) == 0 {
		return errors.New("speech stream: no audio received")
	}
	return nil
}

func (s *pcmStream) err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readErr
}

// Stream implements beep.Streamer.
//
// CRITICAL CONTRACT — it must FILL the requested buffer completely for as long
// as the stream is live, padding with silence when audio has not arrived yet.
// A short read is reserved for the true end of the stream.
//
// This is not defensiveness, it is beep's actual behavior. Resampler.Stream
// (resample.go:102) reads from its source as:
//
//	sn, _ := r.s.Stream(r.buf1)
//	if sn < len(r.buf1) { r.end = ... }
//
// It DISCARDS the ok flag and treats any short read as a permanent end marker.
// A 24 kHz stream is always resampled to the 44.1 kHz device, so returning
// "here are 64 samples, more coming" silently truncated playback — the first
// sentence was cut to roughly the preroll and the rest was dropped.
//
// It also never blocks: beep pulls samples on the audio goroutine, so waiting
// for the network here would stall the device.
func (s *pcmStream) Stream(out [][2]float64) (int, bool) {
	if s.ctx.Err() != nil {
		return 0, false
	}

	s.mu.Lock()
	frames := len(s.buf) / s.frameLen
	if frames > len(out) {
		frames = len(out)
	}
	take := frames * s.frameLen
	var pcm []byte
	if take > 0 {
		pcm = append(pcm, s.buf[:take]...)
		s.buf = s.buf[take:]
	}
	done, remaining := s.done, len(s.buf)
	s.mu.Unlock()

	for i := 0; i < frames; i++ {
		base := i * s.frameLen
		l := float64(int16(binary.LittleEndian.Uint16(pcm[base:]))) / 32767
		r := l
		if s.format.Channels > 1 {
			r = float64(int16(binary.LittleEndian.Uint16(pcm[base+2:]))) / 32767
		}
		out[i][0], out[i][1] = l, r
	}

	// Genuinely finished: the producer is done and nothing usable is left. A
	// short (or zero) read here is correct — it is how the end is signalled.
	if done && remaining < s.frameLen {
		return frames, frames > 0
	}

	// Still live: pad the remainder with silence so the caller always sees a
	// FULL buffer. Rare in practice — the preroll plus TTS arriving faster
	// than real time keeps the queue ahead — and a brief pad is heard as a
	// tiny pause rather than a truncated sentence.
	for i := frames; i < len(out); i++ {
		out[i][0], out[i][1] = 0, 0
	}
	return len(out), true
}

func (s *pcmStream) Err() error { return nil }
