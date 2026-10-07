package metabolism

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite testdata/wire_v1.ndjson")

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newRecorder(t *testing.T, on bool) (*Recorder, *clock, *UsageTotals, *int64) {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)}
	u := &UsageTotals{}
	var declines int64
	r, err := Open(Options{
		Dir:      filepath.Join(t.TempDir(), "metabolism"),
		Enabled:  on,
		Usage:    func() UsageTotals { return *u },
		Declines: func() int64 { return declines },
		Now:      c.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, c, u, &declines
}

func readRecords(t *testing.T, path string) []Record {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []Record
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec Record
		dec := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("bad line %q: %v", sc.Text(), err)
		}
		out = append(out, rec)
	}
	return out
}

func TestDisabledRecordsNothingAndCreatesNothing(t *testing.T) {
	r, _, _, _ := newRecorder(t, false)
	turn := r.Begin(t.TempDir())
	if turn != nil {
		t.Fatal("Begin returned a turn while disabled")
	}
	turn.AddStep(Step{Tool: "shell"})
	r.Finish(turn, "hello", ProvUserTyped, EndDone)
	r.Outcome("abc", OutcomeUndone, SourceDerived, "")
	if _, err := os.Stat(filepath.Dir(r.Path())); !os.IsNotExist(err) {
		t.Fatal("a disabled recorder created its directory")
	}
	var nilRec *Recorder
	nilRec.Finish(nilRec.Begin("."), "x", ProvUserTyped, EndDone) // must not panic
}

func TestFinishWritesEpisodeAndOutcomes(t *testing.T) {
	r, c, u, declines := newRecorder(t, true)
	*u = UsageTotals{Calls: 10, PromptChars: 1000, ResponseChars: 100}
	turn := r.Begin(t.TempDir())
	turn.AddStep(Step{Tool: "file", Action: "glob", Subject: "**/*.py", OK: true})
	turn.AddStep(Step{Tool: "shell", Subject: "export OPENAI_API_KEY=sk-abcdefghijklmnopqrstuv && make", OK: false, Err: "exit 2"})
	*u = UsageTotals{Calls: 13, PromptChars: 9000, ResponseChars: 700}
	*declines = 1
	c.t = c.t.Add(4 * time.Second)
	r.Finish(turn, "build it", ProvUserVoice, EndFailed)

	recs := readRecords(t, r.Path())
	if len(recs) != 3 {
		t.Fatalf("got %d records, want episode + host outcome + declined", len(recs))
	}
	ep := recs[0].Episode
	if recs[0].Kind != "episode" || ep == nil || ep.ID != turn.ID() {
		t.Fatalf("first record: %+v", recs[0])
	}
	if u := ep.Usage; u.ModelCalls != 3 || u.InputChars != 8000 || u.OutputChars != 600 || u.ByKind != nil {
		t.Errorf("usage delta = %+v", ep.Usage)
	}
	if ep.Request.Provenance != ProvUserVoice || ep.End != EndFailed || ep.Host != HostName {
		t.Errorf("episode fields: %+v", ep)
	}
	if !ep.EndedAt.After(ep.StartedAt) {
		t.Error("ended_at not after started_at")
	}
	if strings.Contains(ep.Steps[1].Subject, "sk-abc") {
		t.Errorf("secret reached disk: %q", ep.Steps[1].Subject)
	}
	if recs[1].Outcome.Kind != OutcomeFailure || recs[1].Outcome.Source != SourceHost {
		t.Errorf("host outcome: %+v", recs[1].Outcome)
	}
	if recs[2].Outcome.Kind != OutcomeDeclined || recs[2].Outcome.EpisodeID != ep.ID {
		t.Errorf("declined outcome: %+v", recs[2].Outcome)
	}
	for _, rec := range recs {
		if rec.V != WireVersion {
			t.Errorf("record without wire version: %+v", rec)
		}
	}
}

func TestUsageResetNeverGoesNegative(t *testing.T) {
	r, _, u, _ := newRecorder(t, true)
	*u = UsageTotals{Calls: 50, PromptChars: 5000}
	turn := r.Begin(t.TempDir())
	*u = UsageTotals{Calls: 1, PromptChars: 10} // /clear mid-turn
	r.Finish(turn, "x", ProvUserTyped, EndDone)
	if got := readRecords(t, r.Path())[0].Episode.Usage; got.ModelCalls != 0 || got.InputChars != 0 {
		t.Fatalf("negative usage leaked: %+v", got)
	}
}

