// internal/metrics/metrics.go
// Purpose: BlackBox P3.5 — the local metrics journal, and the reader that was
// deferred with it.
//
// Four metrics files have been WRITTEN since Phase 3 (wake, voice, vision,
// ambient) and nothing has ever read one. That is why P7.8 — "metrics collection
// run against the §10 table", one of the two items gating the release tag — had
// no tooling behind it: the numbers were on disk in NDJSON and the only way to
// see them was `cat`.
//
// This package owns both ends on purpose. Writing lived in cmd/helix and reading
// did not exist, so a reader added elsewhere would have re-declared the field
// names and been free to disagree with the writer — the same
// dropped-at-the-boundary bug this repo has now hit three times with
// speech Endpoints. One package defines the paths, the field names, and the
// summary, so a writer and a reader cannot drift.
//
// Telemetry-free, like the journal: no networking here, grep-enforced by test.
// These files never leave the machine (threat V5), and /purge wipes them.
package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Metric file names (each becomes <name>.jsonl).
const (
	FileWake    = "wake"
	FileVoice   = "voice"
	FileVision  = "vision"
	FileAmbient = "ambient"

	// FileSpeech is new: TTS time-to-first-audio was tracked only in memory
	// (speech.LastSynthesizeLatencyMs), so the one §10 number with a hard
	// millisecond budget vanished on exit and could not be audited from the
	// metrics directory the roadmap says holds all of them.
	FileSpeech = "speech"
)

// Metric names recorded inside the files.
const (
	MetricWakeToExec     = "wake_to_exec"
	MetricFrameToInsight = "frame_to_insight"
	MetricFirstAudio     = "tts_first_audio"
)

// Record is one metrics line. Every field is optional: the files are NDJSON
// written by different call sites, and a reader must tolerate a line that
// predates a field it now knows about.
type Record struct {
	TS      time.Time `json:"-"`
	TSRaw   string    `json:"ts,omitempty"`
	Metric  string    `json:"metric,omitempty"`
	Latency int64     `json:"latency,omitempty"` // milliseconds

	Provider    string `json:"provider,omitempty"`     // vision/TTS provider
	STTProvider string `json:"stt_provider,omitempty"` // voice turns

	Score  float64 `json:"score,omitempty"`
	Phrase string  `json:"phrase,omitempty"`

	Category  string  `json:"category,omitempty"`
	Intensity float64 `json:"intensity,omitempty"`

	Streamed bool `json:"streamed,omitempty"`
}

// Dir returns ~/.helix/metrics.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helix", "metrics"), nil
}

// Append writes one record to <name>.jsonl in the default directory.
//
// Best-effort and silent on failure, like the journal: a metrics write must
// never break the turn it is measuring.
func Append(name string, fields map[string]any) {
	dir, err := Dir()
	if err != nil {
		return
	}
	AppendAt(dir, name, fields)
}

