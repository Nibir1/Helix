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
	"os"
	"os/exec"
	"runtime"
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

// DetectRecorder returns "sox", "ffmpeg", or an error naming what to install.
// sox is preferred: its `silence` effect gives free trailing-silence stop.
func DetectRecorder() (string, error) {
	if _, err := exec.LookPath("rec"); err == nil {
		return "sox", nil
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
	case "sox":
		args := []string{"rec", "-q",
			"-r", fmt.Sprint(opts.SampleRate), "-c", "1", "-b", "16", "-e", "signed-integer",
			path}
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
		return AudioFormat{}, fmt.Errorf("%s produced no audio (cancelled?)", recorder)
	}

	rate, channels, err := wavHeaderInfo(data)
	if err != nil {
		return AudioFormat{}, fmt.Errorf("recorded clip unreadable: %w", err)
	}
	return AudioFormat{Kind: KindWAV, SampleRate: rate, Channels: channels, Bytes: data}, nil
}

// ChunkScanner yields fixed-length, silence-ungated WAV chunks for streaming
// STT. It mirrors the wake-word scanner but lives here so streaming does not
// depend on the wakeword package.
type ChunkScanner struct {
	chunkDuration time.Duration
	sampleRate    int
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

// NextChunk records one fixed-length clip with silence gating disabled (quiet
// chunks are expected mid-utterance and must still yield audio).
func (c *ChunkScanner) NextChunk(ctx context.Context) (AudioFormat, error) {
	return RecordClip(ctx, CaptureOptions{
		MaxDuration:   c.chunkDuration,
		SampleRate:    c.sampleRate,
		NoSilenceStop: true,
	})
}

// Close releases any scanner resources (recording is stateless per chunk).
func (c *ChunkScanner) Close() error { return nil }

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
