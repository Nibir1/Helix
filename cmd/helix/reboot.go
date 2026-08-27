// cmd/helix/reboot.go
//
// Purpose: /reboot — restart the Helix shell in place, keeping what it was
// doing and coming back in the mode it left.
//
// Why the shell needs this at all: /purge already ends by telling you to
// restart, because open SQLite handles only release when the process exits, and
// a provider or sidecar change can leave a session holding state it cannot drop.
// Until now the only way to act on that was to quit and start again by hand —
// which throws away the mode, the working directory and any sense of what was
// in progress.
//
// The restart is a real one. It is not a soft reset of internal state: the
// process image is replaced, so every handle, goroutine, cached provider and
// loaded model is genuinely gone. That is the point — a soft reset that missed
// one of them would be the bug this command exists to clear.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/session"
	"helix/internal/shell"
	"helix/internal/speech"
)

// rebootRequested is read by the REPL loop, which breaks out of it the same way
// `exit` does.
//
// A flag rather than an exec inside the handler, deliberately. Calling
// syscall.Exec from the command handler would replace the process while main's
// deferred db.Close() was still pending, leaving SQLite's WAL and SHM files hot
// — the exact state /purge warns about, caused by the command meant to fix it.
var rebootRequested bool

// rebootUpdated records that the binary was replaced during this restart, so
// the supervisor can tell a bad update from an ordinary exit and roll back.
var rebootUpdated bool

// handleRebootCommand records what Helix was doing and asks the loop to stop.
//
// It does NOT restart anything itself. See rebootRequested.
func handleRebootCommand(c cmdArgs) { handleRebootRequest(c, false) }

// handleRebootSpoken is the microphone's entry point. Split from the typed one
// so the continuity record can tell how the request arrived — see
// captureContinuity.
func handleRebootSpoken() { handleRebootRequest(cmdArgs{}, true) }

func handleRebootRequest(c cmdArgs, spoken bool) {
	sub := c.Lower()
	switch sub {
	case "", "now", "check":
	default:
		fmt.Println(shell.Step(shell.StateWarn, sub, "is not a /reboot option"))
		fmt.Println(shell.Hint("/reboot [now|check]  ·  restart, skip the update check, or only check"))
		return
	}

	// "check" answers the question and stops. Someone asking whether an update
	// exists has not asked to be restarted, and doing it anyway would make the
	// question unaskable.
	if sub == "check" {
		maybeInstallUpdate(spoken, true)
		return
	}

	// The update runs BEFORE the continuity record is written, so a record
	// written by the old binary is read by the new one — which is the whole
	// point, and only works in this order. "now" skips the check for a restart
	// that must be instant.
	updated := false
	if sub != "now" {
		updated = maybeInstallUpdate(spoken, false)
	}
	rebootUpdated = updated
	if updated {
		// A supervised child cannot tell its supervisor anything except its exit
		// status, so the fact that it installed something is left on disk for
		// the supervisor to find. Without it, a bad update installed by the
		// SECOND or later restart would not be rolled back.
		noteUpdateForSupervisor()
	}

	reason := "you asked at the keyboard"
	if spoken {
		reason = "you asked out loud"
	}
	rec := captureContinuity(reason, spoken)

	if err := session.SaveContinuity(rec); err != nil {
		// Not fatal, and the user has to be told: without the record the
		// restart still works, it just comes back with no memory of why. A
		// silent downgrade here would look like the feature simply not working.
		fmt.Println(shell.Step(shell.StateWarn, "continuity",
			"could not be saved — restarting anyway: "+err.Error()))
	}

	printRebootNotice(rec)

	// Say it before the microphone goes away with the process.
	if voiceModeActive {
		// speakDirect rather than the /tts-gated path: this is voice-channel
		// bookkeeping, not a reply, and someone who just spoke to a terminal
		// they may not be looking at should hear that it is going away.
		speakDirect("Rebooting. I will be right back.")
	}

	rebootRequested = true
}

