// internal/wakeword/service.go
// Purpose: WakeWordService — the chunk-scanning loop behind hands-free
// voice mode (BlackBox Phase 3). A Scanner yields short audio chunks
// (production: sox/ffmpeg shell-out per chunk, ADR-003; tests: fixtures);
// a Detector scores each chunk (energy fallback or sidecar keyword model);
// wake events fire at most once per cooldown window. Kill-switch phrases
// are checked by the caller on transcripts (voice_mode.go), not here.
package wakeword

import (
	"context"
	"fmt"
	"time"

	"helix/internal/speech"
)

// Detector scores one audio chunk for wake potential.
type Detector interface {
	Wake(clip speech.AudioFormat) (score float64, woke bool, err error)
}

// Scanner yields short audio chunks until exhausted or cancelled.
type Scanner interface {
	NextChunk(ctx context.Context) (speech.AudioFormat, error)
	Close() error
}

// Config tunes the service loop.
type Config struct {
	// Cooldown suppresses repeated wake events (default CooldownDefault).
	Cooldown time.Duration
	// ChunkTimeout bounds one scanner call.
	ChunkTimeout time.Duration
	// Phrase is echoed back on wake events (display only).
	Phrase string
	// OnError receives non-fatal scanner/detector errors (nil = ignore).
	OnError func(error)
}

// service implements the Service interface fixed in Phase 0.
type service struct {
	scanner  Scanner
	detector Detector
	cfg      Config
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewService builds the loop over a scanner and detector.
func NewService(scanner Scanner, detector Detector, cfg Config) (Service, error) {
	if scanner == nil || detector == nil {
		return nil, fmt.Errorf("wakeword: scanner and detector are required")
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = CooldownDefault
	}
	if cfg.ChunkTimeout <= 0 {
		cfg.ChunkTimeout = 5 * time.Second
	}
	return &service{scanner: scanner, detector: detector, cfg: cfg, done: make(chan struct{})}, nil
}

// Start runs the scan loop until Stop or context cancellation. Events are
// debounced by the cooldown window. A scanner error terminates the loop and
// closes the channel (the caller decides whether to rebuild).
func (s *service) Start(ctx context.Context) (<-chan WakeEvent, error) {
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	events := make(chan WakeEvent, 1)
	go func() {
		defer close(events)
		defer close(s.done)
		defer cancel()

		var lastWake time.Time
		for {
			if runCtx.Err() != nil {
				return
			}

			cctx, ccancel := context.WithTimeout(runCtx, s.cfg.ChunkTimeout)
			clip, err := s.scanner.NextChunk(cctx)
			ccancel()
			if err != nil {
				if runCtx.Err() != nil {
					return // cancelled mid-scan: clean stop
				}
				if s.cfg.OnError != nil {
					s.cfg.OnError(err)
				}
				return // scanner exhausted/failed: end the loop cleanly
			}

			score, woke, err := s.detector.Wake(clip)
			if err != nil {
				if s.cfg.OnError != nil {
					s.cfg.OnError(err)
				}
				continue // detector hiccup: keep scanning
			}
			if !woke {
				continue
			}
			if time.Since(lastWake) < s.cfg.Cooldown {
				continue // debounce: one event per cooldown window
			}
			lastWake = time.Now()

			select {
			case events <- WakeEvent{DetectedAt: lastWake, Score: score, Phrase: s.cfg.Phrase}:
			case <-runCtx.Done():
				return
			}
		}
	}()

	return events, nil
}

// Stop cancels the loop and waits for it to settle.
func (s *service) Stop() error {
	if s.cancel != nil {
		s.cancel()
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
		}
	}
	return s.scanner.Close()
}

// soxScanner records fixed-length chunks via the speech capture utility
// (sox preferred for its fast start; ffmpeg fallback), yielding WAV clips.
type soxScanner struct {
	chunkDuration time.Duration
	sampleRate    int
}

// NewSoXScanner builds the production chunk scanner.
func NewSoXScanner(chunkDuration time.Duration, sampleRate int) Scanner {
	if chunkDuration <= 0 {
		chunkDuration = 1500 * time.Millisecond
	}
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	return &soxScanner{chunkDuration: chunkDuration, sampleRate: sampleRate}
}

func (s *soxScanner) NextChunk(ctx context.Context) (speech.AudioFormat, error) {
	return speech.RecordClip(ctx, speech.CaptureOptions{
		MaxDuration: s.chunkDuration,
		SampleRate:  s.sampleRate,
	})
}

func (s *soxScanner) Close() error { return nil }
