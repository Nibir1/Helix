package metabolism

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// The delivery file is a contract with the engine, which keeps a
// byte-identical copy (deliver/testdata/lessons_v1.json): it decodes
// strictly, and encoding it again gives the same bytes.
func TestDeliveryGolden(t *testing.T) {
	path := filepath.Join("testdata", "lessons_v1.json")
	d, err := ReadDelivery(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Lessons) != 2 || d.Lessons[0].State != StateAccepted || d.Lessons[1].Scope.Key != "helix-1a2b3c4d" {
		t.Fatalf("decoded %+v", d)
	}
	again, _ := json.MarshalIndent(d, "", "  ")
	want, _ := os.ReadFile(path)
	if !bytes.Equal(append(again, '\n'), want) {
		t.Fatalf("encoding drifted:\n%s", again)
	}
}

// A broken or unexpected file delivers nothing rather than something
// half-read: an unknown field, another version, a state the engine never
// delivers, a probability outside [0, 1].
func TestReadDeliveryRefusesWhatItCannotTrust(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"version": `{"v":2,"generated_at":"2026-10-03T09:00:00Z","lessons":[]}`,
		"field":   `{"v":1,"generated_at":"2026-10-03T09:00:00Z","lessons":[],"authority":"system"}`,
		"state":   `{"v":1,"generated_at":"2026-10-03T09:00:00Z","lessons":[{"id":"a","text":"x","state":"candidate","kind":"fact","scope":{"level":"global"},"p":1}]}`,
		"p":       `{"v":1,"generated_at":"2026-10-03T09:00:00Z","lessons":[{"id":"a","text":"x","state":"trial","kind":"fact","scope":{"level":"global"},"p":2}]}`,
	} {
		p := filepath.Join(dir, name+".json")
		_ = os.WriteFile(p, []byte(body), 0o600)
		if _, err := ReadDelivery(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestScopeAppliesLikeTheEngine(t *testing.T) {
	here := Scope{Level: ScopeProject, Key: "helix-1"}
	cases := []struct {
		lesson Scope
		want   bool
	}{
		{Scope{Level: ScopeGlobal}, true},
		{Scope{Level: ScopeMachine, Key: "m-1"}, true},
		{Scope{Level: ScopeMachine, Key: "m-2"}, false},
		{Scope{Level: ScopeProject, Key: "helix-1"}, true},
		{Scope{Level: ScopeProject, Key: "other-2"}, false},
		{Scope{Level: "galaxy"}, false},
	}
	for _, c := range cases {
		if got := c.lesson.Applies(here, "m-1"); got != c.want {
			t.Errorf("%+v applies = %v", c.lesson, got)
		}
	}
}

func TestSelectScopeBudgetCoinAndForgotten(t *testing.T) {
	here := Scope{Level: ScopeProject, Key: "p"}
	d := Delivery{V: 1, Lessons: []DeliveredLesson{
		{ID: "always", Text: "a", State: StateAccepted, P: 1, Scope: Scope{Level: ScopeGlobal}},
		{ID: "never", Text: "b", State: StateAccepted, P: 0, Scope: Scope{Level: ScopeMachine, Key: "m"}},
		{ID: "elsewhere", Text: "c", State: StateAccepted, P: 1, Scope: Scope{Level: ScopeProject, Key: "q"}},
		{ID: "forgotten", Text: "d", State: StateAccepted, P: 1, Scope: Scope{Level: ScopeGlobal}},
		{ID: "over", Text: "e", State: StateAccepted, P: 1, Scope: Scope{Level: ScopeGlobal}},
	}}
	s := Select(d, here, "m", map[string]bool{"forgotten": true}, Budget{MaxLessons: 2}, rand.New(rand.NewPCG(1, 2)))
	if ids := s.IDs(); len(ids) != 1 || ids[0] != "always" || len(s.Withheld) != 1 || s.Withheld[0] != "never" {
		t.Fatalf("selection %+v", s)
	}

	// A lesson that renders to nothing is skipped, and the byte budget uses
	// the rendered size.
	size := func(text string) int {
		if text == "empty" {
			return 0
		}
		return 100
	}
	d = Delivery{V: 1, Lessons: []DeliveredLesson{
		{ID: "e", Text: "empty", State: StateAccepted, P: 1, Scope: Scope{Level: ScopeGlobal}},
		{ID: "a", Text: "a", State: StateAccepted, P: 1, Scope: Scope{Level: ScopeGlobal}},
		{ID: "b", Text: "b", State: StateAccepted, P: 1, Scope: Scope{Level: ScopeGlobal}},
	}}
	s = Select(d, here, "m", nil, Budget{MaxBytes: 150, Size: size}, rand.New(rand.NewPCG(1, 2)))
	if ids := s.IDs(); len(ids) != 1 || ids[0] != "a" {
		t.Fatalf("byte budget: %+v", s)
	}

	// Trial lessons share a budget they do not all fit in.
	d = Delivery{V: 1}
	for _, id := range []string{"t1", "t2", "t3"} {
		d.Lessons = append(d.Lessons, DeliveredLesson{ID: id, Text: id, State: StateTrial, P: 1, Scope: Scope{Level: ScopeGlobal}})
	}
	seen := map[string]int{}
	r := rand.New(rand.NewPCG(3, 4))
	for range 300 {
		for _, id := range Select(d, here, "m", nil, Budget{MaxLessons: 1}, r).IDs() {
			seen[id]++
		}
	}
	if seen["t1"] < 50 || seen["t2"] < 50 || seen["t3"] < 50 {
		t.Fatalf("budget share: %v", seen)
	}
}

// The episode carries what was delivered and withheld.
func TestFinishRecordsDeliveredAndWithheldLessons(t *testing.T) {
	r, _, _, _ := newRecorder(t, true)
	turn := r.Begin(t.TempDir())
	turn.SetLessons([]string{"L1"}, []string{"L2"})
	r.Finish(turn, "build it", ProvUserTyped, EndDone)
	recs := readRecords(t, r.Path())
	ep := recs[0].Episode
	if recs[0].V != WireVersion || len(ep.Exposure) != 1 || ep.Exposure[0] != "L1" || len(ep.Withheld) != 1 || ep.Withheld[0] != "L2" {
		t.Fatalf("episode %+v", ep)
	}
}

// A forget takes effect here at once, and reaches the engine as a feedback
// record only while recording is on.
func TestForget(t *testing.T) {
	r, _, _, _ := newRecorder(t, true)
	if _, err := r.Forget("L1", ""); err == nil {
		t.Fatal("a forget without a reason was accepted")
	}
	recorded, err := r.Forget("L1", "this repo moved to Bazel, key sk-abcdefghijklmnopqrstuvwx")
	if err != nil || !recorded {
		t.Fatalf("forget: %v %v", recorded, err)
	}
	if got := r.Forgotten()["L1"]; got == "" || bytes.Contains([]byte(got), []byte("sk-abc")) {
		t.Fatalf("forgotten reason %q (must be kept, with secrets masked)", got)
	}
	fi, err := os.Stat(filepath.Join(r.dir, ForgottenFileName))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("forgotten file: %v %v", err, fi)
	}
	recs := readRecords(t, r.Path())
	fb := recs[len(recs)-1].Feedback
	if fb == nil || fb.LessonID != "L1" || fb.Action != FeedbackForget {
		t.Fatalf("feedback record %+v", recs[len(recs)-1])
	}

	off, _, _, _ := newRecorder(t, false)
	recorded, err = off.Forget("L2", "wrong")
	if err != nil || recorded || off.Forgotten()["L2"] == "" {
		t.Fatalf("forget while recording is off: %v %v %v", recorded, err, off.Forgotten())
	}
	if _, err := os.Stat(off.Path()); !os.IsNotExist(err) {
		t.Fatal("a forget while recording is off created the episode file")
	}
}

func TestMissingDeliveryIsEmpty(t *testing.T) {
	r, _, _, _ := newRecorder(t, true)
	d, err := r.Delivery()
	if err != nil || len(d.Lessons) != 0 {
		t.Fatalf("missing delivery file: %v %+v", err, d)
	}
}
