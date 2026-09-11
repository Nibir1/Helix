// cmd/helix/listen_mode.go
// Purpose: the one place that knows which of Helix's three listening states is
// current, and the one door every transition goes through.
//
// WHY A STATE AND NOT A BOOL. `voiceModeActive` could only say "live or not",
// and there are three distinct things a user means:
//
//	STANDBY  wake detection only, nothing transcribed. Keyboard live.
//	AWAKE    transcribing every turn, no re-waking. Keyboard live.
//	MANUAL   microphone CLOSED. Keyboard live.
//
// With a bool, "manual mode" and "go to sleep" had nowhere different to go, so
// they were the same list of phrases calling the same function — and since
// leaving live mode left the wake word on, the energy detector fired on the
// tail of the very sentence that asked to be left alone and put the user
// straight back. That loop was the reported bug, and it was not a tuning
// problem: there was no state in which the microphone was shut.
//
// WHY ONE DOOR. Each of the three states is reached from several places —
// typed commands, spoken phrases, a wake event, a restored session, a restart.
// Those doors had drifted: `blackBoxOn` opened the camera and started the
// companion, `enterVoiceMode` did neither, and Phase 13 records what that cost
// ("the same mode reached by a different door, behaving differently"). So
// setListenMode owns the UNION of every entry and exit effect, in a fixed
// order, and the callers become names for a target and a cause.
//
// WHY AN ATOMIC. The mode is not only read by the REPL. companionState.look
// reads it from a background goroutine while the REPL writes it — an
// unsynchronised read of a plain bool that `go test -race` never caught
// because nothing in the suite drives the companion loop across a mode switch.
// An int32 in an atomic costs nothing on the read path and removes the race.
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"helix/internal/audio"
	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/input"
	"helix/internal/shell"
	"helix/internal/speech"
)

// listenMode is which of the three states Helix is in.
type listenMode int32

const (
	// modeManual: the microphone is closed. Typing only. This is the ADR-005
	// safety valve, and it is the state an explicit `"enabled": false` means.
	modeManual listenMode = iota

	// modeStandby: wake detection only — chunks are scored and discarded,
	// nothing is transcribed. The keyboard is live. Helix starts here.
	modeStandby

	// modeAwake: a conversation is happening. Every turn is transcribed with
	// no re-waking in between, and the keyboard stays live so a line can be
	// typed mid-conversation.
	modeAwake
)

// String names the state as the user sees it.
func (m listenMode) String() string {
	switch m {
	case modeAwake:
		return "awake"
	case modeStandby:
		return "standby"
	default:
		return "manual"
	}
}

// modeCause says what asked for a transition. It selects the wording, whether
// the change is persisted, and — for the MANUAL exits — whether it is allowed
// at all.
type modeCause int

const (
	// causeTyped: a typed command. The only cause that may OPEN the microphone
	// from MANUAL (ADR-005: voice may reduce what is collected, never increase
	// it).
	causeTyped modeCause = iota

	// causeSpoken: a phrase in a transcript.
	causeSpoken

	// causeWake: a wake event. Speaks the acknowledgement.
	causeWake

	// causeStandDown: the inactivity fallback out of AWAKE.
	causeStandDown

	// causeRestore: a persisted mode being restored at startup or across a
	// restart. Announces, but does not re-persist what it just read.
	causeRestore

	// causeQuiesce: teardown before exec'ing a new binary. Runs every effect
	// and says NOTHING — the "keyboard · /blackbox on goes live again" line is
	// a lie when the process is about to be replaced.
	causeQuiesce
)

// persists reports whether a transition from this cause should be written to
// disk. A restore is reading the preference back, and a quiesce is not a
// preference at all.
func (c modeCause) persists() bool {
	return c != causeRestore && c != causeQuiesce
}

// announces reports whether the transition prints or speaks anything.
func (c modeCause) announces() bool { return c != causeQuiesce }

var (
	modeCur atomic.Int32 // listenMode; the session's truth after startup
	modeMu  sync.Mutex   // serialises transitions, not reads
)

