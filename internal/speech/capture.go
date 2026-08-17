// internal/speech/capture.go
// Purpose: Microphone capture via external recorder binaries (ADR-003): sox
// (`rec`) on any platform, ffmpeg as fallback. Keeps the default build
// CGO-free; native capture may arrive later behind the audio_cgo tag.
// Recordings are temp files with 0600 perms, deleted right after they are
// read into memory — nothing hits persistent storage.
package speech

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// ErrNoRecorder is returned when neither sox nor ffmpeg is installed.
var ErrNoRecorder = errors.New("no audio recorder found — install sox (`brew install sox` / `apt install sox`) or ffmpeg")

// CaptureOptions controls one recording.
type CaptureOptions struct {
	// MaxDuration caps the clip length.
	MaxDuration time.Duration
	// SampleRate is the capture rate (default 16000, ideal for STT).
	SampleRate int
	// NoSilenceStop records the full MaxDuration without sox's silence
	// gating — for chunk-scanning loops (wake word, ambient) where quiet
	// chunks are expected and must yield a clip, not an error.
	NoSilenceStop bool
}

// DetectRecorder returns the concrete recorder binary — "rec", "sox", or
// "ffmpeg" — or an error naming what to install. sox is preferred: its
// `silence` effect gives free trailing-silence stop. The distinction between
// "rec" and "sox" matters: minimal sox packages ship without the `rec`
// symlink, and invoking the wrong binary fails every capture.
func DetectRecorder() (string, error) {
	if _, err := exec.LookPath("rec"); err == nil {
		return "rec", nil
	}
	if _, err := exec.LookPath("sox"); err == nil {
		return "sox", nil
	}
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		return "ffmpeg", nil
	}
	return "", ErrNoRecorder
}

// RecordClip records the default microphone to an in-memory WAV clip.
//
// With sox, recording stops early after ~2s of trailing silence below the
// noise floor (a crude VAD); with ffmpeg the full MaxDuration is captured.
// The context cancels the recording (partial clips are still returned when
// readable, since Ctrl+C mid-utterance should not lose what was said).
func RecordClip(ctx context.Context, opts CaptureOptions) (AudioFormat, error) {
	recorder, err := DetectRecorder()
	if err != nil {
		return AudioFormat{}, err
	}

	if opts.MaxDuration <= 0 {
		opts.MaxDuration = 15 * time.Second
	}
	if opts.SampleRate <= 0 {
		opts.SampleRate = 16000
	}

	tmp, err := os.CreateTemp("", "helix-clip-*.wav")
	if err != nil {
		return AudioFormat{}, fmt.Errorf("create temp clip: %w", err)
	}
	path := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(path) }()
	_ = os.Chmod(path, 0o600)

	var cmd *exec.Cmd
	switch recorder {
	case "rec", "sox":
		var args []string
		if recorder == "rec" {
			args = []string{"rec", "-q",
				"-r", fmt.Sprint(opts.SampleRate), "-c", "1", "-b", "16", "-e", "signed-integer",
				path}
		} else {
			// Plain sox: `-d` is the default-device input; output-format flags
			// sit between input and output file.
			args = []string{"sox", "-q", "-d",
				"-r", fmt.Sprint(opts.SampleRate), "-c", "1", "-b", "16", "-e", "signed-integer",
				path}
		}
		if !opts.NoSilenceStop {
			// Stop after 2s below the silence floor (crude VAD for
			// utterances). 1% by default — sensitive enough to catch quiet
			// speech, high enough to ignore most room noise; override with
			// HELIX_SOX_SILENCE_PCT (e.g. "2%") for noisy rooms.
			sil := os.Getenv("HELIX_SOX_SILENCE_PCT")
			if sil == "" {
				sil = "1%"
			}
			args = append(args, "silence", "1", "0.1", sil, "1", "2.0", sil)
		}
		args = append(args, "trim", "0", fmt.Sprintf("%.1f", opts.MaxDuration.Seconds()))
		cmd = exec.CommandContext(ctx, args[0], args[1:]...)
	case "ffmpeg":
		cmd = exec.CommandContext(ctx, "ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
			"-f", ffmpegInputFormat(), "-i", ffmpegInputDevice(),
			"-t", fmt.Sprintf("%.1f", opts.MaxDuration.Seconds()),
			"-ar", fmt.Sprint(opts.SampleRate), "-ac", "1", "-sample_fmt", "s16",
			path)
	default:
		return AudioFormat{}, ErrNoRecorder
	}

	if err := cmd.Run(); err != nil {
		// A killed recorder may still have flushed a usable partial clip.
		if ctx.Err() == nil {
			return AudioFormat{}, fmt.Errorf("%s recording failed: %w", recorder, err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil || len(data) < 44 {
		// A deadline with zero audio means the silence gate never opened —
		// nobody spoke. Report ErrNoSpeech so the voice loop re-arms the mic
		// instead of dumping the user to typed fallback with a scary error.
		// (Explicit Ctrl+C cancellation keeps the plain error path.)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return AudioFormat{}, ErrNoSpeech
		}
		return AudioFormat{}, fmt.Errorf("%s produced no audio (cancelled?)", recorder)
	}

	rate, channels, err := wavHeaderInfo(data)
	if err != nil {
		return AudioFormat{}, fmt.Errorf("recorded clip unreadable: %w", err)
	}
	return AudioFormat{Kind: KindWAV, SampleRate: rate, Channels: channels, Bytes: data}, nil
}

// StreamRecorder is a single long-lived recorder process piping raw 16-bit
// mono PCM to stdout. Chunk consumers slice the continuous stream in-process,
// eliminating the per-chunk process-spawn gaps (50-200ms of lost audio per
// chunk) that plagued the record-a-file-per-chunk design. This is the capture
// backbone for streaming STT, wake scanning, and ambient monitoring.
type StreamRecorder struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	out        io.ReadCloser
	cancel     context.CancelFunc
	sampleRate int
	closed     bool
}

