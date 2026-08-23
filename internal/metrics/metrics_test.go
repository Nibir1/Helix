// internal/metrics/metrics_test.go
// Purpose: P3.5 — pin the reader's honesty rules. The summaries feed a release
// decision (P7.8), so the properties worth testing are the ones that stop it
// reporting a number it did not measure.
package metrics

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAppendAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	AppendAt(dir, FileVoice, map[string]any{
		"metric": MetricWakeToExec, "latency": 1200, "stt_provider": "groq",
	})
	AppendAt(dir, FileVoice, map[string]any{
		"metric": MetricWakeToExec, "latency": 800, "stt_provider": "groq",
	})

	recs, err := Load(dir, FileVoice)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	if recs[0].Metric != MetricWakeToExec || recs[0].Latency != 1200 {
		t.Fatalf("record round trip lost fields: %+v", recs[0])
	}
	if recs[0].TS.IsZero() {
		t.Error("Append must stamp a timestamp so a window can be computed")
	}
}

func TestAppendCreates0600In0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	dir := filepath.Join(t.TempDir(), "metrics")
	AppendAt(dir, FileWake, map[string]any{"score": 0.8})

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("metrics dir must be 0700, got %o", perm)
	}
	fi, err := os.Stat(filepath.Join(dir, FileWake+".jsonl"))
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("metrics file must be 0600, got %o", perm)
	}
}