// currentMode reports the current listening state.
func currentMode() listenMode { return listenMode(modeCur.Load()) }

// isAwake reports whether a conversation is in progress. This is the successor
// to every `if voiceModeActive` read.
func isAwake() bool { return currentMode() == modeAwake }

// resolveInitialMode reads the config ONCE, at startup, to decide where to
// begin. After this the enum is the session's truth and only setListenMode
// writes config back; nothing re-derives the mode from disk mid-session.
//
// The three states map onto the two pointers that already exist rather than
// onto a new key, because `wake_word.enabled: false` is already the documented
// opt-out and already means "the microphone is closed" — it just had no name.
// A third persisted field encoding a state derivable from two existing ones is
// the drift shape this repo has paid for four times.
func resolveInitialMode() listenMode {
	// A persisted live session wins over the wake config, preserving the
	// precedence initVoiceMode already had: a restored voice session entered
	// live mode regardless of the wake word's state.
	if cfg.UserPrefs.VoiceMode && voiceEntryPreflight() == nil {
		return modeAwake
	}
	if !cfg.Speech.WakeWord.Listening() {
		return modeManual
	}
	return modeStandby
}

// setListenMode moves to target and runs every effect that state change
// implies. Returns false when the transition was refused, having changed
// nothing.
//
// Refusals are real and there are two: a target of AWAKE with no usable
// microphone (nothing else must be switched on in that case — the ordering is
// why the preflight runs first), and any attempt to OPEN the microphone from
// MANUAL by voice. The second is ADR-005's asymmetry, and putting it here
// rather than only in the command guard means it holds for every door.
func setListenMode(target listenMode, cause modeCause) bool {
	modeMu.Lock()
	defer modeMu.Unlock()

	from := currentMode()
	if from == target {
		return true
	}

	// A closed microphone may only be reopened from the keyboard. `manual mode`
	// is the state a user reaches when they want the mic shut; a spoken phrase
	// must never be able to undo it, because a transcript carries user
	// authority with no proof of who spoke.
	if from == modeManual && target != modeManual &&
		cause != causeTyped && cause != causeRestore {
		if cause.announces() {
			uiWarn("typed only", "the microphone is closed — /blackbox wake on reopens it")
		}
		return false
	}

	if target == modeAwake {
		if err := voiceEntryPreflight(); err != nil {
			if cause.announces() {
				uiFail("cannot go live", err.Error())
			}
			return false
		}
	}

	switch target {
	case modeAwake:
		enterAwakeLocked(cause)
	case modeStandby:
		enterStandbyLocked(from, cause)
	case modeManual:
		enterManualLocked(from, cause)
	}
	modeCur.Store(int32(target))
	return true
}

// enterAwakeLocked opens a conversation. Caller holds modeMu.
//
// The camera decision comes BEFORE the banner because the banner reports it —
// enabling it afterwards printed "SIGHT • off" and then "eyes enabled" two
// lines later, which is the banner lying about the state it exists to show.
func enterAwakeLocked(cause modeCause) {
	eyesWhy := ""
	if ready, why := visionReady(); ready {
		cfg.Vision.Enabled = true
		_ = cfg.SavePreferences()
		journalVisionEvent("enabled", "", 0)
	} else {
		eyesWhy = why
	}

	// Conversational context and barge-in are scoped to the conversation: they
	// only make sense while one is happening, and scoping them here is what
	// makes "leaving drops the retained audio" true rather than aspirational.
	speech.EnableConversationContext(cfg.Speech.TTS.ContextTurns, cfg.Speech.TTS.ContextMaxBytes)
	speech.EnableBargeIn(cfg.Speech.TTS.BargeIn)

	if cause.persists() {
		cfg.UserPrefs.VoiceMode = true
		_ = cfg.SavePreferences()
	}

	// The clock starts at the door, so a conversation nobody speaks in stands
	// down a full window from here rather than from whenever it last heard
	// something in a previous session.
	noteVoiceActivity(time.Now())
	startCompanion()

	if !cause.announces() {
		return
	}
	audio.PlayAlert()
	printLiveBanner()
	if eyesWhy != "" {
		fmt.Println(shell.Hint("camera stays off: " + eyesWhy))
	}
	if cause == causeWake {
		speakWakeAcknowledgement()
	}
}

