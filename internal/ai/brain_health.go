// internal/ai/brain_health.go
//
// Purpose: last-known reachability of the active LLM provider, recorded as a
// side effect of work Helix already does.
//
// Why this exists: the shell KNEW its brain was dead and then said everything
// was fine. Selecting llama.cpp with no llama-server running prints
//
//	llama-server is not reachable at http://127.0.0.1:8080/v1.
//	Until it responds, every planner and chat request will fail.
//
// and a few lines later the per-turn banner said
//
//	[SUCCESS] Helix :: GRID STATUS :: CLEAR
//
// because that line consulted only the speech chains and the failover breaker —
// and the breaker needs two failed model CALLS before it trips, which a session
// that has so far only run slash commands never provides. A startup probe that
// already came back "connection refused" is better evidence than a breaker that
// has not been given the chance to fire, so it is recorded now instead of being
// printed and discarded.
//
// Nothing here probes anything: readers get the last known state for free, which
// is what lets the interactive hot loop consult it every turn.
package ai

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// BrainHealth is the most recent known state of the active LLM provider.
type BrainHealth struct {
	// Attempted reports whether anything has been learned yet. The zero value
	// means "unknown", not "broken" — a provider nobody has called and nobody has
	// probed is not evidence of a problem.
	Attempted bool

	// OK reports whether the provider answered.
	OK bool

	// Provider is the provider the observation is about.
	Provider string

	// Detail is the bounded single-line provider error when OK is false. It is
	// for surfaces with room to print it; the one-line status uses Reason.
	Detail string

	// Cause is the classified short cause, computed from the FULL error at record
	// time. It has to be computed then rather than derived from Detail later: a
	// dial error puts "connection refused" at the very END of a long line, so
	// classifying a truncated Detail silently lost the one word that mattered.
	Cause string
}

// Degraded reports whether the last thing Helix learned was a failure.
func (h BrainHealth) Degraded() bool { return h.Attempted && !h.OK }

// Reason returns a one-clause explanation, or "" when healthy or unknown.
//
// It reports a CLASSIFIED cause rather than the raw error, because it goes inside
// a single line that cannot wrap. Interpolating the provider error produced
//
//	DEGRADED (brain: llamacpp unreachable: request failed: Get "http://127.0.0…)
//
// which is the /voice-status defect all over again — 130 columns with the URL cut
// mid-address. "connection refused" is what the reader actually needs; the full
// error is still on Detail for surfaces that have room.
func (h BrainHealth) Reason() string {
	if !h.Degraded() {
		return ""
	}
	who := h.Provider
	if who == "" {
		who = "active model"
	}
	if h.Cause != "" {
		return who + " unreachable (" + h.Cause + ")"
	}
	return who + " unreachable"
}

// shortCause classifies a provider error into a few words.
//
// Returns "" when nothing matches, in which case the caller omits the
// parenthetical entirely — better a short honest "unreachable" than a fragment
// of an unrecognized error sawn off at an arbitrary column.
func shortCause(detail string) string {
	d := strings.ToLower(detail)
	for _, c := range []struct{ marker, cause string }{
		{"connection refused", "connection refused"},
		{"connection reset", "connection reset"},
		{"no such host", "host not found"},
		{"network is unreachable", "network unreachable"},
		{"host is unreachable", "host unreachable"},
		{"tls handshake", "TLS handshake failed"},
		{"certificate", "TLS certificate rejected"},
		{"deadline exceeded", "timed out"},
		{"i/o timeout", "timed out"},
		{"timed out", "timed out"},
		{"timeout", "timed out"},
		{"http 401", "HTTP 401 unauthorized"},
		{"unauthorized", "HTTP 401 unauthorized"},
		{"http 403", "HTTP 403 forbidden"},
		{"http 404", "HTTP 404 not found"},
		{"http 429", "rate limited"},
		{"missing api key", "no API key configured"},
		{"api key", "API key problem"},
		{"eof", "connection closed early"},
	} {
		if strings.Contains(d, c.marker) {
			return c.cause
		}
	}
	// Any remaining 5xx, without listing every code.
	if strings.Contains(d, "http 5") {
		return "provider server error"
	}
	return ""
}

