//go:build !windows

// tests/e2e/confinement_e2e_test.go
// Purpose: Live proof that /sandbox strict enforces writes at the kernel:
// an outside-root touch must fail while an inside-root touch succeeds.
// On hosts without a confinement backend, verifies the advisory fallback
// warning instead.
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"helix/internal/confinement"
)

// TestE2E_StrictConfinementKernelEnforced proves kernel enforcement end-to-end.
func TestE2E_StrictConfinementKernelEnforced(t *testing.T) {
	if !confinement.Supported() {
		t.Skipf("kernel confinement unavailable on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	h := newHarness(t, unusedPlan)
	defer h.Close()

	h.WriteLine("/sandbox strict")
	if err := h.Expect("Sandbox mode set to: Strict", 10*time.Second); err != nil {
		t.Fatal(err)
	}

	// Outside-root write must be denied by the kernel. SendExpect waits for
	// THIS command's completion marker, not the one the /sandbox slash
	// command already printed.
	evil := fmt.Sprintf("/tmp/helix_e2e_evil_%d", time.Now().UnixNano())
	if err := h.SendExpect("touch "+evil, "GRID STATUS", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(evil); err == nil {
		t.Fatal("kernel confinement failed: write outside the jail succeeded")
	}

	// Inside-root write must succeed.
	if err := h.SendExpect("touch confined_ok.txt", "GRID STATUS", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	h.ExpectFile(t, filepath.Join(h.project, "confined_ok.txt"), 10*time.Second)
}

// TestE2E_StrictFallbackWarningWhenUnsupported verifies graceful degradation.
func TestE2E_StrictFallbackWarningWhenUnsupported(t *testing.T) {
	if confinement.Supported() {
		t.Skip("confinement backend available; fallback path not applicable")
	}
	h := newHarness(t, unusedPlan)
	defer h.Close()
	h.WriteLine("/sandbox strict")
	if err := h.Expect("advisory", 10*time.Second); err != nil {
		t.Fatal(err)
	}
}
