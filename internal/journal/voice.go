// internal/journal/voice.go
// Purpose: BlackBox P2.8 — the opt-in voice interaction log. Records what
// Helix HEARD and what it SAID, with the STT provider and confidence that
// produced each transcript, so a user can audit a voice session after the
// fact: which utterances were understood, which were misheard, and what the
// machine did about each one.
//
// Three properties are the whole design, and all three are privacy guarantees
// rather than features (threat V5):
//
//  1. DEFAULT ABSENT. Disabled is not "an empty file" — it is no directory and
//     no file at all. OpenVoiceLog returns a nil log when the feature is off,
//     and a nil *VoiceLog is a working no-op, so no call site needs a guard.
//  2. NO AUDIO, EVER. Only text and metadata are recorded. §7 of the roadmap
//     described this log as "transcripts + audio refs"; there are no audio refs
//     to record, because captured clips are deleted immediately after they are
//     read (P1.7) and frames are never written at all (guardrail #6). Storing a
//     path to a file that no longer exists would be a privacy liability that
//     bought nothing.
//  3. WIPEABLE. It lives under ~/.helix/voice_log/, which /purge deletes.
package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Direction records which way an utterance travelled.
const (
	DirHeard = "heard" // the user spoke, STT transcribed it
	DirSpoke = "spoke" // Helix synthesized and played this text
)

// Outcomes describe what the pipeline DID with a transcript. This is the field
// that makes the log an audit rather than a diary: "heard X" alone cannot
// distinguish an utterance that ran a command from one a policy refused.
const (
	OutcomePlanner    = "planner"     // dispatched to the AI pipeline as a voice turn
	OutcomeCommand    = "command"     // matched a spoken slash-command
	OutcomeKillPhrase = "kill_phrase" // "manual mode" — left live mode
	OutcomeEyesOff    = "eyes_off"    // "turn off your eyes" — closed the camera
	OutcomeRefused    = "refused"     // Voice Risk Policy declined it (ADR-005)
)

// VoiceEntry is one recorded utterance in either direction.
type VoiceEntry struct {
	TS         time.Time `json:"ts"`
	Dir        string    `json:"dir"`
	Text       string    `json:"text"`
	Provider   string    `json:"provider,omitempty"`
	Confidence float64   `json:"confidence,omitempty"`
	Outcome    string    `json:"outcome,omitempty"`
	Note       string    `json:"note,omitempty"`
}

// VoiceLog is the opt-in transcript log. A nil *VoiceLog is the disabled
// state and every method tolerates it.
type VoiceLog struct {
	app *Appender
}

// VoiceLogFile is the log's name inside the voice_log directory.
const VoiceLogFile = "voice.jsonl"

// OpenVoiceLog prepares the log under dir, or returns nil when disabled.
//
// The enabled flag is passed in rather than read from config here so this
// package stays free of config imports — and so the "disabled means untouched
// filesystem" rule is enforced at the one place that could break it.
func OpenVoiceLog(dir string, enabled bool, opts Options) (*VoiceLog, error) {
	if !enabled {
		return nil, nil
	}
	app, err := Open(filepath.Join(dir, VoiceLogFile), opts)
	if err != nil {
		return nil, err
	}
	return &VoiceLog{app: app}, nil
}

// DefaultVoiceLogDir returns ~/.helix/voice_log.
func DefaultVoiceLogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helix", "voice_log"), nil
}

// Enabled reports whether anything is being recorded.
func (v *VoiceLog) Enabled() bool { return v != nil && v.app != nil }

// Path returns the log file path, or "" when disabled.
func (v *VoiceLog) Path() string {
	if !v.Enabled() {
		return ""
	}
	return v.app.Path()
}

// Heard records a transcript and what the pipeline did with it.
func (v *VoiceLog) Heard(text, provider string, confidence float64, outcome string) {
	if !v.Enabled() {
		return
	}
	v.app.Append(VoiceEntry{
		TS:         time.Now(),
		Dir:        DirHeard,
		Text:       Redact(text),
		Provider:   provider,
		Confidence: confidence,
		Outcome:    outcome,
	})
}

// Spoke records a reply that was synthesized and played.
func (v *VoiceLog) Spoke(text string) {
	if !v.Enabled() {
		return
	}
	v.app.Append(VoiceEntry{TS: time.Now(), Dir: DirSpoke, Text: Redact(text)})
}

// Note records something about the session that is neither heard nor spoken —
// a capture failure, a re-arm, a provider switch.
func (v *VoiceLog) Note(note string) {
	if !v.Enabled() {
		return
	}
	v.app.Append(VoiceEntry{TS: time.Now(), Note: Redact(note)})
}

// Tail returns the last n entries (oldest first).
func (v *VoiceLog) Tail(n int) []VoiceEntry {
	if !v.Enabled() {
		return nil
	}
	lines := v.app.Tail(n)
	out := make([]VoiceEntry, 0, len(lines))
	for _, l := range lines {
		var e VoiceEntry
		if err := json.Unmarshal(l, &e); err == nil {
			out = append(out, e)
		}
	}
	return out
}
