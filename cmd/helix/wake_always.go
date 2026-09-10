// cmd/helix/wake_always.go
// Purpose: always-listen wake — the word reaches the KEYBOARD prompt, so
// speaking switches Helix into live mode without touching the keyboard first.
//
// WHAT THIS IS. Without it the wake word only gates the gaps between spoken
// turns: you had to type `/blackbox on` to start talking, and the wake word
// took over from there. With it, an idle manual prompt keeps the microphone
// open, and a wake event enters live mode and takes the next turn by voice.
// From the keyboard's point of view Helix is a shell; from the room's, it is
// listening.
//
// HOW, given that a blocked read cannot be pre-empted. It is never blocked.
// `shell.ArmedWait` puts the terminal in raw mode and polls stdin for
// readiness, so the editor is only entered once a keystroke is actually
// waiting — and that keystroke is not consumed, so ReadLine reads it as if it
// had arrived a moment later. Nothing in the line editor changed, which is what
// keeps the PTY suite's byte-identical guarantee intact. The alternatives that
// do not work are recorded in internal/shell/keywait_unix.go, measured against
// a real PTY rather than reasoned about.
//
// WHAT IT COSTS, stated because it is a privacy posture and not a convenience.
// An armed prompt holds the microphone open for as long as the shell sits
// idle — which is most of a working day, on a channel the user is not thinking
// about. It is ON by default as of 2026-09-09 (owner decision; threat V2b
// records the cost), and turning it on is still typed-only: ADR-005 says voice
// may reduce what is collected but never increase it, and this increases it.
//
// The default reaches a config with no `enabled` key. It does NOT resurrect an
// explicit `false`, including the one older builds wrote to disk as a plain
// bool's zero value — `"enabled": false` is the documented opt-out, so reading
// it as consent would be a guess in the one direction ADR-005 forbids. Nothing is transcribed while armed (the detector scores
// chunks and discards them), which is the same construction ADR-005 §5's
// between-turns lockout relies on — enforced there by
// TestWakeLoopNeverTranscribes walking the wakeword package for a transcription
// call.
package main

import (
	"context"
	"fmt"
	"strings"

	"helix/internal/shell"
	"helix/internal/utils"
	"helix/internal/ux"
	"helix/internal/wakeword"
)

// armedOutcome says how an armed idle wait ended.
type armedOutcome int

const (
	// armedKeyboard: a keystroke is waiting. Read a line as usual.
	armedKeyboard armedOutcome = iota

	// armedWake: the wake word fired. Enter live mode and take a voice turn.
	armedWake

	// armedUnavailable: arming was not possible here. The caller falls back to
	// an ordinary blocking prompt, which is exactly today's behaviour.
	armedUnavailable
)

// alwaysListenArmed reports whether the next manual prompt should be armed.
//
// Every condition is a reason the feature would otherwise be a lie: the switch
// itself, the wake word it extends, a recorder to hear with, a transcriber to
// answer with, and a platform that can tell us a key is waiting. Checked per
// turn rather than cached, because /blackbox setup can change three of them
// mid-session.
func alwaysListenArmed() bool {
	ww := cfg.Speech.WakeWord
	if !ww.PromptArmed() {
		return false
	}
	if !shell.KeyWaitSupported() {
		return false
	}
	return voiceEntryPreflight() == nil
}

