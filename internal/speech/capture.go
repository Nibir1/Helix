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
		// Effects: stop after 2s below 3% amplitude, hard cap at MaxDuration.
		cmd = exec.CommandContext(ctx, "rec", "-q",
			"-r", fmt.Sprint(opts.SampleRate), "-c", "1", "-b", "16", "-e", "signed-integer",
			path,
			"silence", "1", "0.1", "3%", "1", "2.0", "3%",
			"trim", "0", fmt.Sprintf("%.1f", opts.MaxDuration.Seconds()))
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
