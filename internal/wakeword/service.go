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
	"sync"
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
	//
	// Wire it to something a human can see. An empty func here is worse than
	// nil: it looks connected and discards every recorder failure, device
	// error and permission denial, and since five of those in a row end the
	// loop (maxConsecutiveScanErrs) the result is a wake service that is dead
	// and silent about it.
	OnError func(error)

	// OnScan receives one report per scanned chunk — the instrumentation hook
	// for "is it hearing anything, and what is it comparing against". Called
	// on the scan goroutine, so it must not block for long. Nil = ignore.
	OnScan func(ScanReport)
}

// ScanReport is what one chunk of the wake loop measured. Everything a
// "speaking does nothing" report needs: whether capture worked, how loud the
// chunk was, and the level it was judged against.
type ScanReport struct {
	// Err is the capture or scoring failure, if any. When non-nil the level
	// fields are meaningless.
	Err error

	// Consecutive counts back-to-back capture failures so far; the loop ends
	// at maxConsecutiveScanErrs.
	Consecutive int

	// RMS is the chunk's normalized level (0..1) — the detector's raw score.
	RMS float64

	// Threshold is the level the chunk was compared against, when the detector
	// can report one (see Thresholder); 0 when it cannot.
	Threshold float64

	// Woke is the detector's verdict for this chunk, before cooldown
	// debouncing.
	Woke bool

	// Suppressed is true when Woke fired but the cooldown window swallowed it,
	// which otherwise looks identical to not hearing anything.
	Suppressed bool
}

// Thresholder is optionally implemented by detectors that can report the level
// they judged the last chunk against, so instrumentation can print the number
// and not just the verdict.
type Thresholder interface {
	Bar() float64
}

// service implements the Service interface fixed in Phase 0.
type service struct {
	scanner  Scanner
	detector Detector
	cfg      Config
	cancel   context.CancelFunc
	done     chan struct{}

	mu      sync.Mutex
	lastErr error
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
// debounced by the cooldown window. Persistent scanner failure terminates the
// loop and closes the channel; Err() then says why, and the caller must treat
// a closed channel as "no longer listening" rather than as silence.
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
				// Transient chunk failures (recorder restart, device hiccup,
				// per-chunk timeout in a quiet room) must not kill hands-free
				// listening; only persistent failure ends the loop.
				consecutiveErrs++
				s.report(ScanReport{Err: err, Consecutive: consecutiveErrs})
				if s.cfg.OnError != nil {
					s.cfg.OnError(err)
				}
				if consecutiveErrs >= maxConsecutiveScanErrs {
					s.setErr(fmt.Errorf("wake capture failed %d times in a row, last: %w",
						consecutiveErrs, err))
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
				s.report(ScanReport{Err: err})
				if s.cfg.OnError != nil {
					s.cfg.OnError(err)
				}
				continue // detector hiccup: keep scanning
			}

			suppressed := woke && time.Since(lastWake) < s.cfg.Cooldown
			s.report(ScanReport{
				RMS:        score,
				Threshold:  s.threshold(),
				Woke:       woke,
				Suppressed: suppressed,
			})
			if !woke || suppressed {
				continue // quiet chunk, or debounced inside the cooldown window
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

// report hands one chunk's measurements to the instrumentation hook.
func (s *service) report(r ScanReport) {
	if s.cfg.OnScan != nil {
		s.cfg.OnScan(r)
	}
}

// threshold asks the detector what it judged the last chunk against, 0 when it
// cannot say (the sidecar and energy detectors both can).
func (s *service) threshold() float64 {
	if t, ok := s.detector.(Thresholder); ok {
		return t.Bar()
	}
	return 0
}

// setErr records why the loop ended, for Err().
func (s *service) setErr(err error) {
	s.mu.Lock()
	s.lastErr = err
	s.mu.Unlock()
}

// Err reports why the scan loop ended, or nil for a clean stop (Stop, context
// cancellation) or a loop still running.
//
// This exists because a closed event channel is indistinguishable from a quiet
// room, and the two must not be shown to the user the same way: one is standby,
// the other is a microphone that stopped working while the screen still said
// "listening".
func (s *service) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
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

// ScannerOption configures the production scanner.
type ScannerOption func(*speech.ChunkScanner)

// OnCaptureNotice reports capture problems the scanner recovered from — the
// gapless stream refusing to start or dying mid-run. These are NOT returned as
// errors (a clip still arrives, from the per-chunk fallback), so without this
// hook a machine running the whole standby window on the degraded path looks
// identical to one running on the good one.
func OnCaptureNotice(fn func(error)) ScannerOption {
	return func(c *speech.ChunkScanner) { c.Notice = fn }
}

// NewSoXScanner builds the production chunk scanner.
func NewSoXScanner(chunkDuration time.Duration, sampleRate int, opts ...ScannerOption) Scanner {
	if chunkDuration <= 0 {
		chunkDuration = 1500 * time.Millisecond
	}
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	inner := speech.NewChunkScanner(chunkDuration, sampleRate)
	for _, opt := range opts {
		opt(inner)
	}
	return &soxScanner{inner: inner}
}

func (s *soxScanner) NextChunk(ctx context.Context) (speech.AudioFormat, error) {
	return s.inner.NextChunk(ctx)
}

func (s *soxScanner) Close() error { return s.inner.Close() }
