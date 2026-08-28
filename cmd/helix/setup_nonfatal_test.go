// cmd/helix/setup_nonfatal_test.go
// Purpose: first-run setup must never cost the user their shell.
//
// A real first run chose Ollama, accepted the offered model, and Ollama's
// registry answered 503 through its proxy. The pull error propagated out of
// runNativeSetup, main printed "Setup failed:" and RETURNED — so Helix exited
// to the login prompt because a download did not finish.
//
// Everything setup does is configuration. Without it Helix is degraded, not
// broken: /help, /doctor, /provider use and every local command still work.
// Ejecting the user is the one outcome they cannot recover from in place, and
// it is the first thing a new user ever sees.
package main

import (
	"os"
	"strings"
	"testing"
)

// The pull sites must report and continue, never propagate.
//
// Structural rather than behavioural because reaching the real path needs a
// live Ollama, a live registry and an interactive prompt — and the regression
// is a single `return err` slipping back in.
func TestPullFailureDoesNotAbortSetup(t *testing.T) {
	src, err := os.ReadFile("helpers.go")
	if err != nil {
		t.Fatalf("read helpers.go: %v", err)
	}
	// Normalise line endings before matching. .gitattributes pins the checkout
	// to LF, but a test that reads raw bytes and looks for "\n" should not
	// depend on that being configured correctly — it failed on windows-latest
	// for exactly this reason.
	body := strings.ReplaceAll(string(src), "\r\n", "\n")

	if !strings.Contains(body, "reportPullFailure(") {
		t.Fatal("a failed pull must be reported, not returned as a setup error")
	}
	// Every call site must hand back nil: the model is missing, the session is
	// not.
	for _, site := range []string{
		"reportPullFailure(choice, err)\n\t\treturn nil",
		"reportPullFailure(model, err)\n\t\treturn nil",
	} {
		if !strings.Contains(body, site) {
			t.Errorf("a pull failure still aborts setup; expected to find:\n%s", site)
		}
	}
}

// main must not exit on a setup error.
func TestSetupFailureDoesNotExitHelix(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(src)

	if strings.Contains(body, "color.Red(\"Setup failed: %v\", err)\n\t\t\treturn") {
		t.Fatal("a failed setup must not return from main — that drops the user to their login shell")
	}
	if !strings.Contains(body, "Helix is starting anyway") {
		t.Error("the user must be told the shell still works and how to finish setup")
	}
}

// The advice must name a way forward that exists.
func TestPullFailureAdvicePointsSomewhereReal(t *testing.T) {
	valid := map[string]bool{}
	for _, c := range registry {
		valid[c.Name] = true
	}
	for _, cmd := range []string{"/setup", "/provider", "/doctor"} {
		if !valid[cmd] {
			t.Errorf("the recovery advice names %s, which is not a registered command", cmd)
		}
	}
}