// armedIdleWait holds an idle manual prompt open to both the keyboard and the
// microphone, and reports which one arrived.
//
// The wake service runs for the duration and is stopped on every exit path,
// including the keyboard one — the microphone must be closed before the editor
// takes the line, or a spoken reply and a typed command would be competing for
// the same device.
func armedIdleWait() (wakeword.WakeEvent, armedOutcome) {
	svc, err := newWakeService()
	if err != nil {
		return wakeword.WakeEvent{}, armedUnavailable
	}

	// Unbounded, deliberately, and this is the one place the ADR-005 §5 window
	// does not apply. That rule caps how long an ARMED SESSION may sit before
	// falling back to wake-only listening; wake-only listening is precisely
	// what this is. There is no open capture here to time out into.
	ctx, cancel := context.WithCancel(context.Background())
	unreg := utils.RegisterOperation(cancel)
	defer unreg()
	defer cancel()

	events, err := svc.Start(ctx)
	if err != nil {
		return wakeword.WakeEvent{}, armedUnavailable
	}
	defer func() { _ = svc.Stop() }()

	// One channel for ArmedWait to select on, fed by the first wake event OR
	// by the scan loop dying.
	//
	// Both have to travel the same wire. shell.ArmedWait selects on `fired` and
	// otherwise polls the keyboard forever; a closed `events` channel writes
	// nothing to `fired`, so a dead listener and a quiet room are literally the
	// same program state — which is what made "it says it is listening and
	// nothing happens" the shape of every wake failure. (An earlier comment
	// here claimed "the poll below reports unavailable". It could not: there is
	// nothing for the poll to observe.) So the death is delivered as an event
	// and told apart by scannerDied, which is safe to read after the receive
	// because the channel send orders the write before it.
	fired := make(chan struct{}, 1)
	var got wakeword.WakeEvent
	var scannerDied bool
	signal := func() {
		select {
		case fired <- struct{}{}:
		default:
		}
	}
	go func() {
		ev, ok := <-events
		if !ok {
			scannerDied = true
			signal()
			return
		}
		got = ev
		signal()
	}()

	printArmedPrompt()
	viz := ux.NewVoiceViz()
	viz.SetStandbyHint(standbyHint())
	viz.Start(ux.VizStandby)
	defer viz.Stop()

	res, waitErr := shell.ArmedWait(fired, shell.DefaultArmedPoll)
	switch {
	case waitErr != nil, res == shell.ArmedUnavailable:
		return wakeword.WakeEvent{}, armedUnavailable
	case res == shell.ArmedOther:
		// The HUD comes down first on both branches: it is the indicator that
		// the microphone is open, and on the died branch it no longer is.
		viz.Stop()
		if scannerDied {
			noteArmingDied(svc.Err())
			return wakeword.WakeEvent{}, armedUnavailable
		}
		return got, armedWake
	default:
		return wakeword.WakeEvent{}, armedKeyboard
	}
}

// noteArmingDied reports that hands-free listening stopped mid-wait.
//
// The "◉ listening" line is printed once per session and the HUD is the
// continuous indicator, so a scan loop that dies has to retract both — the HUD
// by stopping it (the caller does that before calling here), the line by
// clearing the announced flag so the next successfully armed prompt says it
// again. Between those two, a wake loop that died leaves nothing on screen
// claiming the microphone is open.
func noteArmingDied(cause error) {
	armedPromptAnnounced = false
	detail := "the wake scanner stopped — /mictest checks the microphone"
	if cause != nil {
		detail = cause.Error() + " — /mictest checks the microphone"
	}
	fmt.Println(shell.Step(shell.StateWarn, "stopped listening at the prompt", detail))
}

// printArmedPrompt says the microphone is open, because an open microphone the
// user cannot see is the thing this feature must never be.
//
// ONCE per session, and that changed when arming became the default. As an
// opt-in it printed before every idle prompt, which was fine for something rare
// and deliberate; on by default it would put a line above every prompt in every
// session — noise that teaches people to stop reading the one line that matters.
//
// What carries the signal the rest of the time is the standby HUD: an animated
// row that is present exactly while the microphone is, and gone the moment it
// closes. So the explanation is said once, in full, with the way out; the
// indicator is continuous.
func printArmedPrompt() {
	if armedPromptAnnounced {
		return
	}
	armedPromptAnnounced = true
	fmt.Println("  " + shell.Fg(shell.HexMuted, "◉ ") +
		shell.Fg(shell.HexText, "listening") +
		shell.Muted("  ·  make any sound to go live  ·  type normally to stay here") +
		shell.Muted("  ·  /blackbox wake off"))
}

// armedPromptAnnounced keeps the explanation to once per session. The HUD is
// the continuous indicator; see printArmedPrompt.
var armedPromptAnnounced bool

// standbyHint is what the standby HUD tells the user to do, worded for the
// engine that is actually listening.
//
// The fourth place this correction was needed. The default energy engine wakes
// on speech ONSET — it cannot match words at all — so instructing someone to
// "say the wake phrase" tells them to do a careful, quiet thing that is exactly
// the wrong move, and then looks broken. Only the sidecar engine scores a
// phrase, so only it names one. Kept beside wakeHeardDetail, which makes the
// same distinction for the event that ends standby.
func standbyHint() string {
	ww := cfg.Speech.WakeWord
	if engineOrDefault(ww.Engine) == "sidecar" && ww.Phrase != "" {
		return fmt.Sprintf("── say %q ──", ww.Phrase)
	}
	return ux.DefaultStandbyHint
}

