// Package metabolism records what Helix did and how it turned out, for
// Metabolism, the Digestive AI engine (github.com/Nibir1/metabolism).
//
// Phase 0 of that project needs a baseline: weeks of real Helix use, measured,
// with learning off. This package is the recording half. It writes one NDJSON
// line per planner turn (an episode) and per signal about how a turn turned out
// (an outcome) to ~/.helix/metabolism/episodes.ndjson. `metabolism ingest`
// reads that file into the engine's journal.
//
// What it is not:
//
//   - It is not telemetry. It imports no networking and never sends anything
//     anywhere; the file stays on this machine until the user ingests it.
//   - It is not on by default. /metabolism on starts recording, like the voice
//     log: an absent file is a privacy guarantee.
//   - It cannot change what Helix does. Nothing here is read back into a
//     prompt. Learned lessons, when they exist, arrive through their own
//     fenced data-only block in a later phase.
//
// The records are a wire format, not a shared Go type: Helix does not import
// the engine, so the engine can change without touching Helix's build. The JSON
// shape matches the engine's types package field for field, and both
// repositories test against the same golden file (testdata/wire_v1.ndjson).
package metabolism

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// WireVersion is the record format version. Bump it on any change a reader
// could misparse, and teach `metabolism ingest` the new version first.
const WireVersion = 1

// HostName is how Helix identifies itself in every episode.
const HostName = "helix"

// Record is one NDJSON line. Exactly one of Episode and Outcome is set.
type Record struct {
	V       int      `json:"v"`
	Kind    string   `json:"kind"` // "episode" | "outcome"
	Episode *Episode `json:"episode,omitempty"`
	Outcome *Outcome `json:"outcome,omitempty"`
}

// Episode mirrors the engine's types.Episode.
type Episode struct {
	ID        string            `json:"id"`
	Host      string            `json:"host"`
	Scope     Scope             `json:"scope"`
	StartedAt time.Time         `json:"started_at"`
	EndedAt   time.Time         `json:"ended_at"`
	Request   Request           `json:"request"`
	Exposure  []string          `json:"exposure,omitempty"`
	Steps     []Step            `json:"steps,omitempty"`
	Usage     Usage             `json:"usage"`
	End       string            `json:"end"`
	Attrs     map[string]string `json:"attrs,omitempty"`
}

// Scope mirrors types.Scope.
type Scope struct {
	Level string `json:"level"`
	Key   string `json:"key,omitempty"`
}

// Request mirrors types.Request.
type Request struct {
	Text       string `json:"text"`
	Provenance string `json:"provenance"`
}

// Step mirrors types.Step. Duration is nanoseconds, as encoding/json writes a
// time.Duration.
type Step struct {
	Tool     string        `json:"tool"`
	Action   string        `json:"action,omitempty"`
	Subject  string        `json:"subject,omitempty"`
	OK       bool          `json:"ok"`
	Err      string        `json:"err,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
}

// Usage mirrors types.Usage.
type Usage struct {
	ModelCalls  int `json:"model_calls"`
	InputChars  int `json:"input_chars"`
	OutputChars int `json:"output_chars"`
}

// Outcome mirrors types.Outcome.
type Outcome struct {
	ID        string    `json:"id"`
	EpisodeID string    `json:"episode_id"`
	At        time.Time `json:"at"`
	Kind      string    `json:"kind"`
	Source    string    `json:"source"`
	Note      string    `json:"note,omitempty"`
}

// Values of the enumerated fields, spelled exactly as the engine spells them.
const (
	ProvUserTyped           = "user-typed"
	ProvUserVoice           = "user-voice"
	ProvUserVoiceUnreliable = "user-voice-unreliable"

	ScopeMachine = "machine"
	ScopeProject = "project"

	EndDone            = "done"
	EndFailed          = "failed"
	EndBudgetExhausted = "budget-exhausted"
	EndOpenWork        = "open-work"

	OutcomeSuccess  = "success"
	OutcomeFailure  = "failure"
	OutcomeUndone   = "undone"
	OutcomeDeclined = "declined"
	OutcomeRepeated = "repeated"

	SourceHost    = "host"
	SourceUser    = "user"
	SourceDerived = "derived"
)

// NewID mints an ID in the engine's format: 12 hex digits of Unix
// milliseconds, then 16 random hex digits. Time-prefixed so IDs sort by
// creation order.
func NewID(now time.Time) string {
	var r [8]byte
	_, _ = rand.Read(r[:])
	return fmt.Sprintf("%012x%s", now.UnixMilli()&0xffffffffffff, hex.EncodeToString(r[:]))
}