// captureContinuity reads the state worth carrying across a restart.
//
// Everything here is read from live state rather than from config, because
// config records what was last CHOSEN and this record has to describe what was
// actually true a moment before the process ended.
// Args:
//   - reason: why the restart is happening, shown on the way back in.
//   - spoken: whether the request arrived through the microphone. It gates the
//     conversation excerpt, and see the comment at the gate for why.
func captureContinuity(reason string, spoken bool) session.Continuity {
	rec := session.Continuity{
		At:       time.Now(),
		Reason:   reason,
		Mode:     session.ModeManual,
		Provider: ai.ActiveProviderName(),
		Model:    ai.ActiveModel(),
	}
	if voiceModeActive {
		rec.Mode = session.ModeVoice
	}
	if cwd, err := os.Getwd(); err == nil {
		rec.Cwd = cwd
	}

	// In-progress tasks are the closest thing Helix has to "what I was doing"
	// that it did not have to infer.
	if todoList != nil {
		for _, item := range todoList.Items() {
			if item.State == session.TodoInProgress {
				rec.Tasks = append(rec.Tasks, item.Text)
			}
		}
	}

	// The last exchange, so the resume can name the subject without replaying
	// the conversation — which survives on its own in session.json.
	//
	// NOT written for a spoken restart, and this is a policy decision rather
	// than a nicety. ADR-005 states, in four documents, that "voice may reduce
	// what is collected but never increase it" — a rule that exists because the
	// microphone is an untrusted channel where a television can act on your
	// behalf. A spoken "reboot" that wrote an excerpt of what you had just said
	// to disk would break that rule for a convenience, so it does not: a voice
	// restart carries the mode, the directory, the provider and the tasks, and
	// no conversation content at all. The resume is very slightly less specific
	// and the principle survives without an exception.
	if agentCore != nil && !spoken {
		if turns := agentCore.SessionTurns(); len(turns) > 0 {
			last := turns[len(turns)-1]
			rec.LastExchange = strings.TrimSpace(last.UserText)
		}
	}

	rec.Doing = describeWork(rec)
	return rec
}

// describeWork writes the one-line summary shown on the way back in.
//
// Composed here rather than at render time so the RECORD carries the sentence:
// the process that wrote it is the only one that knew the context, and a
// summary rebuilt after the restart from fragments would be a guess.
func describeWork(rec session.Continuity) string {
	switch {
	case len(rec.Tasks) == 1:
		return "working on: " + rec.Tasks[0]
	case len(rec.Tasks) > 1:
		return fmt.Sprintf("working on %d tasks", len(rec.Tasks))
	case rec.LastExchange != "":
		return "mid-conversation"
	default:
		return ""
	}
}

// printRebootNotice is the last thing the old process says.
func printRebootNotice(rec session.Continuity) {
	fmt.Println(shell.PanelTitle("reboot"))
	fmt.Println(shell.Step(shell.StateIdle, "restarting", rec.Reason))

	w := shell.KVWidth("MODE", "DOING", "WHERE")
	fmt.Println(shell.KV("MODE", shell.Value(rec.Mode)+
		shell.Muted("  ·  coming back the same way"), w))
	if rec.Doing != "" {
		fmt.Println(shell.KV("DOING", shell.Muted(rec.Doing), w))
	}
	if rec.Cwd != "" {
		fmt.Println(shell.KV("WHERE", shell.Muted(rec.Cwd), w))
	}
	fmt.Println(shell.PanelEnd())
}