// tearDownConversationLocked is the teardown both keyboard states share.
func tearDownConversationLocked(from modeCause) {
	// Leaving mid-sentence should stop the sentence; without this the prompt
	// came back to the keyboard while the previous reply talked over it.
	speech.StopSpeaking()
	speech.EnableConversationContext(0, 0)
	speech.EnableBargeIn(false)
	stopCompanion()
	if cfg.Vision.Enabled {
		setVisionEnabled(false)
	}
	if from.persists() {
		cfg.UserPrefs.VoiceMode = false
		_ = cfg.SavePreferences()
	}
}

// enterStandbyLocked closes the conversation but keeps wake listening. Caller
// holds modeMu.
func enterStandbyLocked(from listenMode, cause modeCause) {
	tearDownConversationLocked(cause)

	// Coming back from MANUAL means the opt-out is on disk and has to be
	// lifted, or "standby" would be a state whose microphone is shut.
	if from == modeManual {
		cfg.Speech.WakeWord.Enabled = config.BoolPtr(true)
		if shell.KeyWaitSupported() {
			cfg.Speech.WakeWord.AlwaysListen = config.BoolPtr(true)
		}
		_ = cfg.SavePreferences()
	}

	// The grace window is the other half of the fix for the reported loop. On
	// the energy engine "any sound wakes it" includes the tail of the sentence
	// that just asked for standby, so without this the state machine would
	// hand the user straight back into AWAKE and the bug would survive the
	// rename.
	armRearmGrace(cause)

	// The explanation is printed once per session and the HUD is the
	// continuous indicator, so re-entering standby has to let the line be said
	// again — the microphone is open and the user needs to be told so.
	armedPromptAnnounced = false

	if cause.announces() {
		fmt.Println(standbyLine(cause))
	}
}

// enterManualLocked closes the microphone and persists it. Caller holds modeMu.
func enterManualLocked(from listenMode, cause modeCause) {
	if from == modeAwake {
		tearDownConversationLocked(cause)
	}
	// Both halves, because one command turned them on: no wake between turns
	// and no microphone at the prompt. Leaving the prompt armed here would be
	// a microphone the user believes they have just closed.
	cfg.Speech.WakeWord.Enabled = config.BoolPtr(false)
	cfg.Speech.WakeWord.AlwaysListen = config.BoolPtr(false)
	_ = cfg.SavePreferences()

	if cause.announces() {
		fmt.Println("  " + shell.Fg(shell.HexMuted, "○ ") +
			shell.Fg(shell.HexText, "manual") +
			shell.Muted("  ·  microphone closed  ·  /blackbox wake on listens again"))
	}
}

// standbyLine renders the standby indicator, saying WHY when the cause is not
// something the user just did.
func standbyLine(cause modeCause) string {
	line := "  " + shell.Fg(shell.HexMuted, "○ ") + shell.Fg(shell.HexText, "standby")
	switch cause {
	case causeStandDown:
		return line + shell.Muted(fmt.Sprintf("  ·  nothing said for %s  ·  "+
			"make any sound and I'm back  ·  \"manual mode\" closes the mic",
			roundedDuration(cfg.Speech.WakeWord.AwakeIdleStandDown())))
	default:
		return line + shell.Muted("  ·  listening for you  ·  "+
			"type normally  ·  \"manual mode\" closes the mic")
	}
}

