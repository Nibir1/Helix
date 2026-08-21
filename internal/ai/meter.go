// internal/ai/meter.go
// Purpose: per-session accounting of model traffic so /cost and /context can
// answer "what has this session actually spent?".
//
// Honesty note: no provider in the registry returns a usage block on the
// streaming path Helix uses (providers.CollectChat assembles text only), so
// token counts here are ESTIMATED from character length, never reported. Every
// surface that prints them must say so. The call counts, byte counts, latency,
// and failure counts are exact.
package ai

import (
	"strings"
	"sync"
	"time"
)

// charsPerToken is the estimator's divisor. 4 chars/token is the widely used
// English approximation for BPE vocabularies; it is wrong for code and CJK in
// opposite directions, which is exactly why callers must label the output.
const charsPerToken = 4

// CallKind labels what a model call was for, so /cost can show where the
// session's budget went instead of one undifferentiated total.
type CallKind string

const (
	KindChat    CallKind = "chat"
	KindPlanner CallKind = "planner"
	KindTool    CallKind = "tool"
	KindVision  CallKind = "vision"
)

// UsageRow is the accounting for one (kind, provider, model) triple.
type UsageRow struct {
	Kind     CallKind
	Provider string
	Model    string

	Calls    int
	Failures int

	PromptChars   int64
	ResponseChars int64

	// EstPromptTokens / EstResponseTokens are derived, not reported. See the
	// package note above.
	EstPromptTokens   int64
	EstResponseTokens int64

	Latency time.Duration
	First   time.Time
	Last    time.Time
}

// EstTotalTokens is the estimated combined token count for the row.
func (r UsageRow) EstTotalTokens() int64 { return r.EstPromptTokens + r.EstResponseTokens }

// AvgLatency is the mean round-trip time across the row's calls.
func (r UsageRow) AvgLatency() time.Duration {
	if r.Calls == 0 {
		return 0
	}
	return r.Latency / time.Duration(r.Calls)
}

// UsageReport is the whole session's accounting, newest-activity first.
type UsageReport struct {
	Rows    []UsageRow
	Started time.Time

	Calls    int
	Failures int

	EstPromptTokens   int64
	EstResponseTokens int64
	Latency           time.Duration
}

// EstTotalTokens is the estimated token count across every recorded call.
func (r UsageReport) EstTotalTokens() int64 { return r.EstPromptTokens + r.EstResponseTokens }

type meterKey struct {
	kind     CallKind
	provider string
	model    string
}

var (
	meterMu      sync.Mutex
	meterRows    = map[meterKey]*UsageRow{}
	meterStarted time.Time
)

// EstimateTokens converts text length into an estimated token count. Exported
// because /context estimates the size of things that were never sent to a
// provider (session memory, a candidate prompt) with the same yardstick.
func EstimateTokens(text string) int64 {
	n := len([]rune(text))
	if n == 0 {
		return 0
	}
	est := int64(n) / charsPerToken
	if est == 0 {
		return 1
	}
	return est
}

// RecordCall books one provider round trip. Safe to call from any goroutine;
// a call that failed still counts (it cost latency and usually prompt tokens).
func RecordCall(
	kind CallKind, provider, model, prompt, response string, elapsed time.Duration, err error,
) {
	if provider == "" {
		provider = "unknown"
	}
	if model == "" {
		model = "unknown"
	}
	now := time.Now()

	meterMu.Lock()
	defer meterMu.Unlock()
	if meterStarted.IsZero() {
		meterStarted = now
	}
	k := meterKey{kind: kind, provider: provider, model: model}
	row := meterRows[k]
	if row == nil {
		row = &UsageRow{Kind: kind, Provider: provider, Model: model, First: now}
		meterRows[k] = row
	}
	row.Calls++
	if err != nil {
		row.Failures++
	}
	row.PromptChars += int64(len([]rune(prompt)))
	row.ResponseChars += int64(len([]rune(response)))
	row.EstPromptTokens += EstimateTokens(prompt)
	row.EstResponseTokens += EstimateTokens(response)
	row.Latency += elapsed
	row.Last = now
}

// Usage returns the session report, sorted by estimated total tokens
// descending so the expensive traffic is on top.
func Usage() UsageReport {
	meterMu.Lock()
	defer meterMu.Unlock()

	rep := UsageReport{Started: meterStarted, Rows: make([]UsageRow, 0, len(meterRows))}
	for _, row := range meterRows {
		rep.Rows = append(rep.Rows, *row)
		rep.Calls += row.Calls
		rep.Failures += row.Failures
		rep.EstPromptTokens += row.EstPromptTokens
		rep.EstResponseTokens += row.EstResponseTokens
		rep.Latency += row.Latency
	}
	// Insertion sort: the row count is the number of distinct
	// (kind, provider, model) triples touched in one session — single digits.
	for i := 1; i < len(rep.Rows); i++ {
		for j := i; j > 0 && lessUsage(rep.Rows[j-1], rep.Rows[j]); j-- {
			rep.Rows[j-1], rep.Rows[j] = rep.Rows[j], rep.Rows[j-1]
		}
	}
	return rep
}

// lessUsage reports whether a sorts AFTER b (bigger first, then stable by name).
func lessUsage(a, b UsageRow) bool {
	if a.EstTotalTokens() != b.EstTotalTokens() {
		return a.EstTotalTokens() < b.EstTotalTokens()
	}
	if a.Calls != b.Calls {
		return a.Calls < b.Calls
	}
	return strings.Compare(string(a.Kind)+a.Provider+a.Model, string(b.Kind)+b.Provider+b.Model) > 0
}

// ResetUsage clears the accounting. /clear uses it so a fresh conversation
// starts with a fresh bill.
func ResetUsage() {
	meterMu.Lock()
	defer meterMu.Unlock()
	meterRows = map[meterKey]*UsageRow{}
	meterStarted = time.Time{}
}