// NewStreamRecorder starts the recorder process (rec/sox/ffmpeg). The caller
// must Close it; the process also dies with the passed context.
func NewStreamRecorder(ctx context.Context, sampleRate int) (*StreamRecorder, error) {
	recorder, err := DetectRecorder()
	if err != nil {
		return nil, err
	}
	if sampleRate <= 0 {
		sampleRate = 16000
	}

	rctx, cancel := context.WithCancel(ctx)
	var cmd *exec.Cmd
	rate := fmt.Sprint(sampleRate)
	switch recorder {
	case "rec":
		cmd = exec.CommandContext(rctx, "rec", "-q",
			"-t", "raw", "-r", rate, "-c", "1", "-b", "16", "-e", "signed-integer", "-")
	case "sox":
		cmd = exec.CommandContext(rctx, "sox", "-q", "-d",
			"-t", "raw", "-r", rate, "-c", "1", "-b", "16", "-e", "signed-integer", "-")
	case "ffmpeg":
		cmd = exec.CommandContext(rctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
			"-f", ffmpegInputFormat(), "-i", ffmpegInputDevice(),
			"-f", "s16le", "-ar", rate, "-ac", "1", "-")
	default:
		cancel()
		return nil, ErrNoRecorder
	}

	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stream recorder pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("stream recorder start: %w", err)
	}

	return &StreamRecorder{cmd: cmd, out: out, cancel: cancel, sampleRate: sampleRate}, nil
}

