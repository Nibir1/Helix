// cmd/helix/reboot_outcome_test.go
//
// Purpose: a restart must be able to say whether it looked for an update.
//
// Asked out loud after a spoken reboot — "did you download the latest binaries
// and install them?" — Helix answered "No. I just restarted in voice mode; I
// did not download or install any binaries." That answer happened to be true
// (the machine was already on the newest release), but nothing in the program
// knew it: the plain /reboot path printed nothing about the check, carried
// nothing across the restart, and the reply came from the model's guess about
// its own behaviour.
//
// Guessing correctly is not the same as knowing, and the next guess is the
// problem.
package main

import (
	"os"
	"strings"
	"testing"

	"helix/internal/session"
)

func TestUpdateOutcomeIsCarriedAcrossTheRestart(t *testing.T) {
	saved := updateOutcome
	t.Cleanup(func() { updateOutcome = saved })

	setUpdateOutcome("already on the newest release (1.5.0)")
	if got := updateOutcomeLine(); got == "" {
		t.Fatal("the outcome was recorded and then read back empty")
	}

	rec := session.Continuity{
		Reason: "you asked out loud",
		Mode:   session.ModeVoice,
		Update: updateOutcomeLine(),
	}
	if rec.Update == "" {
		t.Fatal("the continuity record carries no update outcome, so the shell " +
			"that comes back cannot answer what the one before it did")
	}
	if !strings.Contains(rec.Update, "1.5.0") {
		t.Errorf("outcome %q does not name the version it compared against", rec.Update)
	}
}

// Every path must leave a statement — including the ones that decline to look.
// "did not check" and "checked and found nothing" are different answers, and a
// blank is indistinguishable from never having run.
func TestEveryCheckPathRecordsSomething(t *testing.T) {
	saved := updateOutcome
	t.Cleanup(func() { updateOutcome = saved })

	for _, outcome := range []string{
		"already on the newest release (1.5.0)",
		"not checked — update.check is off",
		"check failed — no network",
		"installed 1.6.0 — restarting into it",
		"found 1.6.0 but could not install it",
	} {
		updateOutcome = ""
		setUpdateOutcome(outcome)
		if updateOutcomeLine() != outcome {
			t.Errorf("outcome %q did not survive being recorded", outcome)
		}
	}
}

// The restart turn appended to the session must repeat it, because that is what
// the model reads when asked.
func TestRestartTurnMentionsTheUpdateCheck(t *testing.T) {
	src := readSourceFile(t, "reboot.go")
	fn := functionBody(src, "func recordRestartInSession(")
	if fn == "" {
		t.Fatal("recordRestartInSession not found")
	}
	if !strings.Contains(fn, "rec.Update") {
		t.Error("the restart turn does not mention the update check, so the " +
			"model answering \"did you download anything?\" is still guessing")
	}
}

// readSourceFile reads a file from this package's directory.
func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	// .gitattributes pins LF, but a test reading raw bytes should not depend on
	// that being configured.
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// functionBody returns the source of one function, by its declaration prefix.
func functionBody(src, decl string) string {
	i := strings.Index(src, decl)
	if i < 0 {
		return ""
	}
	body := src[i:]
	if end := strings.Index(body[len(decl):], "\nfunc "); end >= 0 {
		body = body[:len(decl)+end]
	}
	return body
}