// restoreContinuity is the first thing the new process says, if there is
// anything to say.
//
// Called after the banner and after the session, task list and voice mode are
// live, so everything it reports is state that has actually been restored
// rather than state it intends to restore.
//
// Args: now: the clock, injected so the staleness rule is testable.
// Returns: whether a record was consumed.
func restoreContinuity(now time.Time) bool {
	rec, ok := session.LoadContinuity(now)
	if !ok {
		return false
	}

	// The working directory is restored BEFORE anything is printed, so the
	// panel below describes where you actually are. A shell that announces the
	// old directory and leaves you in $HOME is worse than one that says nothing.
	if rec.Cwd != "" && sandbox != nil {
		// Through the sandbox rather than os.Chdir: the sandbox owns the
		// confinement root, and moving the process under it would leave the two
		// disagreeing about where "here" is — which is how a confinement check
		// starts passing for the wrong directory.
		_ = sandbox.SetDirectory(rec.Cwd)
	}

	// Mode is honoured only when it DISAGREES with what boot already did.
	// initVoiceMode has run by now and acts on cfg.UserPrefs.VoiceMode; the two
	// normally agree, and re-entering a mode already entered would replay the
	// banner and restart the companion for no reason.
	applyContinuityMode(rec.Mode)

	if !rec.Resumable() {
		// A bare mode carry-over is worth doing and not worth a paragraph.
		return true
	}

	fmt.Println(shell.PanelTitle("resumed"))
	fmt.Println(shell.Step(shell.StateGood, "back",
		"after a reboot — "+rec.Reason))

	w := shell.KVWidth("WAS DOING", "TASKS", "LAST", "BRAIN", "WHERE")
	if len(rec.Tasks) > 0 {
		// The tasks ARE the summary when there are any — printing both gave
		// "WAS DOING working on: wire up the parser" directly above
		// "TASKS wire up the parser", which is one fact typed twice.
		for _, task := range rec.Tasks {
			fmt.Println(shell.KV("TASKS", shell.Value(task), w))
		}
	} else if rec.Doing != "" {
		fmt.Println(shell.KV("WAS DOING", shell.Muted(rec.Doing), w))
	}
	if rec.LastExchange != "" {
		fmt.Println(shell.KV("LAST", shell.Muted("you asked: "+rec.LastExchange), w))
	}
	if rec.Provider != "" {
		brain := shell.Value(rec.Provider)
		if rec.Model != "" {
			brain += shell.Muted("  ·  " + rec.Model)
		}
		fmt.Println(shell.KV("BRAIN", brain, w))
	}
	if rec.Cwd != "" {
		// Reported here rather than by the sandbox, which is why SetDirectory
		// exists: the move belongs to this panel, not to a green line above it.
		fmt.Println(shell.KV("WHERE", shell.Muted(rec.Cwd), w))
	}
	fmt.Println(shell.PanelEnd())
	fmt.Println(shell.Hint("/memory shows the conversation this picks up from"))
	return true
}

// applyContinuityMode puts the shell back in the mode the record names.
//
// The record wins over the persisted preference, and the difference is the
// whole reason it carries a mode at all: cfg.UserPrefs.VoiceMode is what you
// last CHOSE, while this is what was true at the instant of the restart. A
// session that had entered voice mode without persisting — or left it without
// persisting — would otherwise come back as the opposite of what it was.
//
// Nothing happens when the two already agree, which is the common case: boot
// has restored the mode already and re-entering it would replay the banner and
// restart the companion for nothing.
func applyContinuityMode(mode string) {
	switch mode {
	case session.ModeVoice:
		if voiceModeActive {
			return
		}
		// Same preflight as /blackbox on: refusing to strand someone in voice
		// mode without a microphone is a rule that must not have an exception
		// just because the mode arrived from a file.
		if _, err := speech.DetectRecorder(); err != nil {
			fmt.Println(shell.Step(shell.StateWarn, "manual mode",
				"you rebooted from voice mode, but no recorder is available: "+err.Error()))
			return
		}
		enterVoiceMode(true)
		startCompanion()
	case session.ModeManual:
		if !voiceModeActive {
			return
		}
		exitVoiceMode(true)
	}
}

