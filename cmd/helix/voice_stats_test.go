// cmd/helix/voice_stats_test.go
// Purpose: P3.5 — the report's verdict wording, which is the part a release
// decision reads. The arithmetic is tested in internal/metrics; what matters
// here is that the badge does not overstate what was measured.
package main

import (
	"strings"
	"testing"

	"helix/internal/metrics"
)

// stripANSI removes colour so assertions read the text, not the escapes.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == 0x1b:
			inEsc = true
		case inEsc && s[i] == 'm':
			inEsc = false
		case !inEsc:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func rowText(row []string) string {
	var parts []string
	for _, c := range row {
		parts = append(parts, stripANSI(c))
	}
	return strings.Join(parts, " ")
}

// A metric with no samples must read as unevaluated. Rendering it as a pass
// would turn a fresh install into a green release report.
func TestStatsReportsNotMeasuredWithoutSamples(t *testing.T) {
	rows := latencyRows(metrics.MetricWakeToExec, "wake → execution", nil, sttProviderOf)
	if len(rows) != 1 {
		t.Fatalf("expected one placeholder row, got %d", len(rows))
	}
	text := rowText(rows[0])
	if !strings.Contains(text, "not measured") {
		t.Fatalf("empty metric must say not measured, got %q", text)
	}
	for _, forbidden := range []string{"meets target", "over target"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("an unmeasured metric must not claim %q: %q", forbidden, text)
		}
	}
}

// When every sample is inside the budget, the verdict is unqualified.
func TestStatsReportsMeetsTargetWhenAllSamplesPass(t *testing.T) {
	var recs []metrics.Record
	for _, ms := range []int64{200, 250, 300} {
		recs = append(recs, metrics.Record{Latency: ms, Provider: "openai"})
	}
	rows := latencyRows(metrics.MetricFirstAudio, "TTS first audio", recs, providerOf)
	if len(rows) != 1 {
		t.Fatalf("expected one cloud row, got %d", len(rows))
	}
	text := rowText(rows[0])
	if !strings.Contains(text, "meets target") {
		t.Fatalf("all-passing samples should meet the target, got %q", text)
	}
}

// The distinction this test exists for: a typical turn inside the budget with a
// worst case outside it must NOT read as a flat pass. The first version of the
// report printed "meets target" beside a visible 6.80s worst case against a
// 6.00s budget — true of the p50, and misleading as a release signal.
func TestStatsQualifiesVerdictWhenWorstCaseExceedsTarget(t *testing.T) {
	recs := []metrics.Record{
		{Latency: 3800, STTProvider: "whisper-local"},
		{Latency: 4200, STTProvider: "whisper-local"},
		{Latency: 9000, STTProvider: "whisper-local"}, // outlier past the 6s local budget
	}
	rows := latencyRows(metrics.MetricWakeToExec, "wake → execution", recs, sttProviderOf)
	if len(rows) != 1 {
		t.Fatalf("expected one local row, got %d", len(rows))
	}
	text := rowText(rows[0])
	if strings.Contains(text, "meets target") {
		t.Fatalf("a worst case over budget must not read as a flat pass: %q", text)
	}
	if !strings.Contains(text, "typical only") {
		t.Fatalf("expected a qualified verdict, got %q", text)
	}
}

// Over budget at the p50 is a plain failure.
func TestStatsReportsOverTarget(t *testing.T) {
	recs := []metrics.Record{
		{Latency: 9000, STTProvider: "whisper-local"},
		{Latency: 9500, STTProvider: "whisper-local"},
	}
	rows := latencyRows(metrics.MetricWakeToExec, "wake → execution", recs, sttProviderOf)
	text := rowText(rows[0])
	if !strings.Contains(text, "over target") {
		t.Fatalf("expected over target, got %q", text)
	}
}

// Local and cloud samples for the same metric must be reported separately, or a
// blended p50 gets judged against a budget that applies to neither half.
func TestStatsSplitsLocalAndCloudPaths(t *testing.T) {
	recs := []metrics.Record{
		{Latency: 300, Provider: "openai"},
		{Latency: 1200, Provider: "piper-local"},
	}
	rows := latencyRows(metrics.MetricFirstAudio, "TTS first audio", recs, providerOf)
	if len(rows) != 2 {
		t.Fatalf("expected separate cloud and local rows, got %d", len(rows))
	}
	joined := rowText(rows[0]) + " " + rowText(rows[1])
	if !strings.Contains(joined, "cloud") || !strings.Contains(joined, "local") {
		t.Fatalf("both paths must be labelled: %q", joined)
	}
	// Each is judged against its own §10 column: 1200ms passes the 1.5s local
	// budget while it would fail the 800ms cloud one.
	for _, row := range rows {
		text := rowText(row)
		if strings.Contains(text, "local") && strings.Contains(text, "over target") {
			t.Errorf("1200ms local TTS meets the 1.5s local budget: %q", text)
		}
	}
}

// Local frame-to-insight has no §10 number ("best effort"), so the report must
// say so rather than grade it.
func TestStatsSaysNoTargetWhereTableHasNone(t *testing.T) {
	recs := []metrics.Record{{Latency: 31600, Provider: "ollama"}}
	rows := latencyRows(metrics.MetricFrameToInsight, "frame → insight", recs, providerOf)
	text := rowText(rows[0])
	if !strings.Contains(text, "no target") {
		t.Fatalf("local frame-to-insight has no target, got %q", text)
	}
	if strings.Contains(text, "over target") {
		t.Errorf("an absent target must not be reported as a failure: %q", text)
	}
}
