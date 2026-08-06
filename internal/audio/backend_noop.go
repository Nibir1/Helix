//go:build linux && !audio_cgo

// internal/audio/backend_noop.go
//
// Purpose: Silent audio backend for Linux builds without ALSA/cgo.
// oto's Linux layer requires CGO + libasound2-dev; this backend keeps CI,
// GoReleaser (CGO_ENABLED=0), and contributor machines compiling first try.
// Linux users who want sound: sudo apt install libasound2-dev, then build
// with `go build -tags audio_cgo`.
package audio

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

// backendPlayError is a silent no-op.
func backendPlayError() {}