func TestRepeatDetection(t *testing.T) {
	cwd := t.TempDir()
	cases := []struct {
		name       string
		first, sec string
		gap        time.Duration
		want       bool
	}{
		{"same words, different case and punctuation", "Run the tests.", "run the tests", time.Minute, true},
		{"same ask with an extra word", "fix the failing test in parser go", "fix the failing test in parser.go please", time.Minute, true},
		{"overlapping but different", "run the unit tests", "run the integration tests", time.Minute, false},
		{"too late", "run the tests", "run the tests", RepeatWindow + time.Second, false},
		{"different ask", "run the tests", "commit my changes", time.Minute, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, c, _, _ := newRecorder(t, true)
			a := r.Begin(cwd)
			r.Finish(a, tc.first, ProvUserTyped, EndDone)
			c.t = c.t.Add(tc.gap)
			b := r.Begin(cwd)
			r.Finish(b, tc.sec, ProvUserTyped, EndDone)
			got := false
			for _, rec := range readRecords(t, r.Path()) {
				if rec.Outcome != nil && rec.Outcome.Kind == OutcomeRepeated {
					if rec.Outcome.EpisodeID != a.ID() {
						t.Fatalf("repeat labelled the wrong episode")
					}
					got = true
				}
			}
			if got != tc.want {
				t.Fatalf("repeated = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSimilar(t *testing.T) {
	if !similar("please run all the tests now", "run all the tests now please") {
		t.Error("same words reordered should be similar")
	}
	if similar("", "") || similar("ok", "no") {
		t.Error("empty or different short requests are not similar")
	}
}

func TestLateOutcome(t *testing.T) {
	r, _, _, _ := newRecorder(t, true)
	r.Outcome("", OutcomeUndone, SourceDerived, "/undo") // predates recording
	r.Outcome("0199a1b2c3d4e5f6a7b8c9d0e1f2", OutcomeUndone, SourceDerived, "/undo")
	recs := readRecords(t, r.Path())
	if len(recs) != 1 || recs[0].Outcome.Kind != OutcomeUndone {
		t.Fatalf("late outcomes: %+v", recs)
	}
}

func TestSetEnabledTogglesWithoutLosingData(t *testing.T) {
	r, _, _, _ := newRecorder(t, true)
	r.Finish(r.Begin(t.TempDir()), "one", ProvUserTyped, EndDone)
	if err := r.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	r.Finish(r.Begin(t.TempDir()), "two", ProvUserTyped, EndDone)
	if err := r.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	r.Finish(r.Begin(t.TempDir()), "three", ProvUserTyped, EndDone)
	var texts []string
	for _, rec := range readRecords(t, r.Path()) {
		if rec.Episode != nil {
			texts = append(texts, rec.Episode.Request.Text)
		}
	}
	if strings.Join(texts, ",") != "one,three" {
		t.Fatalf("recorded %v, want [one three]", texts)
	}
}

func TestScrub(t *testing.T) {
	cases := map[string]string{
		"export GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123": "export GITHUB_TOKEN=[redacted]",
		"curl -H 'Authorization: Bearer abc.def.ghijklmnop' x":   "curl -H 'Authorization: Bearer [redacted]' x",
		"git clone https://me:hunter2@github.com/o/r":            "git clone https://[redacted]@github.com/o/r",
		"mysql --password=hunter2 -u root":                       "mysql --password=[redacted] -u root",
		"tool --token abcd1234 run":                              "tool --token [redacted] run",
		"echo AKIAABCDEFGHIJKLMNOP":                              "echo [redacted]",
		"go test ./...":                                          "go test ./...",
	}
	for in, want := range cases {
		if got := Scrub(in); got != want {
			t.Errorf("Scrub(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubjectIsBounded(t *testing.T) {
	long := strings.Repeat("é", 400)
	got := cleanSubject(long)
	if len(got) > MaxSubjectBytes+len("…") {
		t.Fatalf("subject not bounded: %d bytes", len(got))
	}
	if !json.Valid([]byte(`"` + got + `"`)) {
		t.Fatal("truncation split a rune")
	}
}

func TestScopeFor(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "my repo")
	sub := filepath.Join(repo, "internal", "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := ScopeFor(sub)
	if s.Level != ScopeProject || !strings.HasPrefix(s.Key, "my-repo-") {
		t.Fatalf("scope in a repo subdirectory = %+v", s)
	}
	if s != ScopeFor(repo) {
		t.Fatal("same repo, different key")
	}
	if strings.Contains(s.Key, string(filepath.Separator)) {
		t.Fatal("project key leaks a path")
	}
	if m := ScopeFor(t.TempDir()); m.Level != ScopeMachine || !strings.HasPrefix(m.Key, "m-") {
		t.Fatalf("scope outside a repo = %+v", m)
	}
}

// TestWireGolden pins the wire format. The same file is copied into the
// Metabolism repository (adapters/ndjson/testdata/helix_wire_v1.ndjson), whose
// ingest test decodes it into the engine's own types. If this test fails, the
// format changed: bump WireVersion and update both copies together.
//
// Version 1 records are still read by the engine, so this file stays pinned
// after the bump to version 2: a v2 encoder writing a record with no v2
// fields must produce exactly the v1 bytes, apart from the version number.
func TestWireGolden(t *testing.T) {
	at := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	recs := []Record{
		{V: 1, Kind: "episode", Episode: &Episode{
			ID: "0199a1b2c3d4e5f6a7b8c9d0e1f2", Host: HostName,
			Scope:     Scope{Level: ScopeProject, Key: "helix-1a2b3c4d"},
			StartedAt: at, EndedAt: at.Add(6 * time.Second),
			Request: Request{Text: "find the python config loader", Provenance: ProvUserTyped},
			Steps: []Step{
				{Tool: "file", Action: "glob", Subject: "**/config*.py", OK: true, Duration: 12 * time.Millisecond},
				{Tool: "shell", Subject: "go build ./...", OK: false, Err: "exit status 1", Duration: time.Second},
			},
			Usage: Usage{ModelCalls: 2, InputChars: 8200, OutputChars: 640},
			End:   EndBudgetExhausted,
			Attrs: map[string]string{"machine": "m-0a1b2c3d"},
		}},
		{V: 1, Kind: "outcome", Outcome: &Outcome{
			ID: "0199a1b2c3d5f6a7b8c9d0e1f2a3", EpisodeID: "0199a1b2c3d4e5f6a7b8c9d0e1f2",
			At: at.Add(6 * time.Second), Kind: OutcomeFailure, Source: SourceHost,
		}},
		{V: 1, Kind: "outcome", Outcome: &Outcome{
			ID: "0199a1b2c3d6a7b8c9d0e1f2a3b4", EpisodeID: "0199a1b2c3d4e5f6a7b8c9d0e1f2",
			At: at.Add(3 * time.Minute), Kind: OutcomeUndone, Source: SourceDerived, Note: "/undo",
		}},
	}
	var buf bytes.Buffer
	for _, r := range recs {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(line, '\n'))
	}
	golden := filepath.Join("testdata", "wire_v1.ndjson")
	if *update {
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("wire format changed.\n got: %s\nwant: %s", buf.String(), want)
	}
}

// TestWireGoldenV2 pins version 2: an episode with lessons delivered and
// withheld, a /lessons forget and a /lessons forget --hard. The engine keeps a byte-identical copy
// (adapters/ndjson/testdata/helix_wire_v2.ndjson).
func TestWireGoldenV2(t *testing.T) {
	at := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	recs := []Record{
		{V: 2, Kind: "episode", Episode: &Episode{
			ID: "0199a1b2c3d4e5f6a7b8c9d0e1f5", Host: HostName,
			Scope:     Scope{Level: ScopeProject, Key: "helix-1a2b3c4d"},
			StartedAt: at, EndedAt: at.Add(6 * time.Second),
			Request:  Request{Text: "build the project", Provenance: ProvUserTyped},
			Exposure: []string{"0199a1b2c3d4e5f6a7b8c9d0e1f3"},
			Withheld: []string{"0199a1b2c3d4e5f6a7b8c9d0e1f4"},
			Steps: []Step{
				{Tool: "file", Action: "read", Subject: "go.mod", OK: true, Duration: 12 * time.Millisecond},
				{Tool: "shell", Subject: "go build ./...", OK: true, Duration: time.Second},
			},
			Usage: Usage{ModelCalls: 1, InputChars: 8200, OutputChars: 640},
			End:   EndDone,
			Attrs: map[string]string{"machine": "m-0a1b2c3d"},
		}},
		{V: 2, Kind: "outcome", Outcome: &Outcome{
			ID: "0199a1b2c3d5f6a7b8c9d0e1f2a5", EpisodeID: "0199a1b2c3d4e5f6a7b8c9d0e1f5",
			At: at.Add(6 * time.Second), Kind: OutcomeSuccess, Source: SourceHost,
		}},
		{V: 2, Kind: "feedback", Feedback: &Feedback{
			ID: "0199a1b2c3d6a7b8c9d0e1f2a3b6", LessonID: "0199a1b2c3d4e5f6a7b8c9d0e1f4",
			At: at.Add(5 * time.Minute), Action: FeedbackForget, Reason: "this repo moved to Bazel",
		}},
		{V: 2, Kind: "feedback", Feedback: &Feedback{
			ID: "0199a1b2c3d7b8c9d0e1f2a3b4c7", LessonID: "0199a1b2c3d4e5f6a7b8c9d0e1f3",
			At: at.Add(6 * time.Minute), Action: FeedbackForgetHard, Reason: "it quotes a private hostname",
		}},
	}
	var buf bytes.Buffer
	for _, r := range recs {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(line, '\n'))
	}
	want, err := os.ReadFile(filepath.Join("testdata", "wire_v2.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("wire v2 changed.\n got: %s\nwant: %s", buf.String(), want)
	}
}

// TestWireGoldenV3 pins version 3 (Metabolism D-031): calls per kind, a
// record per planner round, the critic's verdict, the fallback's cause and
// each step's round. The engine keeps a byte-identical copy
// (adapters/ndjson/testdata/helix_wire_v3.ndjson).
func TestWireGoldenV3(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	recs := []Record{
		{V: WireVersion, Kind: "episode", Episode: &Episode{
			ID: "01a10f00c3d4e5f6a7b8c9d0e1f5", Host: HostName,
			Scope:     Scope{Level: ScopeProject, Key: "helix-1a2b3c4d"},
			StartedAt: at, EndedAt: at.Add(9 * time.Second),
			Request: Request{Text: "where is the planner prompt built?", Provenance: ProvUserTyped},
			Steps: []Step{
				{Tool: "file", Action: "grep", Subject: "BuildPlannerPrompt in internal/ai", OK: true, Duration: 30 * time.Millisecond, Round: 1},
				{Tool: "response", OK: true, Round: 2},
			},
			Usage: Usage{ModelCalls: 3, InputChars: 21000, OutputChars: 900,
				ByKind: map[string]int{"planner": 3}},
			End:   EndDone,
			Attrs: map[string]string{"machine": "m-0a1b2c3d"},
			Plans: []Plan{
				{Round: 1, Prompt: "full", Calls: 2, Result: PlanPlanned, Intent: "multi_step", FirstTool: "file", Steps: 1,
					InputChars: 14000, OutputChars: 300},
				{Round: 2, Prompt: "full", Calls: 1, Result: PlanPlanned, Intent: "chat", FirstTool: "response", Answered: true, Steps: 1,
					InputChars: 7000, OutputChars: 600},
			},
		}},
		{V: WireVersion, Kind: "episode", Episode: &Episode{
			ID: "01a10f01c3d4e5f6a7b8c9d0e1f6", Host: HostName,
			Scope:     Scope{Level: ScopeProject, Key: "helix-1a2b3c4d"},
			StartedAt: at.Add(time.Minute), EndedAt: at.Add(time.Minute + 7*time.Second),
			Request: Request{Text: "fetch the release notes and summarise them", Provenance: ProvUserTyped},
			Usage: Usage{ModelCalls: 3, InputChars: 15000, OutputChars: 1200,
				ByKind: map[string]int{"chat": 1, "critic": 1, "planner": 1}},
			End:      EndDone,
			Attrs:    map[string]string{"machine": "m-0a1b2c3d"},
			Plans:    []Plan{{Round: 1, Prompt: "full", Calls: 1, Result: PlanQuarantined, Intent: "shell", FirstTool: "shell", Steps: 1}},
			Critic:   &Critic{Verdict: "no", Calls: 1},
			Fallback: FallbackQuarantine,
		}},
	}
	var buf bytes.Buffer
	for _, r := range recs {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(line, '\n'))
	}
	path := filepath.Join("testdata", "wire_v3.ndjson")
	if os.Getenv("HELIX_WRITE_GOLDEN") == "1" {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("wire v3 changed.\n got: %s\nwant: %s", buf.String(), want)
	}
}
