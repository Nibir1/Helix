//go:build linux && !audio_cgo

// internal/audio/backend_noop.go
//
// Purpose: Silent audio backend for Linux builds without ALSA/cgo.
// oto's Linux layer requires CGO + libasound2-dev; this backend keeps CI,
// GoReleaser (CGO_ENABLED=0), and contributor machines compiling first try.
// Linux users who want sound: sudo apt install libasound2-dev, then build
// with `go build -tags audio_cgo`.
package audio

import "github.com/gopxl/beep/v2"

// backendInit reports success so the engine stays "ready" but silent.
//
// Args: none.
// Returns: nil.
// Complexity: O(1).
func backendInit() error { return nil }

// backendPlayClick is a silent no-op.
func backendPlayClick() {}

// backendPlayTick is a silent no-op.
func backendPlayTick() {}

// backendPlayAlert is a silent no-op.
func backendPlayAlert() {}

// backendPlayAlertSync is a silent no-op. There is no playback to wait for on
// this backend, so "blocking until the tone finishes" returns immediately —
// which is also the honest answer for the mic-overlap problem it exists to
// solve: a silent build cannot echo its own chime into the microphone.
func backendPlayAlertSync() {}

// backendPlayError is a silent no-op.
func backendPlayError() {}

// backendPlaySpeech explains why speech is silent on this build.
func backendPlaySpeech(beep.Streamer) error { return ErrSpeechUnsupported }

// backendName identifies this build's audio backend for /doctor (P10.3).
// This is the single most consequential build-flavor fact on a Linux edge
// device: the default CGO-free binary CANNOT speak, and without a way to see
// that, a silent appliance looks like a broken TTS provider.
func backendName() string { return "silent (CGO-free Linux build; no ALSA)" }

// backendSpeechSupported reports whether TTS audio can actually be heard.
func backendSpeechSupported() bool { return false }
