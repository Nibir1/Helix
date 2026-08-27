// internal/session/continuity_test.go
// Purpose: the record a restart carries has to survive the round trip, describe
// only one restart, and never outlive its usefulness.
package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContinuityRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reboot.json")
	now := time.Now()
	want := Continuity{
		At:           now,
		Reason:       "you asked out loud",
		Mode:         ModeVoice,
		Cwd:          "/somewhere/real",
		Provider:     "gemini",
		Model:        "gemini-3.7-flash",
		Doing:        "working on: wire up the parser",
		Tasks:        []string{"wire up the parser"},
		LastExchange: "how far did we get",
	}
	if err := SaveContinuityAt(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, ok := LoadContinuityAt(path, now.Add(time.Second))
	if !ok {
		t.Fatal("a record written a second ago must load")
	}
	if got.Mode != ModeVoice || got.Cwd != want.Cwd || got.Provider != want.Provider ||
		got.Model != want.Model || got.Doing != want.Doing ||
		got.LastExchange != want.LastExchange || got.Reason != want.Reason {
		t.Errorf("record did not survive the round trip: %+v", got)
	}
	if len(got.Tasks) != 1 || got.Tasks[0] != "wire up the parser" {
		t.Errorf("tasks did not survive: %v", got.Tasks)
	}
}

// The record describes ONE restart. Left on disk it would announce the same
// resume on every boot from then on, and a shell that claims to be picking up
// where it left off every morning is telling you nothing.
func TestContinuityIsConsumedByReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reboot.json")
	if err := SaveContinuityAt(path, Continuity{At: time.Now(), Mode: ModeManual}); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadContinuityAt(path, time.Now()); !ok {
		t.Fatal("the first read must succeed")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the record must be deleted once read")
	}
	if _, ok := LoadContinuityAt(path, time.Now()); ok {
		t.Error("a second read must find nothing")
	}
}

// A record that cannot be trusted is discarded AND removed, so it can never
// wedge the greeting permanently.
func TestUnusableRecordsAreDiscardedAndDeleted(t *testing.T) {
	now := time.Now()
	cases := map[string]func(dir string) string{
		"corrupt json": func(dir string) string {
			p := filepath.Join(dir, "a.json")
			_ = os.WriteFile(p, []byte("{not json"), 0o600)
			return p
		},
		"wrong version": func(dir string) string {
			p := filepath.Join(dir, "b.json")
			_ = os.WriteFile(p, []byte(`{"version":999,"at":"`+
				now.Format(time.RFC3339)+`","mode":"manual"}`), 0o600)
			return p
		},
		"no timestamp": func(dir string) string {
			p := filepath.Join(dir, "c.json")
			_ = os.WriteFile(p, []byte(`{"version":1,"mode":"manual"}`), 0o600)
			return p
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			path := setup(t.TempDir())
			if _, ok := LoadContinuityAt(path, now); ok {
				t.Error("an unusable record must not be honoured")
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("an unusable record must still be removed, or it is read forever")
			}
		})
	}
}

// A record found a week later describes a machine that has moved on: it would
// restore a directory that may not exist and announce a task finished by hand
// days ago.
func TestStaleAndFutureRecordsAreIgnored(t *testing.T) {
	dir := t.TempDir()

	stale := filepath.Join(dir, "stale.json")
	if err := SaveContinuityAt(stale, Continuity{At: time.Now(), Mode: ModeManual}); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadContinuityAt(stale, time.Now().Add(ContinuityMaxAge+time.Minute)); ok {
		t.Error("a record older than the max age must be ignored")
	}

	// A clock change makes the age rule meaningless rather than generous.
	future := filepath.Join(dir, "future.json")
	if err := SaveContinuityAt(future, Continuity{
		At: time.Now().Add(48 * time.Hour), Mode: ModeManual,
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadContinuityAt(future, time.Now()); ok {
		t.Error("a record stamped in the future must be ignored")
	}
}

// The excerpt is a reminder, not a transcript: session.json holds the
// conversation and /memory clear governs it, so a second unbounded copy here
// would be a privacy surface with no control attached.
func TestExcerptIsBoundedOnARuneBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reboot.json")
	long := strings.Repeat("héllo wörld ", 200) // multi-byte on purpose
	if err := SaveContinuityAt(path, Continuity{
		At: time.Now(), Mode: ModeManual, LastExchange: long,
	}); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadContinuityAt(path, time.Now())
	if !ok {
		// A severed UTF-8 sequence makes the JSON unparseable and the whole
		// record is silently dropped — which would lose exactly the long
		// exchange someone wanted back.
		t.Fatal("a long excerpt must not make the record unreadable")
	}
	if n := len([]rune(got.LastExchange)); n > continuityExcerptMax+1 {
		t.Errorf("excerpt is %d runes, over the %d bound", n, continuityExcerptMax)
	}
	if !strings.HasPrefix(got.LastExchange, "héllo wörld") {
		t.Errorf("excerpt lost its start: %q", got.LastExchange)
	}
}

// A bare mode carry-over is worth honouring and not worth a paragraph.
func TestResumableDistinguishesWorkFromABareMode(t *testing.T) {
	if (Continuity{Mode: ModeVoice}).Resumable() {
		t.Error("a record with only a mode has nothing to announce")
	}
	for name, c := range map[string]Continuity{
		"a task":         {Tasks: []string{"x"}},
		"a summary":      {Doing: "mid-conversation"},
		"a last message": {LastExchange: "what were we doing"},
	} {
		if !c.Resumable() {
			t.Errorf("%s should be worth announcing", name)
		}
	}
}

// The file may carry a fragment of what you said, so it gets the same
// permissions as everything else that can.
func TestContinuityFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "reboot.json")
	if err := SaveContinuityAt(path, Continuity{At: time.Now(), Mode: ModeManual}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("record mode = %o, want 600", perm)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory mode = %o, want 700", perm)
	}
}