// A missing file means "never measured", which is a normal state for a fresh
// install and must not read as an error — otherwise the report would refuse to
// render on the machine most likely to be running it for the first time.
func TestLoadMissingFileIsNotAnError(t *testing.T) {
	recs, err := Load(t.TempDir(), FileVision)
	if err != nil {
		t.Fatalf("missing file must not error, got %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("expected no records, got %d", len(recs))
	}
}

// A truncated final line is the normal result of killing a daemon mid-write.
// Losing that sample is fine; refusing to report the others is not.
func TestLoadSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileVoice+".jsonl")
	body := `{"ts":"2026-08-23T10:00:00Z","metric":"wake_to_exec","latency":900}` + "\n" +
		`{"ts":"2026-08-23T10:00:0` + "\n" + // truncated by a kill -9
		`{"ts":"2026-08-23T10:01:00Z","metric":"wake_to_exec","latency":1100}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	recs, err := Load(dir, FileVoice)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected the 2 intact records, got %d: %+v", len(recs), recs)
	}
}

func TestSummarizeLatency(t *testing.T) {
	var recs []Record
	for _, ms := range []int64{100, 200, 300, 400, 500} {
		recs = append(recs, Record{Latency: ms})
	}
	got := SummarizeLatency(recs)
	if got.N != 5 || got.Min != 100 || got.Max != 500 {
		t.Fatalf("summary wrong: %+v", got)
	}
	if got.P50 != 300 {
		t.Errorf("p50 = %d, want 300", got.P50)
	}
}

// Records without a latency (wake events, ambient events) must not be counted
// as zero-millisecond samples — that would drag every percentile to zero and
// make a release look faster than it is.
func TestSummarizeIgnoresRecordsWithoutLatency(t *testing.T) {
	recs := []Record{
		{Latency: 500}, {Score: 0.9}, {Category: "loud_noise"}, {Latency: 700},
	}
	got := SummarizeLatency(recs)
	if got.N != 2 {
		t.Fatalf("expected 2 latency samples, got %d (%+v)", got.N, got)
	}
	if got.Min != 500 {
		t.Errorf("min = %d, want 500 — a non-latency record was counted as 0", got.Min)
	}
}

// The p95 guard: on a tiny sample the "95th percentile" is the maximum with a
// statistical hat on, and a release decision should not be made on it.
func TestP95MeaningfulOnlyWithEnoughSamples(t *testing.T) {
	small := SummarizeLatency([]Record{{Latency: 100}, {Latency: 200}})
	if small.P95Meaningful() {
		t.Error("a 2-sample series must not claim a meaningful p95")
	}

	var many []Record
	for i := 0; i < 25; i++ {
		many = append(many, Record{Latency: int64(100 + i)})
	}
	if !SummarizeLatency(many).P95Meaningful() {
		t.Error("a 25-sample series should support a p95")
	}
}

// The §10 table has separate cloud and local columns, so the verdict has to be
// chosen by the sample's own provider. Judging a local whisper turn against the
// 3s cloud budget would fail a configuration that met its actual target.
func TestVerdictUsesTheRightColumn(t *testing.T) {
	// 4s: within the 6s local budget, over the 3s cloud one.
	measured := 4 * time.Second

	limit, ok := Verdict(MetricWakeToExec, true, measured)
	if !ok || limit != 6*time.Second {
		t.Errorf("local path: got limit=%v ok=%v, want 6s and pass", limit, ok)
	}
	limit, ok = Verdict(MetricWakeToExec, false, measured)
	if ok || limit != 3*time.Second {
		t.Errorf("cloud path: got limit=%v ok=%v, want 3s and fail", limit, ok)
	}
}

// Local frame-to-insight is "best effort" in §10 — deliberately no number. The
// report must say so rather than invent a threshold and grade against it.
func TestVerdictReportsNoTargetWhereTableHasNone(t *testing.T) {
	limit, ok := Verdict(MetricFrameToInsight, true, 30*time.Second)
	if limit != 0 {
		t.Errorf("local frame-to-insight has no §10 target, got %v", limit)
	}
	if !ok {
		t.Error("absent target must not be reported as a failure")
	}
}

func TestIsLocalClassifiesProviders(t *testing.T) {
	for _, p := range []string{"whisper-local", "piper-local", "ollama", "llamacpp"} {
		if !IsLocal(p) {
			t.Errorf("%q should be local", p)
		}
	}
	for _, p := range []string{"groq", "openai", "deepgram", "elevenlabs", ""} {
		if IsLocal(p) {
			t.Errorf("%q should not be local", p)
		}
	}
}

// The wake proxy must be labelled as a proxy. What it counts is wakes with no
// answering turn — which is a false trigger OR a user who changed their mind,
// and the two are indistinguishable from here.
func TestSummarizeWakeCountsUnansweredWakes(t *testing.T) {
	base := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	wake := []Record{
		{TS: base},                       // answered 5s later
		{TS: base.Add(30 * time.Minute)}, // never answered
		{TS: base.Add(time.Hour)},        // answered 10s later
	}
	voice := []Record{
		{TS: base.Add(5 * time.Second), Latency: 900},
		{TS: base.Add(time.Hour + 10*time.Second), Latency: 1100},
	}

	st := SummarizeWake(wake, voice)
	if st.Events != 3 {
		t.Fatalf("events = %d, want 3", st.Events)
	}
	if st.Unanswered != 1 {
		t.Errorf("unanswered = %d, want 1", st.Unanswered)
	}
	if !st.WindowKnown {
		t.Fatal("window should be known from 3 timestamped events")
	}
	// 3 events across 1 hour.
	if st.PerHour < 2.9 || st.PerHour > 3.1 {
		t.Errorf("per-hour = %.2f, want ~3", st.PerHour)
	}
}

// A single event has no window, so a rate cannot be computed. Reporting
// "3 per hour" from one event a second after boot would be nonsense.
func TestSummarizeWakeWithoutWindowClaimsNoRate(t *testing.T) {
	st := SummarizeWake([]Record{{TS: time.Now()}}, nil)
	if st.WindowKnown {
		t.Error("a single event cannot establish a window")
	}
	if st.PerHour != 0 {
		t.Errorf("per-hour must stay 0 without a window, got %.2f", st.PerHour)
	}
}

func TestCategoryCounts(t *testing.T) {
	got := CategoryCounts([]Record{
		{Category: "loud_noise"}, {Category: "silence"}, {Category: "loud_noise"},
	})
	if got["loud_noise"] != 2 || got["silence"] != 1 {
		t.Fatalf("counts wrong: %v", got)
	}
}

func TestFormatMs(t *testing.T) {
	cases := map[int64]string{167: "167ms", 999: "999ms", 1000: "1.00s", 8800: "8.80s"}
	for in, want := range cases {
		if got := FormatMs(in); got != want {
			t.Errorf("FormatMs(%d) = %q, want %q", in, got, want)
		}
	}
}

// Telemetry-free contract: the package that reads what the microphone did must
// not be able to send it anywhere (threat V5), same rule as diagnostics and
// journal.
func TestNoNetworkImports(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob metrics package: %v", err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range af.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == "net" || strings.HasPrefix(p, "net/") || p == "crypto/tls" {
				t.Fatalf("metrics must be telemetry-free but imports %q", p)
			}
		}
	}
}

// Every metric with a §10 target must have one, and every target must belong to
// a metric the code actually records — a target for a metric nobody writes is a
// release gate that can never be evaluated.
func TestTargetsMatchRecordedMetrics(t *testing.T) {
	recorded := map[string]bool{
		MetricWakeToExec:     true,
		MetricFirstAudio:     true,
		MetricFrameToInsight: true,
	}
	for metric := range Targets {
		if !recorded[metric] {
			t.Errorf("§10 target declared for %q, which nothing records", metric)
		}
	}
	for metric := range recorded {
		if _, ok := Targets[metric]; !ok {
			t.Errorf("metric %q is recorded but has no §10 target", metric)
		}
	}
}
