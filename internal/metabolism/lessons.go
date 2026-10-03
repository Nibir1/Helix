package metabolism

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file is the delivery half (Metabolism Phase 3). The engine writes the
// lessons it has tested to lessons.json (`metabolism export`); Helix reads
// them, picks the ones that apply to a turn, flips a coin for each, and
// records which were delivered and which withheld on the turn's episode. The
// engine's credit ledger compares the two.
//
// Delivery is opt-in twice over: /lessons on, and recording on, because a
// lesson delivered on an unrecorded turn earns no credit and could never be
// eliminated by evidence.

// LessonsFileName is the delivery file inside the recorder's directory.
const LessonsFileName = "lessons.json"

// ForgottenFileName lists the lessons the user forgot here, so they stop
// at once, before the engine has ingested the forget.
const ForgottenFileName = "forgotten.json"

// LessonsVersion is the delivery file version this build reads. The engine
// pins the format with a golden file both repositories keep
// (testdata/lessons_v1.json).
const LessonsVersion = 1

// Delivery is the engine's delivery file (engine: deliver.File).
type Delivery struct {
	V           int               `json:"v"`
	GeneratedAt time.Time         `json:"generated_at"`
	Lessons     []DeliveredLesson `json:"lessons"`
}

// DeliveredLesson is one lesson Helix may deliver. Text goes into the
// planner's prompt as data; Evidence and Credit are shown to the user by
// /lessons and never reach a prompt.
type DeliveredLesson struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	State string `json:"state"`
	Kind  string `json:"kind"`
	Scope Scope  `json:"scope"`
	// P is the probability of delivering the lesson on a turn it applies to.
	P        float64 `json:"p"`
	Evidence string  `json:"evidence,omitempty"`
	Credit   string  `json:"credit,omitempty"`
}

// Lesson states the engine delivers.
const (
	StateTrial    = "trial"
	StateAccepted = "accepted"
)

// ReadDelivery reads a delivery file strictly: unknown fields, another
// version, or a lesson in a state the engine never delivers is an error, and
// a broken file delivers nothing rather than something half-read.
func ReadDelivery(path string) (Delivery, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Delivery{}, err
	}
	var d Delivery
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return Delivery{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if d.V != LessonsVersion {
		return Delivery{}, fmt.Errorf("%s: version %d, this Helix reads %d", filepath.Base(path), d.V, LessonsVersion)
	}
	for _, l := range d.Lessons {
		if l.ID == "" || strings.TrimSpace(l.Text) == "" || (l.State != StateTrial && l.State != StateAccepted) ||
			l.P < 0 || l.P > 1 {
			return Delivery{}, fmt.Errorf("%s: lesson %q is not deliverable", filepath.Base(path), l.ID)
		}
	}
	return d, nil
}

// Applies reports whether a lesson with scope s applies to a turn in scope
// at on machine: a global lesson everywhere, a machine lesson on that
// machine, a project lesson in that project. The engine's types.Scope.Covers
// is the same rule.
func (s Scope) Applies(at Scope, machine string) bool {
	switch s.Level {
	case ScopeProject:
		return s == at
	case ScopeMachine:
		return s.Key == machine || s == at
	case ScopeGlobal:
		return true
	}
	return false
}

// ScopeGlobal is a lesson that applies everywhere.
const ScopeGlobal = "global"

// Budget bounds one turn's lessons. Size measures a lesson as the prompt
// renders it (0 means it renders to nothing and is skipped), so what is
// selected is exactly what fits.
type Budget struct {
	MaxLessons int
	MaxBytes   int
	Size       func(text string) int
}

// Selection is one turn's lessons.
type Selection struct {
	Delivered []DeliveredLesson
	Withheld  []string
}

// IDs lists the delivered lessons' IDs.
func (s Selection) IDs() []string {
	out := make([]string, 0, len(s.Delivered))
	for _, l := range s.Delivered {
		out = append(out, l.ID)
	}
	return out
}

