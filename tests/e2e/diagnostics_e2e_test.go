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

	h.WriteLine("/purge")
	if err := h.Expect("FULL PURGE", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	h.WriteLine("y")
	if err := h.Expect("model weights", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	h.WriteLine("n")
	if err := h.Expect("PURGE COMPLETE", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(crashPath); err == nil {
		t.Fatal("crash report must be deleted by /purge")
	}
}
