// internal/edge/systemd_test.go
// Purpose: BlackBox P10.4 — the systemd --user unit is syntactically sound and
// carries the two directives that make it actually work on a headless board.
package edge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdUnitHasRequiredSections(t *testing.T) {
	unit := SystemdUnit("/usr/local/bin/helix")

	for _, section := range []string{"[Unit]", "[Service]", "[Install]"} {
		if !strings.Contains(unit, section) {
			t.Errorf("unit is missing the %s section", section)
		}
	}
	if !strings.Contains(unit, "ExecStart=/usr/local/bin/helix daemon") {
		t.Errorf("ExecStart must invoke the given binary:\n%s", unit)
	}
	if !strings.Contains(unit, "WantedBy=default.target") {
		t.Error("a --user service must be WantedBy=default.target to be enable-able")
	}
}

// The footgun this template exists to fix: After= alone does not pull the
// target in, so on a minimal headless image the ordering is silently inert and
// the daemon races the network it is about to probe.
func TestSystemdUnitPullsInNetworkOnline(t *testing.T) {
	unit := SystemdUnit("/usr/local/bin/helix")

	if !strings.Contains(unit, "Wants=network-online.target") {
		t.Fatal("After=network-online.target without Wants= is inert — the daemon " +
			"would start before the network exists")
	}
	if !strings.Contains(unit, "After=network-online.target") {
		t.Error("ordering against the network target is still required")
	}
}

// Restart storms on a small board are worse than a stopped service.
func TestSystemdUnitBoundsRestartStorms(t *testing.T) {
	unit := SystemdUnit("/usr/local/bin/helix")

	if !strings.Contains(unit, "Restart=on-failure") {
		t.Error("the daemon must be restarted on failure")
	}
	for _, k := range []string{"StartLimitIntervalSec=", "StartLimitBurst="} {
		if !strings.Contains(unit, k) {
			t.Errorf("missing restart-storm bound %q", k)
		}
	}

	// These are [Unit] options on systemd >= 230; under [Service] modern
	// systemd logs "Unknown lvalue" and silently ignores them.
	//
	// Sections are resolved by scanning for headers at the START of a line —
	// a naive strings.Index would match the literal "[Service]" inside the
	// template's own explanatory comment.
	if got := sectionOf(unit, "StartLimitIntervalSec="); got != "[Unit]" {
		t.Errorf("StartLimitIntervalSec is in %s, must be in [Unit]", got)
	}
	if got := sectionOf(unit, "StartLimitBurst="); got != "[Unit]" {
		t.Errorf("StartLimitBurst is in %s, must be in [Unit]", got)
	}
	if got := sectionOf(unit, "Restart=on-failure"); got != "[Service]" {
		t.Errorf("Restart is in %s, must be in [Service]", got)
	}
}

// sectionOf returns the section header a directive appears under, ignoring
// comment lines so the template's prose cannot confuse the lookup.
func sectionOf(unit, directive string) string {
	current := ""
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(line, "[") && strings.HasSuffix(strings.TrimSpace(line), "]") {
			current = strings.TrimSpace(line)
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), directive) {
			return current
		}
	}
	return "<not found>"
}

// systemd treats % as a specifier introducer, so a literal percent in a value
// must be doubled or the unit fails to load.
func TestSystemdUnitEscapesPercentCorrectly(t *testing.T) {
	unit := SystemdUnit("/usr/local/bin/helix")

	// %h (home) must survive as a single-percent specifier.
	if !strings.Contains(unit, "WorkingDirectory=%h") {
		t.Errorf("the %%h specifier was mangled:\n%s", unit)
	}
	// The sox silence knob is a percentage value; it must appear doubled.
	if !strings.Contains(unit, "HELIX_SOX_SILENCE_PCT=2%%") {
		t.Error("a literal % in an Environment value must be doubled for systemd")
	}
}