// roundedDuration renders a stand-down window the way a person would say it.
func roundedDuration(d time.Duration) string {
	if d >= time.Minute {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	return fmt.Sprintf("%d seconds", int(d.Seconds()))
}

// ---------------------------------------------------------------------------
// Inactivity stand-down
// ---------------------------------------------------------------------------

// lastSpeechAt is when a human was last audibly present, in unix nanoseconds.
var lastSpeechAt atomic.Int64

// noteVoiceActivity records presence. Called for anything that proves someone
// is there — speech that cleared the energy gate (INCLUDING a clip that was
// then misheard: being misunderstood must not stand you down), a keystroke, a
// spoken command, entering AWAKE.
func noteVoiceActivity(t time.Time) { lastSpeechAt.Store(t.UnixNano()) }

// awakeIdleFor reports how long it has been since the last sign of presence.
func awakeIdleFor(now time.Time) time.Duration {
	last := lastSpeechAt.Load()
	if last == 0 {
		return 0
	}
	return now.Sub(time.Unix(0, last))
}

// shouldStandDown reports whether AWAKE has been quiet long enough to fall
// back to STANDBY.
//
// A pure function of the clock and the config, deliberately: this is checked
// at the TOP of each turn, before the microphone opens, and is never a
// context deadline inside the listening path. A timeout in there is the exact
// shape of the defect that expired a wake hold into open capture, and the
// guard test for that exists so nobody reintroduces the pattern — even
// pointing the other way.
func shouldStandDown(now time.Time) bool {
	window := cfg.Speech.WakeWord.AwakeIdleStandDown()
	if window <= 0 {
		return false // explicitly disabled
	}
	return awakeIdleFor(now) >= window
}

// ---------------------------------------------------------------------------
// Re-arm grace
// ---------------------------------------------------------------------------

// rearmUntil is when standby will start honouring wake events again.
var rearmUntil atomic.Int64

// armRearmGrace suppresses wake events for a short window after entering
// standby.
//
// A spoken exit gets a longer window than a typed one for a measurable reason:
// the phrase that asked for standby is still in the air, and on the energy
// engine its own tail is a wake event.
func armRearmGrace(cause modeCause) {
	d := cfg.Speech.WakeWord.RearmDelay()
	if cause == causeSpoken || cause == causeStandDown {
		d = d * 5 / 3
	}
	if d <= 0 {
		rearmUntil.Store(0)
		return
	}
	rearmUntil.Store(time.Now().Add(d).UnixNano())
}

// inRearmGrace reports whether a wake event arriving now should be dropped.
func inRearmGrace(now time.Time) bool {
	until := rearmUntil.Load()
	return until != 0 && now.UnixNano() < until
}

// ---------------------------------------------------------------------------
// Turn provenance
// ---------------------------------------------------------------------------

// turnChannel is how the line now being handled arrived.
//
// This exists because the mode was being used as a proxy for it, and the proxy
// was already wrong: `/permissions` refuses to change the approval posture "by
// voice" by testing whether voice mode is on, so the typed line offered after a
// microphone failure was refused too. With the keyboard live during a
// conversation it would be wrong constantly. InputEvent.Channel has carried the
// truth all along; it just never reached the handlers.
var turnChannel atomic.Int32

// setTurnChannel records the provenance of the line about to be handled.
func setTurnChannel(ch input.Channel) {
	if ch == input.ChannelVoice {
		turnChannel.Store(1)
		return
	}
	turnChannel.Store(0)
}

// turnIsSpoken reports whether the line being handled arrived by voice.
func turnIsSpoken() bool { return turnChannel.Load() == 1 }

// prompterForChannel picks the prompter a turn's confirmations should use.
//
// Per TURN, not per mode. The prompter used to be swapped on entering and
// leaving a conversation, which was the same thing as the mode — but with the
// keyboard live during a conversation, a line the user TYPED would have its
// confirmation asked out loud and answered by the microphone. That is a new
// hole in the wall ADR-005 §2 builds, and provenance is the only thing that
// closes it.
func prompterForChannel(ch input.Channel) commands.Prompter {
	if ch == input.ChannelVoice && voicePrompter != nil {
		return voicePrompter
	}
	return ttyPrompter
}