var (
	brainMu     sync.Mutex
	brainHealth BrainHealth
)

// LastBrainHealth returns the last known state of the active LLM provider.
func LastBrainHealth() BrainHealth {
	brainMu.Lock()
	defer brainMu.Unlock()
	return brainHealth
}

// NoteProviderReachable records a successful probe or call.
func NoteProviderReachable(provider string) {
	brainMu.Lock()
	brainHealth = BrainHealth{Attempted: true, OK: true, Provider: provider}
	brainMu.Unlock()
}

// NoteProviderUnreachable records a failed probe or call.
//
// Exported because the most valuable observation happens in the setup path
// (cmd/helix/helpers.go), which health-checks the endpoint before the user
// commits to it — the one moment Helix has hard evidence and no model call has
// run yet.
//
// Args:
//   - provider: the provider name.
//   - detail: the failure, trimmed to one short line for the status.
//
// Complexity: O(len(detail)).
func NoteProviderUnreachable(provider, detail string) {
	// Classify first, truncate second — see BrainHealth.Cause.
	cause := shortCause(detail)
	brainMu.Lock()
	brainHealth = BrainHealth{
		Attempted: true, Provider: provider,
		Detail: shortDetail(detail), Cause: cause,
	}
	brainMu.Unlock()
}

// brainDetailMax bounds a recorded error so a hostile or verbose provider cannot
// grow the record without limit. Generous, because Detail is only ever shown on
// surfaces that can wrap; the one-line status reads Cause instead.
const brainDetailMax = 200

// shortDetail collapses an error into one bounded line.
func shortDetail(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= brainDetailMax {
		return s
	}
	return string([]rune(s)[:brainDetailMax-1]) + "…"
}

// noteBrainCall records one model-call outcome.
//
// A cancelled call (Ctrl+C, a barge-in) is deliberately not evidence about the
// provider — mirroring the speech registry, where a cancelled turn also leaves
// the last chain outcome alone.
//
// Unlike the breaker in noteCallResult this counts NON-availability failures too
// (a 401, a 400): the breaker rightly refuses to hide a misconfiguration behind
// a quieter local model, but from the user's seat a brain that returns 401 on
// every turn is just as unable to answer, and the status line must say so.
//
// Half-open probes are skipped: the provider that call went to is not the active
// one, so recording it under the active name would mislabel the observation. The
// breaker owns that path, and the next ordinary call records the truth.
func noteBrainCall(err error, probing bool) {
	if probing {
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	if err == nil {
		NoteProviderReachable(ActiveProviderName())
		return
	}
	NoteProviderUnreachable(ActiveProviderName(), err.Error())
}

// CheckActiveProvider probes the active provider and records the outcome.
//
// This is the only function here that touches the network, and it is for
// EXPLICIT diagnostics (/provider-status, /doctor) — never the turn loop, which
// reads LastBrainHealth instead. Probing on demand is what makes a status
// command trustworthy: /provider-status used to list the active provider with its
// key situation and nothing about whether anything was listening, so an
// unreachable llama-server displayed as "local/no key (active)" — the same defect
// as a down whisper.cpp sidecar reporting "key".
//
// Args:
//   - ctx: bounds the probe; callers supply a short timeout.
//
// Returns: nil when the provider answered, otherwise the failure.
// Complexity: O(1) provider round trip.
func CheckActiveProvider(ctx context.Context) error {
	name := ActiveProviderName()
	if name == "" {
		return errors.New("no AI provider configured")
	}
	p, err := registryGet(name)
	if err != nil {
		NoteProviderUnreachable(name, err.Error())
		return err
	}
	if err := p.HealthCheck(ctx); err != nil {
		NoteProviderUnreachable(name, err.Error())
		return err
	}
	NoteProviderReachable(name)
	return nil
}
