// cmd/helix/voice_log_test.go
// Purpose: P2.8 — the voice log's command surface and its one policy rule.
package main

import (
	"strings"
	"testing"
)

// Voice may STOP recording but never START it. Enabling a store of everything
// the microphone hears moves the privacy posture, which is why ADR-005 keeps
// /config and /stealth off the voice surface — but /blackbox must stay
// voice-reachable because the "manual mode" safety valve lives on it, so the
// rule lands on the subcommand.
func TestVoiceCannotStartTranscriptLog(t *testing.T) {
	starts := []string{
		"/blackbox log on",
		"/blackbox log enable",
		"/bb log on",
		"/blackbox logs on",
		"/blackbox transcript on",
		"/BLACKBOX LOG ON",
	}
	for _, line := range starts {
		if !voiceStartsTranscriptLog(line) {
			t.Errorf("%q must be recognized as starting the transcript log", line)
		}
		allowed, reason := voiceCommandAllowed(line)
		if allowed {
			t.Errorf("voice must not be allowed to run %q", line)
		}
		if !strings.Contains(strings.ToLower(reason), "typed") {
			t.Errorf("refusal for %q should tell the user to type it, got %q", line, reason)
		}
	}
}

// The asymmetry is the point: turning recording OFF by voice is always allowed,
// because a privacy control should fail toward collecting less.
func TestVoiceMayStopTranscriptLog(t *testing.T) {
	for _, line := range []string{
		"/blackbox log off",
		"/bb log off",
		"/blackbox log",        // status only
		"/blackbox log status", // status only
		"/blackbox off",        // the safety valve must never be caught by this rule
		"/blackbox on",
		"/blackbox eyes off",
	} {
		if voiceStartsTranscriptLog(line) {
			t.Errorf("%q must NOT be treated as starting the transcript log", line)
		}
		if allowed, reason := voiceCommandAllowed(line); !allowed {
			t.Errorf("voice must still be allowed to run %q (refused: %s)", line, reason)
		}
	}
}

// The status line has to distinguish "off" from "recording" without claiming a
// path when there is none — the readiness-honesty rule /blackbox status exists
// to enforce.
func TestVoiceLogStatusLineReportsOffWhenDisabled(t *testing.T) {
	saved := voiceLog
	voiceLog = nil
	defer func() { voiceLog = saved }()

	line := voiceLogStatusLine()
	if !strings.Contains(line, "off") {
		t.Fatalf("disabled status line must say off, got %q", line)
	}
	if strings.Contains(line, "voice_log") {
		t.Fatalf("disabled status line must not name a file that does not exist: %q", line)
	}
	if !strings.Contains(line, "/blackbox log on") {
		t.Fatalf("disabled status line should say how to enable it, got %q", line)
	}
}

// The log subcommand must be reachable and documented: an opt-in privacy
// control nobody can find is only half-shipped.
func TestVoiceLogSubcommandIsDocumented(t *testing.T) {
	cmd, ok := lookupCommand("/blackbox")
	if !ok {
		t.Fatal("/blackbox must be registered")
	}
	if !strings.Contains(cmd.Usage, "log") {
		t.Errorf("/blackbox usage must mention the log subcommand, got %q", cmd.Usage)
	}

	var found bool
	for _, line := range blackBoxUsage {
		if strings.Contains(line, "/blackbox log") {
			found = true
		}
	}
	if !found {
		t.Error("blackBoxUsage must list /blackbox log")
	}

	// And the detail text must state the default, because "off by default" is
	// the guarantee users need to hear without reading the docs.
	detail := strings.Join(blackBoxDetail(), "\n")
	if !strings.Contains(detail, "log on") {
		t.Error("/blackbox detail should mention how to start the transcript log")
	}
}