// rebootPhrases end a turn by asking for a restart.
//
// Matched as a SUFFIX like the manual-mode kill phrases, and for the same
// reason recorded there: people do not speak in bare commands. "Okay, please
// reboot." has to work, while "how do I reboot this thing?" — where the word
// lands mid-sentence — must not. Ending on it is what makes it an instruction.
//
// "restart" alone is deliberately absent. It is the ordinary English word for
// restarting anything — a sidecar, a download, a sentence — and a suffix match
// on it would fire on "let's restart" in a conversation about something else.
// Every phrase here names Helix or the shell, or is the unambiguous "reboot".
var rebootPhrases = []string{
	"reboot", "please reboot", "reboot yourself", "reboot now",
	"reboot the shell", "reboot yourself now",
	"restart yourself", "restart the shell", "restart helix",
}

// isVoiceRebootPhrase reports whether the user asked for a restart out loud.
//
// Args: text: the raw transcript.
// Returns: whether the shell should reboot.
// Complexity: O(len(text) × len(rebootPhrases)).
func isVoiceRebootPhrase(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	// A question about rebooting is not a request to reboot. "What happens when
	// you reboot" ends on the phrase and would otherwise fire — which is the
	// suffix rule's one weak spot, and it costs more here than it does for the
	// manual-mode kill phrase: mishearing that stops the microphone, mishearing
	// this ends the process.
	if looksLikeAQuestion(t) {
		return false
	}
	t = strings.TrimRight(t, " .!?,")
	for _, p := range rebootPhrases {
		if t == p || strings.HasSuffix(t, " "+p) {
			return true
		}
	}
	return false
}

// questionOpeners begin a sentence that is asking rather than instructing.
//
// Openers rather than a trailing "?" alone: speech-to-text punctuation is a
// guess, and several providers never emit a question mark at all — so a rule
// that depended on one would work on some chains and not others.
var questionOpeners = []string{
	"what", "how", "why", "when", "where", "who", "which",
	"can ", "could ", "should ", "would ", "will ", "do ", "does ", "did ",
	"is ", "are ", "was ", "were ",
}

// looksLikeAQuestion reports whether an utterance is asking about something.
func looksLikeAQuestion(t string) bool {
	if strings.HasSuffix(strings.TrimSpace(t), "?") {
		return true
	}
	for _, opener := range questionOpeners {
		if strings.HasPrefix(t, opener) {
			return true
		}
	}
	return false
}

// maybeReboot replaces this process with a fresh Helix, if one was asked for.
//
// Deferred from main so it runs AFTER the database is closed and the SessionEnd
// hook has fired. See the registration site for why the ordering is load-bearing.
//
// A failure here is reported rather than swallowed, and the record is cleared:
// the shell is about to exit either way, and coming back later to announce a
// resume from a reboot that never happened would be a lie told by a file.
func maybeReboot() {
	if !rebootRequested {
		return
	}
	quiesceForRestart()
	restartShell() // never returns
}

// quiesceForRestart puts the outgoing process to sleep before it becomes a
// supervisor.
//
// This is not tidiness, it is a correctness requirement of the supervisor
// design. The parent does not exit — it blocks waiting on the child — so every
// goroutine it started is still running: the companion loop would keep sampling
// the CAMERA and queueing remarks, and a half-finished reply would keep talking,
// while a second Helix is doing the same thing on the same microphone and
// speaker. Two live modes sharing one set of senses is exactly the state the
// active-session lock exists to prevent, arrived at from inside one command.
//
// Nothing here PERSISTS: the record already captured the mode, and writing
// "voice off" to config on the way out is how a restart from live mode would
// come back at the keyboard.
// The pieces of exitVoiceMode are inlined rather than called, for one reason:
// exitVoiceMode announces "keyboard · /blackbox on goes live again", which is
// true when someone asked to leave live mode and a lie here — the shell is
// restarting INTO live mode, and the panel above already said so.
func quiesceForRestart() {
	stopCompanion()
	speech.StopSpeaking()
	speech.EnableConversationContext(0, 0)
	speech.EnableBargeIn(false)
	voiceModeActive = false
}

// asExitError is errors.As, spelled out so reboot_exec.go reads without an
// import that exists for one line.
func asExitError(err error, target **exec.ExitError) bool {
	return errors.As(err, target)
}