// Every line must be a section header, a comment, blank, or key=value —
// the cheapest way to catch a malformed template without systemd present.
func TestSystemdUnitLinesAreWellFormed(t *testing.T) {
	for _, line := range strings.Split(SystemdUnit("/usr/local/bin/helix"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "",
			strings.HasPrefix(trimmed, "#"),
			strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]"):
			continue
		}
		if !strings.Contains(trimmed, "=") {
			t.Errorf("malformed unit line (not a comment, header, or key=value): %q", trimmed)
		}
	}
}

// The edge knobs documented in the unit must match the ones the codebase and
// docs/edge_deployment.md actually honor.
func TestSystemdUnitDocumentsEdgeKnobs(t *testing.T) {
	unit := SystemdUnit("/usr/local/bin/helix")
	for _, knob := range EnvironmentKnobs() {
		if !strings.Contains(unit, knob) {
			t.Errorf("the unit should document the %s knob", knob)
		}
		// Documented, not active: an uncommented default would override the
		// user's own config.
		if strings.Contains(unit, "\nEnvironment="+knob) {
			t.Errorf("%s must be commented out, not applied by default", knob)
		}
	}
}

func TestSystemdUnitPath(t *testing.T) {
	got := SystemdUnitPath("/home/pi")
	want := filepath.Join("/home/pi", ".config", "systemd", "user", "helix-daemon.service")
	if got != want {
		t.Fatalf("unit path = %q, want %q", got, want)
	}
}

func TestLingerEnabledReadsMarker(t *testing.T) {
	dir := t.TempDir()
	old := lingerDir
	t.Cleanup(func() { lingerDir = old })
	lingerDir = dir

	if on, known := LingerEnabled("pi"); !known || on {
		t.Fatalf("absent marker means known-and-off, got on=%v known=%v", on, known)
	}

	if err := os.WriteFile(filepath.Join(dir, "pi"), nil, 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if on, known := LingerEnabled("pi"); !known || !on {
		t.Fatalf("present marker means known-and-on, got on=%v known=%v", on, known)
	}

	// No linger directory at all: do not claim knowledge we do not have.
	lingerDir = filepath.Join(dir, "definitely-absent")
	if on, known := LingerEnabled("pi"); known || on {
		t.Fatalf("missing linger dir must report unknown, got on=%v known=%v", on, known)
	}
}

// Lingering is the difference between "installed" and "actually runs" on an
// appliance nobody logs into, so a disabled state must be called out loudly
// with the exact fix.
func TestSystemdEdgeNotesWarnAboutLinger(t *testing.T) {
	off := strings.Join(SystemdEdgeNotes("pi", false, true), "\n")
	if !strings.Contains(off, "lingering is OFF") {
		t.Error("a disabled linger state must be stated plainly")
	}
	if !strings.Contains(off, "loginctl enable-linger pi") {
		t.Error("the note must give the exact remediation command with the user")
	}
	if !strings.Contains(off, "never runs") {
		t.Error("the consequence on a headless board must be spelled out")
	}

	on := strings.Join(SystemdEdgeNotes("pi", true, true), "\n")
	if strings.Contains(on, "lingering is OFF") {
		t.Error("an enabled linger state must not warn")
	}
	if !strings.Contains(on, "ENABLED") {
		t.Error("an enabled linger state should be confirmed")
	}

	// Unknown state must still surface the requirement rather than stay silent.
	unknown := strings.Join(SystemdEdgeNotes("pi", false, false), "\n")
	if !strings.Contains(unknown, "enable-linger") {
		t.Error("an unknown linger state must still mention the requirement")
	}
}

func TestSystemdEdgeNotesCoverAudioAndVerification(t *testing.T) {
	notes := strings.Join(SystemdEdgeNotes("pi", true, true), "\n")

	for _, want := range []string{
		"usermod -aG audio pi", // mic/speaker access
		"audio_cgo",            // the silent-build gotcha
		"/doctor",              // how to verify what is really in force
		"journalctl --user",    // where the logs are
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("edge notes should mention %q:\n%s", want, notes)
		}
	}
}
