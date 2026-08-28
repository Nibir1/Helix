// cmd/helix/reboot_test.go
// Purpose: a spoken reboot must fire on the ways people actually say it, and on
// nothing else — and the record it writes has to describe the mode it left.
package main

import (
	"strings"
	"testing"

	"helix/internal/session"
)

// The phrase list is a suffix match for the same reason "manual mode" is:
// people do not speak in bare commands. QA's complaint about the safety valve
// was that it only opened for someone who already knew its exact wording.
func TestSpokenRebootAcceptsHowPeopleActuallyAsk(t *testing.T) {
	for _, said := range []string{
		"reboot",
		"Reboot.",
		"please reboot",
		"Okay, please reboot.",
		"reboot yourself",
		"go ahead and reboot the shell",
		"helix reboot",
		"now reboot",
		"i want you to reboot",
		"restart yourself",
		"Alright, restart helix!",
		"reboot now",
	} {
		if !isVoiceRebootPhrase(said) {
			t.Errorf("%q should reboot", said)
		}
	}
}

// A suffix, never a substring. The word lands mid-sentence in a question ABOUT
// the feature, and answering a question by doing the thing is the failure mode
// this shape exists to avoid.
func TestSpokenRebootRefusesEverythingElse(t *testing.T) {
	for _, said := range []string{
		"how do I reboot this thing?",
		"reboot the router",
		"remind me to reboot the server later",
		// A question about rebooting is not a request to reboot. Speech-to-text
		// punctuation is a guess, so the opener carries the rule rather than a
		// trailing question mark that some providers never emit.
		"what happens when you reboot",
		"should we reboot",
		"do you need to reboot",
		"when did you last reboot",
		// The sentence that actually cost a live session: not a question, ends
		// on the phrase, and Helix restarted in the middle of the user
		// explaining that it had forgotten restarting.
		"so you don't have any memory that I told you to reboot",
		"you said reboot",
		"I already asked you to reboot",
		"the last thing I said was reboot",
		"I am not asking you to reboot",
		// "restart" alone is the ordinary English word for restarting anything.
		// A suffix match on it would fire on a conversation about a download.
		"let's restart",
		"can we restart",
		"restart the download",
		"",
		"manual mode",
	} {
		if isVoiceRebootPhrase(said) {
			t.Errorf("%q must NOT reboot", said)
		}
	}
}

// The record has to carry the mode that was TRUE, not the one last chosen:
// cfg.UserPrefs.VoiceMode records a preference, and a session that entered or
// left voice mode without persisting would otherwise come back as the opposite
// of what it was.
func TestCaptureContinuityRecordsTheLiveMode(t *testing.T) {
	restore := voiceModeActive
	t.Cleanup(func() { voiceModeActive = restore })

	voiceModeActive = true
	if got := captureContinuity("spoken", true).Mode; got != session.ModeVoice {
		t.Errorf("live mode = %q, want %q", got, session.ModeVoice)
	}
	voiceModeActive = false
	if got := captureContinuity("typed", false).Mode; got != session.ModeManual {
		t.Errorf("keyboard mode = %q, want %q", got, session.ModeManual)
	}
}

// ADR-005's standing rule is that voice may reduce what is collected but never
// increase it. A spoken restart that wrote an excerpt of what you had just said
// to disk would break it — so it writes no conversation content at all, and the
// resume is slightly less specific instead of the principle being amended.
func TestSpokenRebootWritesNoConversationContent(t *testing.T) {
	restore := voiceModeActive
	t.Cleanup(func() { voiceModeActive = restore })
	voiceModeActive = true

	if got := captureContinuity("you asked out loud", true).LastExchange; got != "" {
		t.Errorf("a spoken reboot must store no conversation content, got %q", got)
	}
}

// The record is stamped and carries a working directory, or the resume cannot
// put the user back where they were.
func TestCaptureContinuityStampsAndLocatesItself(t *testing.T) {
	rec := captureContinuity("you asked at the keyboard", false)
	if rec.At.IsZero() {
		t.Error("an unstamped record is discarded on load")
	}
	if rec.Cwd == "" {
		t.Error("the record must name the working directory to return to")
	}
	if rec.Reason == "" {
		t.Error("a restart with no cause reads like a crash")
	}
}

// describeWork is what the resume says out loud. It must prefer the concrete.
func TestDescribeWorkPrefersTheTaskOverTheConversation(t *testing.T) {
	one := describeWork(session.Continuity{
		Tasks: []string{"wire up the parser"}, LastExchange: "hello",
	})
	if !strings.Contains(one, "wire up the parser") {
		t.Errorf("a single task should be named: %q", one)
	}
	many := describeWork(session.Continuity{Tasks: []string{"a", "b", "c"}})
	if !strings.Contains(many, "3") {
		t.Errorf("several tasks should be counted: %q", many)
	}
	if got := describeWork(session.Continuity{LastExchange: "hello"}); got == "" {
		t.Error("a conversation with no tasks is still something to resume")
	}
	if got := describeWork(session.Continuity{}); got != "" {
		t.Errorf("an idle shell has nothing to describe, got %q", got)
	}
}

// The supervisor's exit code must not collide with an ordinary one, or a shell
// the user actually quit would be restarted.
func TestRebootExitCodeIsDistinct(t *testing.T) {
	for _, taken := range []int{0, 1, 42, 130} {
		if rebootExitCode == taken {
			t.Errorf("rebootExitCode %d collides with an exit code Helix already uses", taken)
		}
	}
}
