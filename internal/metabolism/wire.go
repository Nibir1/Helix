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
//   - Recording cannot change what Helix does. Nothing recorded is read back
//     into a prompt. Learned lessons are a separate opt-in (/lessons on): the
//     engine writes them to lessons.json (lessons.go), and they reach the
//     planner only through the fenced data-only learned-lessons block.
//
// The records are a wire format, not a shared Go type: Helix does not import
// the engine, so the engine can change without touching Helix's build. The JSON
// shape matches the engine's types package field for field, and both
// repositories test against the same golden files (testdata/wire_v1.ndjson,
// testdata/wire_v2.ndjson).
package metabolism

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// WireVersion is the record format version. Bump it on any change a reader
// could misparse, and teach `metabolism ingest` the new version first.
//
// Version 2 (Phase 3) adds an episode's withheld lessons and the feedback
// record. Version 3 (Metabolism D-031) adds what the turn decided: model
// calls per kind, a record per planner round, the firewall critic's verdict,
// why the chat fallback ran, and each step's round. Version 4 (D-031,
// planning mode) adds the provider's reported token counts per turn and per
// planner round, and which arm of the thinking coin flip the turn was in.
// Every v3 and v4 field is optional. Older files are still read by the
// engine.
const WireVersion = 4

// HostName is how Helix identifies itself in every episode.
const HostName = "helix"

// Record is one NDJSON line. Exactly one of Episode, Outcome and Feedback is
// set.
type Record struct {
	V       int      `json:"v"`
	Kind    string   `json:"kind"` // "episode" | "outcome" | "feedback"
	Episode *Episode `json:"episode,omitempty"`
	Outcome *Outcome `json:"outcome,omitempty"`
	// Feedback is what the user told Helix about a lesson (wire v2).
	Feedback *Feedback `json:"feedback,omitempty"`
}

// Feedback mirrors the engine's ndjson.Feedback. FeedbackForget: /lessons
// forget eliminated the lesson, with a reason. FeedbackForgetHard: /lessons
// forget --hard deleted it, and the engine deletes it from its journal with
// an audit record (Metabolism D-028).
type Feedback struct {
	ID       string    `json:"id"`
	LessonID string    `json:"lesson_id"`
	At       time.Time `json:"at"`
	Action   string    `json:"action"`
	Reason   string    `json:"reason"`
}

// The actions /lessons forget records.
const (
	FeedbackForget     = "forget"
	FeedbackForgetHard = "forget-hard"
)

// Episode mirrors the engine's types.Episode.
type Episode struct {
	ID        string    `json:"id"`
	Host      string    `json:"host"`
	Scope     Scope     `json:"scope"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Request   Request   `json:"request"`
	Exposure  []string  `json:"exposure,omitempty"`
	// Withheld lists lessons that applied and fitted the budget but lost the
	// coin flip: the matched no-lesson turns the engine's credit ledger
	// compares Exposure against (wire v2).
	Withheld []string          `json:"withheld,omitempty"`
	Steps    []Step            `json:"steps,omitempty"`
	Usage    Usage             `json:"usage"`
	End      string            `json:"end"`
	Attrs    map[string]string `json:"attrs,omitempty"`

	// What the turn decided (wire v3). Plans has one record per planner
	// round; Critic is set when the firewall critic reviewed a plan;
	// Fallback says why the chat fallback answered instead of a plan.
	Plans    []Plan  `json:"plans,omitempty"`
	Critic   *Critic `json:"critic,omitempty"`
	Fallback string  `json:"fallback,omitempty"`

	// Thinking is the turn's arm of the planning-mode coin flip: "on" or
	// "off" (wire v4). Empty when no coin was flipped.
	Thinking string `json:"thinking,omitempty"`
}

// The arms of the thinking coin flip (wire v4).
const (
	ThinkingOn  = "on"
	ThinkingOff = "off"
)

// Tokens is what the provider reported model calls cost (wire v4, mirrors
// types.Tokens). Calls is how many calls came with a report: the rest are
// not in these counts.
type Tokens struct {
	Prompt     int `json:"prompt"`
	CacheHit   int `json:"cache_hit,omitempty"`
	Completion int `json:"completion"`
	Reasoning  int `json:"reasoning,omitempty"`
	Calls      int `json:"calls"`
}

// tokensDelta is what was reported between two totals (nil when nothing).
func tokensDelta(now, before Tokens) *Tokens {
	d := Tokens{
		Prompt: now.Prompt - before.Prompt, CacheHit: now.CacheHit - before.CacheHit,
		Completion: now.Completion - before.Completion, Reasoning: now.Reasoning - before.Reasoning,
		Calls: now.Calls - before.Calls,
	}
	if d.Calls <= 0 {
		return nil
	}
	return &d
}

// TokensSince is the exported tokensDelta, for per-round plan records.
func TokensSince(now, before Tokens) *Tokens { return tokensDelta(now, before) }

// Plan is one planner round of a turn (wire v3, mirrors types.PlanRecord).
type Plan struct {
	Round int `json:"round"`
	// Prompt is the planner prompt that produced it: full, compact or
	// minimal (the smaller ones are tried only after an empty answer).
	Prompt string `json:"prompt"`
	// Calls is how many model calls the planning cost, retries included.
	Calls int `json:"calls"`
	// Result: planned, error (no plan), parse-error, canary or quarantined.
	Result string `json:"result"`
	// Intent and FirstTool are the plan's own labels; Answered is true when
	// every step is a reply to the user, so the round acted on nothing.
	Intent    string `json:"intent,omitempty"`
	FirstTool string `json:"first_tool,omitempty"`
	Answered  bool   `json:"answered,omitempty"`
	Steps     int    `json:"steps,omitempty"`
	// InputChars and OutputChars are what the planning sent and received,
	// retries included: a habit that changes the prompt saves tokens, not
	// calls, and this is where they show.
	InputChars  int `json:"input_chars,omitempty"`
	OutputChars int `json:"output_chars,omitempty"`
	// Tokens is what the provider reported the planning cost (wire v4).
	Tokens *Tokens `json:"tokens,omitempty"`
}

// Critic is the firewall critic's review of a plan (wire v3).
type Critic struct {
	Verdict string `json:"verdict"` // yes | no
	Calls   int    `json:"calls"`
}

// Why the chat fallback answered instead of a plan (wire v3).
const (
	FallbackPlannerError = "planner-error"
	FallbackParseError   = "parse-error"
	FallbackQuarantine   = "critic-quarantine"
)

// Plan results (wire v3).
const (
	PlanPlanned     = "planned"
	PlanError       = "error"
	PlanParseError  = "parse-error"
	PlanCanary      = "canary"
	PlanQuarantined = "quarantined"
)

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
	// Round is the planner round that planned the step, from 1 (wire v3).
	Round int `json:"round,omitempty"`
}

// Usage mirrors types.Usage.
type Usage struct {
	ModelCalls  int `json:"model_calls"`
	InputChars  int `json:"input_chars"`
	OutputChars int `json:"output_chars"`
	// ByKind splits ModelCalls by what each call was for: planner, critic,
	// chat, tool, vision (wire v3).
	ByKind map[string]int `json:"by_kind,omitempty"`
	// Tokens is the provider's reported token count for the turn (wire v4).
	Tokens *Tokens `json:"tokens,omitempty"`
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