// AppendAt writes to an explicit directory (tests).
func AppendAt(dir, name string, fields map[string]any) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	if _, ok := fields["ts"]; !ok {
		fields["ts"] = time.Now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(fields)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, name+".jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

// Load reads and parses one metrics file, skipping unparseable lines.
//
// A corrupt line is skipped rather than fatal: these files are appended to by a
// long-running daemon and a truncated final line after a kill -9 is normal.
// Losing one sample is better than refusing to report any.
func Load(dir, name string) ([]Record, error) {
	path := filepath.Join(dir, name+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // never measured is not an error
		}
		return nil, err
	}

	var out []Record
	for _, line := range splitLines(data) {
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		if r.TSRaw != "" {
			if ts, perr := time.Parse(time.RFC3339, r.TSRaw); perr == nil {
				r.TS = ts
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// LatencySummary describes a latency series in milliseconds.
type LatencySummary struct {
	N   int
	P50 int64
	P95 int64
	Min int64
	Max int64
}

// P95Meaningful reports whether the sample is large enough for the p95 to mean
// anything. With four samples the "95th percentile" is just the maximum wearing
// a statistical hat, and reporting it as a percentile invites a release decision
// on a number that is not one.
func (s LatencySummary) P95Meaningful() bool { return s.N >= 20 }

// SummarizeLatency computes the distribution of a latency series.
func SummarizeLatency(recs []Record) LatencySummary {
	values := make([]int64, 0, len(recs))
	for _, r := range recs {
		if r.Latency > 0 {
			values = append(values, r.Latency)
		}
	}
	if len(values) == 0 {
		return LatencySummary{}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })

	return LatencySummary{
		N:   len(values),
		P50: percentile(values, 50),
		P95: percentile(values, 95),
		Min: values[0],
		Max: values[len(values)-1],
	}
}

// percentile returns the nearest-rank percentile of a sorted slice.
func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p * len(sorted)) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// Filter returns records whose Metric matches, or all of them when the file
// records only one kind (wake, ambient) and carries no metric name.
func Filter(recs []Record, metric string) []Record {
	var out []Record
	for _, r := range recs {
		if r.Metric == metric {
			out = append(out, r)
		}
	}
	return out
}

// Window returns the time span covered by the records, and whether it is known.
func Window(recs []Record) (time.Duration, bool) {
	var first, last time.Time
	for _, r := range recs {
		if r.TS.IsZero() {
			continue
		}
		if first.IsZero() || r.TS.Before(first) {
			first = r.TS
		}
		if r.TS.After(last) {
			last = r.TS
		}
	}
	if first.IsZero() || !last.After(first) {
		return 0, false
	}
	return last.Sub(first), true
}

// WakeStats summarizes hands-free triggering.
type WakeStats struct {
	Events int

	// PerHour is events divided by the observed window. It is only meaningful
	// with a window, which needs at least two timestamped events.
	PerHour     float64
	WindowKnown bool
	Window      time.Duration

	// Unanswered counts wake events not followed by a voice turn within
	// UnansweredWindow.
	//
	// This is the closest honest proxy for the §10 "wake false positives ≤1/hour"
	// target and it is NOT the same measurement: Helix cannot know whether a user
	// meant to wake it. A wake that produced no transcribed turn is either a
	// false trigger or someone changing their mind, and the report says so rather
	// than printing a false-positive rate it cannot actually observe.
	Unanswered int
}

// UnansweredWindow is how long after a wake event a voice turn may still count
// as that wake's answer.
const UnansweredWindow = 60 * time.Second

// SummarizeWake correlates wake events against voice turns.
func SummarizeWake(wake, voice []Record) WakeStats {
	st := WakeStats{Events: len(wake)}
	if w, ok := Window(wake); ok {
		st.Window, st.WindowKnown = w, true
		if hours := w.Hours(); hours > 0 {
			st.PerHour = float64(len(wake)) / hours
		}
	}

	turns := make([]time.Time, 0, len(voice))
	for _, v := range voice {
		if !v.TS.IsZero() {
			turns = append(turns, v.TS)
		}
	}
	for _, w := range wake {
		if w.TS.IsZero() {
			continue
		}
		answered := false
		for _, t := range turns {
			if !t.Before(w.TS) && t.Sub(w.TS) <= UnansweredWindow {
				answered = true
				break
			}
		}
		if !answered {
			st.Unanswered++
		}
	}
	return st
}

// CategoryCounts tallies ambient events by category.
func CategoryCounts(recs []Record) map[string]int {
	out := map[string]int{}
	for _, r := range recs {
		if r.Category != "" {
			out[r.Category]++
		}
	}
	return out
}

// Target is a §10 acceptance threshold for a latency metric.
type Target struct {
	Cloud time.Duration
	Local time.Duration
}

// Targets are the §10 numbers, in one place so a report cannot quote a
// threshold the roadmap does not set.
var Targets = map[string]Target{
	MetricWakeToExec:     {Cloud: 3 * time.Second, Local: 6 * time.Second},
	MetricFirstAudio:     {Cloud: 800 * time.Millisecond, Local: 1500 * time.Millisecond},
	MetricFrameToInsight: {Cloud: 5 * time.Second, Local: 0}, // local is best-effort
}

// LocalProviders are the provider names that mean "nothing left the machine",
// which decides WHICH §10 column a sample is judged against. Deriving the
// verdict from the sample's own provider is the only honest way to apply a table
// with separate cloud and local targets.
var LocalProviders = map[string]bool{
	"whisper-local": true,
	"piper-local":   true,
	"kokoro-local":  true,
	"ollama":        true,
	"llamacpp":      true,
}

// IsLocal reports whether a provider name runs on this machine.
func IsLocal(provider string) bool { return LocalProviders[provider] }

// Verdict compares a measured latency against the target for its path.
//
// Returns the target used and whether the measurement met it. An absent target
// (local frame-to-insight is "best effort") reports ok=true with zero target, so
// the report says "no target" instead of inventing a pass or a fail.
func Verdict(metric string, local bool, measured time.Duration) (time.Duration, bool) {
	t, ok := Targets[metric]
	if !ok {
		return 0, true
	}
	limit := t.Cloud
	if local {
		limit = t.Local
	}
	if limit == 0 {
		return 0, true
	}
	return limit, measured <= limit
}

// splitLines splits NDJSON content into non-empty lines.
func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				out = append(out, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

// FormatMs renders a millisecond count for a report.
func FormatMs(ms int64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.2fs", float64(ms)/1000)
	}
	return fmt.Sprintf("%dms", ms)
}