// Select picks a turn's lessons the way the engine's deliver.Select does:
//
//  1. keep the lessons that apply to the turn and were not forgotten here;
//  2. fill the budget with accepted lessons in file order, then trial
//     lessons in a fresh random order (otherwise trial lessons past the
//     budget would never be measured);
//  3. flip a coin with each budgeted lesson's P: delivered or withheld.
//
// A lesson that did not fit is in neither list, so every withheld turn is
// one where the lesson would have been delivered but for the coin.
func Select(d Delivery, at Scope, machine string, forgotten map[string]bool, b Budget, r *rand.Rand) Selection {
	var accepted, trial []DeliveredLesson
	for _, l := range d.Lessons {
		if forgotten[l.ID] || !l.Scope.Applies(at, machine) {
			continue
		}
		if l.State == StateAccepted {
			accepted = append(accepted, l)
		} else {
			trial = append(trial, l)
		}
	}
	r.Shuffle(len(trial), func(i, j int) { trial[i], trial[j] = trial[j], trial[i] })

	var s Selection
	n, used := 0, 0
	for _, l := range append(accepted, trial...) {
		size := len(l.Text) + 3
		if b.Size != nil {
			size = b.Size(l.Text)
		}
		if size <= 0 || (b.MaxLessons > 0 && n == b.MaxLessons) || (b.MaxBytes > 0 && used+size > b.MaxBytes) {
			continue
		}
		n, used = n+1, used+size
		if r.Float64() < l.P {
			s.Delivered = append(s.Delivered, l)
		} else {
			s.Withheld = append(s.Withheld, l.ID)
		}
	}
	return s
}

// SetLessons records which lessons the turn delivered and withheld.
func (t *Turn) SetLessons(delivered, withheld []string) {
	if t == nil {
		return
	}
	t.exposure, t.withheld = delivered, withheld
}

// LessonsPath is the delivery file the engine writes.
func (r *Recorder) LessonsPath() string {
	if r == nil {
		return ""
	}
	return filepath.Join(r.dir, LessonsFileName)
}

// Delivery reads the delivery file. A missing file is an empty delivery, not
// an error: it is the normal state before the engine has tested anything.
func (r *Recorder) Delivery() (Delivery, error) {
	if r == nil {
		return Delivery{}, nil
	}
	d, err := ReadDelivery(r.LessonsPath())
	if errors.Is(err, os.ErrNotExist) {
		return Delivery{V: LessonsVersion}, nil
	}
	return d, err
}

// Forgotten maps each lesson forgotten here to the reason given.
func (r *Recorder) Forgotten() map[string]string {
	out := map[string]string{}
	if r == nil {
		return out
	}
	data, err := os.ReadFile(filepath.Join(r.dir, ForgottenFileName))
	if err != nil {
		return out
	}
	_ = json.Unmarshal(data, &out)
	return out
}

// Forget stops delivering a lesson here at once and, when recording is on,
// records the forget for the engine, which eliminates the lesson at the next
// ingest. It returns whether the engine will hear of it.
func (r *Recorder) Forget(lessonID, reason string) (recorded bool, err error) {
	return r.forget(lessonID, reason, false)
}

// ForgetHard is Forget, and also deletes the lesson's text from the delivery
// file at once. The engine deletes it from its journal, with an audit record
// that keeps a hash of each deleted record and never its content, at the
// next ingest (Metabolism D-028).
func (r *Recorder) ForgetHard(lessonID, reason string) (recorded bool, err error) {
	return r.forget(lessonID, reason, true)
}

func (r *Recorder) forget(lessonID, reason string, hard bool) (recorded bool, err error) {
	if r == nil {
		return false, errors.New("metabolism is not available in this session")
	}
	lessonID, reason = strings.TrimSpace(lessonID), cleanText(strings.TrimSpace(reason))
	if lessonID == "" || reason == "" {
		return false, errors.New("a forget needs a lesson ID and a reason")
	}
	forgotten := r.Forgotten()
	forgotten[lessonID] = reason
	if err := writePrivateJSON(filepath.Join(r.dir, ForgottenFileName), forgotten); err != nil {
		return false, err
	}
	action := FeedbackForget
	if hard {
		action = FeedbackForgetHard
		if err := r.dropFromDelivery(lessonID); err != nil {
			return false, err
		}
	}
	if !r.Enabled() {
		return false, nil
	}
	now := r.now()
	r.write(Record{V: WireVersion, Kind: "feedback", Feedback: &Feedback{
		ID: NewID(now), LessonID: lessonID, At: now.UTC(), Action: action, Reason: reason,
	}})
	return true, nil
}

// dropFromDelivery rewrites the delivery file without the lesson, atomically
// and 0600, in the format the engine writes it.
func (r *Recorder) dropFromDelivery(lessonID string) error {
	d, err := ReadDelivery(r.LessonsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	kept := d.Lessons[:0]
	for _, l := range d.Lessons {
		if l.ID != lessonID {
			kept = append(kept, l)
		}
	}
	if len(kept) == len(d.Lessons) {
		return nil
	}
	d.Lessons = kept
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(r.LessonsPath(), append(data, '\n'))
}

// writePrivateJSON replaces path atomically with v, 0600 in a 0700
// directory.
func writePrivateJSON(path string, v map[string]string) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(data, '\n'))
}

// writePrivateFile replaces path atomically with data, 0600 in a 0700
// directory.
func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".metabolism-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
