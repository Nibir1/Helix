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

// maxConsecutiveScanErrs ends the scan loop after this many back-to-back
// scanner failures (a healthy chunk resets the count).
const maxConsecutiveScanErrs = 5

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
		consecutiveErrs := 0
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
				// Transient chunk failures (recorder restart, device hiccup,
				// per-chunk timeout in a quiet room) must not kill hands-free
				// listening; only persistent failure ends the loop.
				consecutiveErrs++
				if consecutiveErrs >= maxConsecutiveScanErrs {
					return
				}
				select {
				case <-time.After(200 * time.Millisecond):
				case <-runCtx.Done():
					return
				}
				continue
			}
			consecutiveErrs = 0

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

// soxScanner yields fixed-length WAV chunks for wake scanning. It rides the
// shared speech.ChunkScanner, which prefers a persistent gapless PCM stream
// (one recorder process for the whole standby window — no per-chunk spawn
// latency, no audio lost at chunk boundaries, and words that straddle a
// boundary stay intact) and silently degrades to per-chunk recording with
// silence gating DISABLED (quiet chunks are expected in standby and must
// yield a clip, not an error).
type soxScanner struct {
	inner *speech.ChunkScanner
}

// NewSoXScanner builds the production chunk scanner.
func NewSoXScanner(chunkDuration time.Duration, sampleRate int) Scanner {
	if chunkDuration <= 0 {
		chunkDuration = 1500 * time.Millisecond
	}
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	return &soxScanner{inner: speech.NewChunkScanner(chunkDuration, sampleRate)}
}

func (s *soxScanner) NextChunk(ctx context.Context) (speech.AudioFormat, error) {
	return s.inner.NextChunk(ctx)
}

func (s *soxScanner) Close() error { return s.inner.Close() }
