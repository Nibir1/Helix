// Package daemon implements the HelixDaemon ("Living AI", BlackBox Phase 4):
// a persistent background service that owns wake-word listening, speech
// providers, the voice input source, and a headless-capable Agent, supervised
// with crash restart and journaled interactions.
//
// IPC (ADR-004): newline-delimited JSON over a Unix domain socket at
// ~/.helix/daemon.sock (0600) on macOS/Linux and a named pipe on Windows. No
// brokers, no gRPC — stdlib only. Client: `helix remote`.
//
// Security notes (docs/threat_model_voice.md V7): the socket inherits
// 0600/0700 permissions so only the owning UID may connect; an optional
// shared-token file adds replay protection; the daemon refuses `submit`
// while an interactive TTY session holds the active-session lock.
//
// Skeleton compiled and tested since Phase 0.
package daemon

import "time"

// Message types exchanged over the IPC channel (NDJSON, one object per line).
const (
	TypeStatus   = "status"   // client → daemon: report health/state
	TypeSubmit   = "submit"   // client → daemon: inject an input event
	TypeMode     = "mode"     // client ↔ daemon: query or change voice/manual mode
	TypeLogTail  = "log_tail" // client → daemon: tail recent journal entries
	TypeStop     = "stop"     // client → daemon: graceful shutdown
	TypeEvent    = "event"    // daemon → client: async notification
	TypeResponse = "response" // daemon → client: reply to a request
)

// Request is any client → daemon message.
type Request struct {
	Type    string         `json:"type"`
	Text    string         `json:"text,omitempty"`
	Channel string         `json:"channel,omitempty"` // "text" | "voice"
	Timeout time.Duration  `json:"timeout,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// Response is any daemon → client message.
type Response struct {
	Type  string         `json:"type"`
	OK    bool           `json:"ok"`
	Error string         `json:"error,omitempty"`
	State map[string]any `json:"state,omitempty"`
	Meta  map[string]any `json:"meta,omitempty"`
}
