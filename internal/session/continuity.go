// internal/session/continuity.go
//
// Purpose: what Helix was doing, written down so a restart can pick it up.
//
// Conversation memory already survives a restart — RingStore writes
// session.json on every turn and reloads it at boot — and the voice/manual mode
// already survives it, in cfg.UserPrefs.VoiceMode. What did NOT survive is
// everything between those two: which directory you were in, which model was
// answering, which task was in progress, and the plain fact that the restart
// happened at all. A shell that comes back silently in the right mode, in the
// wrong directory, with no word about the work it was in the middle of, has
// technically resumed and practically forgotten.
//
// This record is deliberately SMALL and deliberately NOT a second copy of the
// conversation. It points at state that already persists and carries only what
// would otherwise be lost. Duplicating the transcript here would mean two stores
// of the same turns that can disagree — and would put a second copy of
// everything you said on disk, which the voice threat model (V5) exists to
// prevent.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ContinuityVersion is the record's schema version.
//
// Checked on load rather than assumed: an older Helix reading a newer record
// should ignore it and start clean, which is a worse resume and never a wrong
// one.
const ContinuityVersion = 1

// ContinuityMaxAge bounds how long a record stays meaningful.
//
// A reboot record is about the seconds between two processes. One found a week
// later describes a machine that has moved on — resuming into it would restore a
// working directory that may not exist and announce a task the user finished by
// hand days ago. Stale is silently discarded, not honoured.
const ContinuityMaxAge = 12 * time.Hour

// ModeVoice and ModeManual are the two shells Helix can come back as.
const (
	ModeVoice  = "voice"
	ModeManual = "manual"
)

// Continuity is the state a restart carries across.
type Continuity struct {
	Version int       `json:"version"`
	At      time.Time `json:"at"`

	// Reason says who asked. Free text, shown on the way back in, because
	// "Helix restarted" with no cause reads like a crash.
	Reason string `json:"reason"`

	// Update is what the restart's version check concluded, in the user's
	// terms: "already on the newest release (1.5.0)", "installed 1.6.0", or
	// "not checked — update.check is off".
	//
	// Carried across the restart because the question is asked AFTER it. Left
	// out, the only thing that could answer "did you download the latest
	// binaries?" was the model's guess at what the program had done — and it
	// guessed, plausibly and without evidence. A fact the new process can read
	// is the difference between reporting and inventing.
	Update string `json:"update,omitempty"`

	// Mode is the shell to come back as: ModeVoice or ModeManual.
	//
	// Carried explicitly rather than left to cfg.UserPrefs.VoiceMode, even
	// though that field also persists it. The preference records what you last
	// CHOSE; this records what was true at the instant of the restart, and the
	// two differ in exactly the case that matters — a session that entered
	// voice mode without persisting, or left it without persisting, would come
	// back as the other thing.
	Mode string `json:"mode"`

	// Cwd is the working directory to return to.
	Cwd string `json:"cwd,omitempty"`

	// Provider and Model name the brain that was answering.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`

	// Doing is the one-line human summary of the work in flight.
	Doing string `json:"doing,omitempty"`

	// Tasks are the in-progress task texts, so the resume can name them.
	Tasks []string `json:"tasks,omitempty"`

	// LastExchange is a heavily truncated echo of the final turn, and the ONLY
	// conversation content stored here. It exists so the resume can say what
	// was being discussed without loading and rendering the whole ring.
	LastExchange string `json:"last_exchange,omitempty"`
}

// continuityExcerptMax bounds the stored excerpt.
//
// Small on purpose. This is a reminder, not a transcript: the transcript is in
// session.json, governed by /memory clear, and a second unbounded copy here
// would be a privacy surface with no control attached to it.
const continuityExcerptMax = 240

// ContinuityPath returns ~/.helix/reboot.json.
func ContinuityPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helix", "reboot.json"), nil
}

// SaveContinuity writes the record at the default path.
func SaveContinuity(c Continuity) error {
	path, err := ContinuityPath()
	if err != nil {
		return err
	}
	return SaveContinuityAt(path, c)
}

// SaveContinuityAt writes the record to an explicit path (tests).
//
// 0600 inside a 0700 directory, matching every other file Helix writes that
// can carry a fragment of what you said.
func SaveContinuityAt(path string, c Continuity) error {
	c.Version = ContinuityVersion
	if c.At.IsZero() {
		return fmt.Errorf("continuity record has no timestamp")
	}
	c.LastExchange = truncateExcerpt(c.LastExchange)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal continuity: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write continuity: %w", err)
	}
	return nil
}

// LoadContinuity reads and CONSUMES the record at the default path.
func LoadContinuity(now time.Time) (Continuity, bool) {
	path, err := ContinuityPath()
	if err != nil {
		return Continuity{}, false
	}
	return LoadContinuityAt(path, now)
}

// LoadContinuityAt reads and CONSUMES the record at an explicit path.
//
// Consuming is the point: the record describes ONE restart. Left on disk it
// would announce the same resume on every subsequent boot, and a shell that
// claims to be picking up where it left off every single morning is telling you
// nothing. It is deleted whether or not it turned out to be usable, so a
// corrupt or stale record cannot wedge the greeting permanently.
//
// Args: now: the clock, injected so the age rule is testable.
// Returns: the record and whether it is usable.
// Complexity: O(size of the record).
func LoadContinuityAt(path string, now time.Time) (Continuity, bool) {
	data, err := os.ReadFile(path)
	// Removed before it is trusted, not after: a record that panics a parser or
	// fails a check must not be read again on the next boot.
	_ = os.Remove(path)
	if err != nil {
		return Continuity{}, false
	}

	var c Continuity
	if err := json.Unmarshal(data, &c); err != nil {
		// A corrupt record is not an error worth reporting to the user. The
		// cost of ignoring it is a silent normal start; the cost of surfacing
		// it is a scary message about a file nobody asked about.
		return Continuity{}, false
	}
	if c.Version != ContinuityVersion || c.At.IsZero() {
		return Continuity{}, false
	}
	if now.Sub(c.At) > ContinuityMaxAge || c.At.After(now.Add(time.Hour)) {
		// Too old to describe this machine, or stamped in the future — a clock
		// change, which makes the age rule meaningless rather than generous.
		return Continuity{}, false
	}
	return c, true
}

// ClearContinuity removes any pending record.
//
// Used when a restart is abandoned after the record was already written, so a
// reboot that did not happen cannot announce itself on the next ordinary start.
func ClearContinuity() {
	if path, err := ContinuityPath(); err == nil {
		_ = os.Remove(path)
	}
}

// Resumable reports whether the record has anything worth saying out loud.
//
// A record with a mode and nothing else is still worth HONOURING — it is what
// puts you back in voice mode — but it is not worth a paragraph. The greeting
// checks this so a restart with no work in flight stays quiet.
func (c Continuity) Resumable() bool {
	return c.Doing != "" || len(c.Tasks) > 0 || c.LastExchange != ""
}

// truncateExcerpt bounds stored conversation text on a rune boundary.
//
// The boundary matters: a severed UTF-8 sequence makes the JSON line
// unparseable, and an unparseable record is silently dropped — so a careless
// byte-slice here would lose exactly the long exchange someone wanted back.
func truncateExcerpt(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= continuityExcerptMax {
		return s
	}
	return strings.TrimSpace(string(r[:continuityExcerptMax])) + "…"
}
