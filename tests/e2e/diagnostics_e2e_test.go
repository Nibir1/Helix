//go:build !windows

// tests/e2e/diagnostics_e2e_test.go
// Purpose: Live proof of telemetry-free crash diagnostics: the hidden selftest
// panic exits 42 with a redacted local report; /doctor surfaces reports and
// /purge removes them.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2E_SelftestPanicWritesRedactedReport proves the panic path end-to-end.
func TestE2E_SelftestPanicWritesRedactedReport(t *testing.T) {
	home := t.TempDir()
	cmd := exec.Command(binPath)
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"HELIX_SELFTEST_PANIC=1",
		"CUSTOM_API_KEY=sekrit-value-123",
	}
	out, err := cmd.CombinedOutput()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 42 {
		t.Fatalf("expected exit 42, got err=%v output=%s", err, out)
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".helix", "crash-*.json"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 crash report, got %d", len(matches))
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	report := string(data)
	if !strings.Contains(report, "[REDACTED]") {
		t.Fatal("expected secrets to be redacted in the report")
	}
	if strings.Contains(report, "sekrit-value-123") {
		t.Fatal("secret value leaked into the crash report")
	}
	if !strings.Contains(report, "selftest") {
		t.Fatal("expected the selftest panic reason in the report")
	}
}

// TestE2E_DoctorListsAndPurgeRemovesCrashReports proves the UX integration.
func TestE2E_DoctorListsAndPurgeRemovesCrashReports(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	crashPath := filepath.Join(h.home, ".helix", "crash-20260101T000000.000.json")
	payload := `{"version":"1.0.0","reason":"panic: boom","timestamp":"2026-01-01T00:00:00Z","env":{}}`
	if err := os.WriteFile(crashPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	h.WriteLine("/doctor")
	// "CRASH REPORTS" + "1 pending": /doctor renders as a panel now, so the row
	// label and the count are separate cells rather than one sentence.
	if err := h.Expect("CRASH REPORTS", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("1 pending", 10*time.Second); err != nil {
		t.Fatal(err)
	}

	// A download that the SECOND prompt governs. Seeded so the two-stage flow
	// is actually exercised: the weights prompt is only asked when there is
	// something to delete, and declining it has to leave the file alone.
	// A credential, so the group that makes a purge irreversible is on screen.
	if err := os.WriteFile(filepath.Join(h.home, ".helix", "secrets.json"),
		[]byte(`{"openai":"sk-not-a-real-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	weightsDir := filepath.Join(h.home, ".helix", "whisper-models")
	if err := os.MkdirAll(weightsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	weightFile := filepath.Join(weightsDir, "ggml-small.en.bin")
	if err := os.WriteFile(weightFile, []byte("weights"), 0o600); err != nil {
		t.Fatal(err)
	}

	h.WriteLine("/purge")
	// The manifest leads with the irreversibility, then names the group that
	// makes it irreversible.
	if err := h.Expect("permanent", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("CREDENTIALS", 10*time.Second); err != nil {
		t.Fatal("the manifest must call out credentials as their own group")
	}
	h.WriteLine("y")
	if err := h.Expect("download", 10*time.Second); err != nil {
		t.Fatal("a purge with downloads present must ask about them separately")
	}
	h.WriteLine("n")
	if err := h.Expect("blank slate", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(crashPath); err == nil {
		t.Fatal("crash report must be deleted by /purge")
	}
	// Declining the second prompt must actually keep them. The prompt used to
	// offer only ~/.helix/models while every download the wizard makes lands
	// elsewhere, so a YES deleted nothing and a NO was indistinguishable.
	if _, err := os.Stat(weightFile); err != nil {
		t.Fatal("declining the downloads prompt must leave the weights on disk")
	}
}

// TestE2E_DoctorAppliancePanelStaysInsideItsFrame pins the conversion.
//
// The appliance block used to print BETWEEN two panels as a flat stack starting
// at column zero: `--- Edge appliance ---` as a heading on a screen where every
// other heading is a chip, and twelve green lines of equal weight below it. The
// panel is what makes it one report rather than twelve adjacent facts, so the
// test asserts the frame, not the wording.
func TestE2E_DoctorAppliancePanelStaysInsideItsFrame(t *testing.T) {
	h := newHarness(t, "")
	defer h.Close()

	if err := h.SendExpect("/doctor", "APPLIANCE", 40*time.Second); err != nil {
		t.Fatal("the appliance diagnostics must render as their own panel")
	}
	// The rows a headless board depends on, each now a labelled row rather than
	// a sentence.
	for _, want := range []string{"PLATFORM", "AUDIO", "LOCAL SIDECARS"} {
		if err := h.Expect(want, 10*time.Second); err != nil {
			t.Fatalf("appliance panel is missing the %s row: %v", want, err)
		}
	}
	// And the flat heading it replaced must be gone, or both are shipping.
	if strings.Contains(h.stripped(), "--- Edge appliance ---") {
		t.Error("the flat heading survived the conversion")
	}
	// Every body line between the two chips belongs behind the gutter. This is
	// the invariant the old block broke, and the only one worth asserting: what
	// each row SAYS depends on the host, but nothing may escape the frame.
	body := h.stripped()
	start := strings.Index(body, "APPLIANCE")
	end := strings.Index(body[start:], "ENVIRONMENT")
	if end < 0 {
		t.Fatal("the ENVIRONMENT panel must follow the appliance panel")
	}
	for _, line := range strings.Split(body[start:start+end], "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "─") ||
			strings.HasPrefix(trimmed, "│") || strings.HasPrefix(trimmed, "▸") ||
			strings.Contains(trimmed, "APPLIANCE") {
			continue
		}
		t.Errorf("line escaped the appliance panel: %q", trimmed)
	}
}