// enterVoiceModeFromWake performs the transition a spoken word asked for.
//
// It routes through blackBoxOn rather than calling enterVoiceMode directly, so
// the mode reached by speaking is the same mode reached by typing — camera,
// companion loop, banner and persistence included. Phase 13 records what the
// alternative costs: live mode restored from a preference once came back
// without the companion, "the same mode reached by a different door, behaving
// differently".
func enterVoiceModeFromWake(ev wakeword.WakeEvent) {
	fmt.Println()
	fmt.Println(shell.Step(shell.StateGood, "wake heard", wakeHeardDetail(ev)))
	blackBoxOn()

	// Spoken only once live mode is actually up. blackBoxOn can refuse — a
	// failed preflight, or a mode that was already on — and saying "I'm
	// listening" into either of those would be the readiness lie this file
	// spends its length avoiding. voiceModeActive is the fact, so it is what
	// gets asked.
	if voiceModeActive {
		speakWakeAcknowledgement()
	}
}

// wakeHeardDetail describes what actually triggered, without overstating it.
//
// The default engine cannot match words, so a phrase here would be the fourth
// place in this codebase making a promise the detector does not keep (see
// blackBoxWakeLine, wakeBannerLines, printWakeStatus). Only the sidecar engine
// is scoring a phrase, so only the sidecar engine gets to name one.
func wakeHeardDetail(ev wakeword.WakeEvent) string {
	if engineOrDefault(cfg.Speech.WakeWord.Engine) == "sidecar" && ev.Phrase != "" {
		return fmt.Sprintf("%q (score %.2f) — going live", ev.Phrase, ev.Score)
	}
	// Four decimals for the energy engine, two for the sidecar. They are not
	// the same quantity: a sidecar score is a 0..1 confidence where 0.91 is
	// meaningful, while an energy level is a normalized RMS that lives between
	// 0.001 and 0.03 on a built-in microphone — at %.2f every wake this engine
	// will ever report prints as "0.01", which is the number rounded away to
	// nothing.
	return fmt.Sprintf("speech onset (level %.4f) — going live", ev.Score)
}

// alwaysListenStatusLine is the one-liner for /blackbox status and the
// subcommand's own report.
func alwaysListenStatusLine() string {
	ww := cfg.Speech.WakeWord
	if !ww.PromptArmed() {
		return shell.Badge(shell.StateIdle, "off") +
			shell.Muted("  /blackbox wake on  ·  listens at the prompt and between turns")
	}
	if !shell.KeyWaitSupported() {
		return shell.Badge(shell.StateWarn, "unavailable here") +
			shell.Muted("  set, but this platform cannot arm an idle prompt")
	}
	if err := voiceEntryPreflight(); err != nil {
		return shell.Badge(shell.StateWarn, "not armed") + shell.Muted("  "+err.Error())
	}
	if !ww.Listening() {
		return shell.Badge(shell.StateWarn, "not armed") +
			shell.Muted("  wake word is off  ·  /blackbox wake on")
	}
	return shell.Badge(shell.StateGood, "armed") +
		shell.Muted("  an idle prompt hears the wake word")
}

// voiceStartsAlwaysListen reports whether a spoken command would OPEN the
// microphone at the keyboard prompt.
//
// The same shape as voiceStartsTranscriptLog, and for the same reason: the deny
// list is per-command, /blackbox must stay voice-reachable because the "manual
// mode" safety valve lives on it, so the rule lands on the subcommand.
func voiceStartsAlwaysListen(line string) bool {
	fields := strings.Fields(strings.ToLower(line))
	if len(fields) < 3 {
		return false
	}
	if fields[0] != "/blackbox" && fields[0] != "/bb" {
		return false
	}
	if fields[1] != "wake" {
		return false
	}
	// `wake on` is the form to guard now. It used to be `wake always on` — the
	// two switches merged, and the guard had to move with them or a spoken
	// "blackbox wake on" would open the microphone at the keyboard prompt,
	// which is precisely the increase in collection ADR-005 reserves for the
	// keyboard. Missing this would have left the rule intact in the threat
	// model and broken in the code.
	return fields[2] == "on" || fields[2] == "enable"
}

// armingLapseAnnounced keeps the lapse notice to once per session.
//
// The same discipline wakeLapseAnnounced applies to the between-turns window:
// the message explains a STATE CHANGE, and an armed prompt is re-entered on
// every turn, so repeating it would bury the shell in a notice about not
// listening.
var armingLapseAnnounced bool

// noteArmingLapse says the microphone could not be armed, once.
//
// It matters because the failure is otherwise invisible in the one direction
// that counts: the prompt looks exactly the same, so a user who said the wake
// word and got nothing would have no way to tell a mis-heard word from a dead
// recorder.
func noteArmingLapse() {
	if armingLapseAnnounced {
		return
	}
	armingLapseAnnounced = true
	fmt.Println(shell.Step(shell.StateWarn, "not listening at the prompt",
		"the wake scanner could not start — /blackbox status diagnoses, "+
			"/mictest checks the microphone"))
}