// ReadChunk blocks until one chunk of the given duration has been captured
// and returns it as a WAV clip (gapless with its neighbors). Cancellation of
// ctx closes the recorder, unblocking the read.
func (r *StreamRecorder) ReadChunk(ctx context.Context, d time.Duration) (AudioFormat, error) {
	if d <= 0 {
		d = 300 * time.Millisecond
	}
	n := int(float64(r.sampleRate)*d.Seconds()) * 2 // 16-bit mono
	buf := make([]byte, n)

	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		_, err := io.ReadFull(r.out, buf)
		done <- result{err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			return AudioFormat{}, fmt.Errorf("stream capture: %w", res.err)
		}
		return AudioFormat{
			Kind:       KindWAV,
			SampleRate: r.sampleRate,
			Channels:   1,
			Bytes:      EncodeWAVPCM16(buf, r.sampleRate, 1),
		}, nil
	case <-ctx.Done():
		// Unblock the pending read by killing the recorder; the stream is
		// unusable after this, matching Scanner retry-then-rebuild semantics.
		_ = r.Close()
		<-done
		return AudioFormat{}, ctx.Err()
	}
}

// SampleRate reports the stream's capture rate.
func (r *StreamRecorder) SampleRate() int { return r.sampleRate }

// Close terminates the recorder process. Idempotent.
func (r *StreamRecorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	r.cancel()
	_ = r.out.Close()
	_ = r.cmd.Wait()
	return nil
}

// ChunkScanner yields fixed-length, silence-ungated WAV chunks for streaming
// STT. It prefers a persistent StreamRecorder (gapless); when the stream
// cannot start it degrades to one recorder process per chunk.
type ChunkScanner struct {
	chunkDuration time.Duration
	sampleRate    int

	mu       sync.Mutex
	rec      *StreamRecorder
	fallback bool
}

// NewChunkScanner builds a chunk scanner (default 300ms at 16 kHz).
func NewChunkScanner(chunkDuration time.Duration, sampleRate int) *ChunkScanner {
	if chunkDuration <= 0 {
		chunkDuration = 300 * time.Millisecond
	}
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	return &ChunkScanner{chunkDuration: chunkDuration, sampleRate: sampleRate}
}

// NextChunk returns the next gapless chunk from the persistent stream, or —
// in fallback mode — records one fixed-length clip with silence gating
// disabled (quiet chunks are expected mid-utterance and must yield audio).
func (c *ChunkScanner) NextChunk(ctx context.Context) (AudioFormat, error) {
	c.mu.Lock()
	if !c.fallback && c.rec == nil {
		rec, err := NewStreamRecorder(context.Background(), c.sampleRate)
		if err != nil {
			c.fallback = true
		} else {
			c.rec = rec
		}
	}
	rec := c.rec
	c.mu.Unlock()

	if rec != nil {
		clip, err := rec.ReadChunk(ctx, c.chunkDuration)
		if err == nil {
			return clip, nil
		}
		// Dead stream (device yanked, process killed): drop to per-chunk
		// recording for this call; the next call retries the stream.
		c.mu.Lock()
		if c.rec == rec {
			_ = rec.Close()
			c.rec = nil
		}
		c.mu.Unlock()
		if ctx.Err() != nil {
			return AudioFormat{}, err
		}
	}

	return RecordClip(ctx, CaptureOptions{
		MaxDuration:   c.chunkDuration,
		SampleRate:    c.sampleRate,
		NoSilenceStop: true,
	})
}

// Close releases the persistent recorder (if any).
func (c *ChunkScanner) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rec != nil {
		err := c.rec.Close()
		c.rec = nil
		return err
	}
	return nil
}

// ffmpegInputFormat returns the platform capture demuxer.
func ffmpegInputFormat() string {
	switch runtime.GOOS {
	case "darwin":
		return "avfoundation"
	case "windows":
		return "dshow"
	default:
		return "pulse"
	}
}

// ffmpegInputDevice returns the platform default audio-input device spec.
// Override with HELIX_AUDIO_DEVICE for exotic setups.
func ffmpegInputDevice() string {
	if dev := os.Getenv("HELIX_AUDIO_DEVICE"); dev != "" {
		return dev
	}
	switch runtime.GOOS {
	case "darwin":
		return ":0" // avfoundation: first audio input
	case "windows":
		return "audio=Microphone"
	default:
		return "default"
	}
}
