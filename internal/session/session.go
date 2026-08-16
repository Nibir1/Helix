// Package session gives Helix stateful awareness: a bounded conversation
// memory (ring buffer of recent turns persisted to ~/.helix/session.json,
// 0600) injected into planner calls as data-only context, plus the safe-subset
// undo journal ("undo that" for actions with a known reversal).
//
// Security notes: history injected into planner prompts is fenced with
// zero-authority markers exactly like retrieved RAG data (Instruction
// Firewall conventions); the undo journal records reversal commands but every
// reversal still passes the full safety pipeline and Voice Risk Policy.
//
// BlackBox Phase 4 stage 4B (roadmap §6). Skeleton compiled and tested since
// Phase 0.
package session

import "time"

// Turn is one conversation exchange (user input + assistant response
// summary) retained in the ring buffer.
type Turn struct {
	Timestamp time.Time `json:"timestamp"`
	Channel   string    `json:"channel"` // "text" | "voice"
	UserText  string    `json:"user_text"`
	Reply     string    `json:"reply"`
}

// DefaultCapacity is the ring-buffer size for session memory.
const DefaultCapacity = 20

// Store is the conversation memory contract (SessionStore in the roadmap).
type Store interface {
	// Append records a turn, evicting the oldest beyond capacity.
	Append(Turn)

	// Recent returns the last n turns, oldest first.
	Recent(n int) []Turn

	// Clear wipes the in-memory buffer and the persisted session file.
	Clear() error
}
