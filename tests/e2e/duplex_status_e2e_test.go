//go:build !windows

// tests/e2e/duplex_status_e2e_test.go
// Purpose: RENDER the full-duplex status row rather than read the code that
// builds it (§9 rule 12).
//
// The panel is the only place a user learns that a $0.05-a-minute session is or
// is not about to open, and three separate wordings live behind one branch —
// "selected", "unavailable", and the row being absent entirely. Reading that in
// source has been wrong here before; printing it has not.
package e2e

import (
	"strings"
	"testing"
	"time"
)

// With gpt-live-1 chosen, the row must appear AND must not claim a session is
// open, because none is: the session opens with the next conversation.
func TestE2E_DuplexRowSaysSelectedNotOpen(t *testing.T) {
	t.Setenv("HELIX_E2E_SPEECH", "1")
	t.Setenv("HELIX_E2E_STT", "1")
	t.Setenv("HELIX_E2E_STT_MODEL", "gpt-live-1")
	h := newHarness(t, `{"intent":"chat","response":"ok"}`)
	defer h.Close()

	if err := h.SendExpect("/blackbox status", "DUPLEX", 20*time.Second); err != nil {
		t.Fatalf("the DUPLEX row never rendered: %v\n%s", err, h.stripped())
	}
	out := h.stripped()
	row := duplexRow(out)
	if row == "" {
		t.Fatalf("no DUPLEX row in:\n%s", out)
	}
	if !strings.Contains(row, "selected") {
		t.Errorf("DUPLEX row does not say the session is merely selected: %q", row)
	}
	// The badge, not the prose: "opens with the next conversation" legitimately
	// contains the word "open", and an earlier version of this assertion tripped
	// over exactly that.
	if strings.Contains(row, "• open ") {
		t.Errorf("DUPLEX row badges a session as open before any conversation started: %q", row)
	}
	if !strings.Contains(row, "gpt-live-1") {
		t.Errorf("the DUPLEX row never names the model that selected it — the model IS "+
			"the switch, so nothing else answers \"why is this on?\": %q", row)
	}
	// The row must FIT. The first version ran to "opens with the next" and the
	// panel ate the rest, which is §9 rule 12 in one line: it looked right in
	// source and was cut on screen.
	if !strings.Contains(row, "$0.05/min while open") {
		t.Errorf("the DUPLEX row is truncated, or no longer says what a session costs — "+
			"this is the only row in Helix that bills by the second: %q", row)
	}
}

// An install that did not ask for full duplex must not grow a row about it.
func TestE2E_DuplexRowIsAbsentOnAnOrdinaryInstall(t *testing.T) {
	t.Setenv("HELIX_E2E_SPEECH", "1")
	t.Setenv("HELIX_E2E_STT", "1")
	h := newHarness(t, `{"intent":"chat","response":"ok"}`)
	defer h.Close()

	if err := h.SendExpect("/blackbox status", "TRANSCRIPT", 20*time.Second); err != nil {
		t.Fatalf("status panel never rendered: %v\n%s", err, h.stripped())
	}
	if row := duplexRow(h.stripped()); row != "" {
		t.Errorf("an install that never selected full duplex shows: %q", row)
	}
}

// duplexRow returns the rendered DUPLEX line, or "".
func duplexRow(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "DUPLEX") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
