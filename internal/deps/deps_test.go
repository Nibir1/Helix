// internal/deps/deps_test.go
// Purpose: the catalog is a promise Helix makes in someone else's shell, so the
// properties worth pinning are the honest ones — never emit a command we cannot
// stand behind, and never claim a tool is missing when the capability is there.
package deps

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fakePath makes PATH deterministic: these tests must not depend on what the
// machine running them happens to have installed.
func fakePath(t *testing.T, present ...string) {
	t.Helper()
	set := make(map[string]bool, len(present))
	for _, p := range present {
		set[p] = true
	}
	saved := lookPath
	lookPath = func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { lookPath = saved })
}

// Detection is by CAPABILITY, not by package: someone with `rec` but no `sox`
// binary, or a source build, is not missing anything.
func TestPresentAsksWhetherTheCapabilityExists(t *testing.T) {
	sox := Catalog()[0]
	if sox.Name != "sox" {
		t.Fatalf("catalog order changed; first entry is %q", sox.Name)
	}

	fakePath(t, "rec")
	if !sox.Present() {
		t.Error("rec alone must satisfy sox — it is the recording front end")
	}

	fakePath(t, "sox")
	if !sox.Present() {
		t.Error("sox alone must satisfy sox")
	}

	fakePath(t)
	if sox.Present() {
		t.Error("nothing on PATH must not satisfy sox")
	}
}

func TestMissingReflectsThePath(t *testing.T) {
	fakePath(t, "sox", "ffmpeg")
	if got := Missing(); len(got) != 0 {
		t.Errorf("nothing should be missing, got %+v", got)
	}

	fakePath(t, "sox")
	got := Missing()
	if len(got) != 1 || got[0].Name != "ffmpeg" {
		t.Errorf("missing = %+v, want only ffmpeg", got)
	}
}

func TestDetectManagerPrefersWhatIsInstalled(t *testing.T) {
	fakePath(t, "brew")
	if m := DetectManager(); m != ManagerBrew {
		t.Errorf("manager = %q, want brew", m)
	}
	fakePath(t, "apt-get")
	if m := DetectManager(); m != ManagerApt {
		t.Errorf("manager = %q, want apt", m)
	}
	fakePath(t)
	if m := DetectManager(); m != ManagerUnknown {
		t.Errorf("manager = %q, want unknown on a bare host", m)
	}
}

// The property that matters most: Helix never prints an install command it
// cannot stand behind. A wrong package name fails in the user's shell with
// Helix's name on it.
func TestNoInstallCommandIsInventedForAnUnknownPackage(t *testing.T) {
	for _, d := range Catalog() {
		for _, m := range []Manager{ManagerBrew, ManagerApt, ManagerDnf, ManagerPacman,
			ManagerZypper, ManagerApk, ManagerWinget, ManagerChoco, ManagerUnknown} {
			cmd, ok := d.InstallCommand(m)
			if _, known := d.Packages[m]; !known || m == ManagerUnknown {
				if ok {
					t.Errorf("%s/%s: offered %q for a package we do not know", d.Name, m, cmd)
				}
				continue
			}
			if !ok {
				t.Errorf("%s/%s: known package produced no command", d.Name, m)
				continue
			}
			if !strings.Contains(cmd, d.Packages[m]) {
				t.Errorf("%s/%s: command %q does not install the catalogued package", d.Name, m, cmd)
			}
			if !strings.HasPrefix(cmd, string(m)) && !strings.HasPrefix(cmd, "sudo "+string(m)) {
				t.Errorf("%s/%s: command %q is not run by that manager", d.Name, m, cmd)
			}
		}
	}
}

// Linux installs need root, and the escalation must be visible in the command
// the user is shown — never slipped in somewhere else.
func TestLinuxCommandsAskForRootOutLoud(t *testing.T) {
	ffmpeg := Catalog()[1]
	for _, m := range []Manager{ManagerApt, ManagerDnf, ManagerPacman, ManagerZypper, ManagerApk} {
		cmd, ok := ffmpeg.InstallCommand(m)
		if !ok {
			t.Fatalf("%s has no ffmpeg command", m)
		}
		if !strings.HasPrefix(cmd, "sudo ") {
			t.Errorf("%s: %q must show its privilege escalation", m, cmd)
		}
	}
	// macOS brew must NOT, because brew refuses to run under sudo.
	cmd, _ := ffmpeg.InstallCommand(ManagerBrew)
	if strings.Contains(cmd, "sudo") {
		t.Errorf("brew command must not use sudo: %q", cmd)
	}
}

// Non-interactive flags matter: an install run from inside Helix that stops at
// a [y/N] prompt looks like a hang, because Helix already asked.
func TestInstallCommandsAreNonInteractive(t *testing.T) {
	ffmpeg := Catalog()[1]
	for m, wantFlag := range map[Manager]string{
		ManagerApt:    "-y",
		ManagerDnf:    "-y",
		ManagerPacman: "--noconfirm",
		ManagerZypper: "-y",
		ManagerChoco:  "-y",
		ManagerWinget: "-e",
	} {
		cmd, ok := ffmpeg.InstallCommand(m)
		if !ok {
			t.Fatalf("%s has no ffmpeg command", m)
		}
		if !strings.Contains(cmd, wantFlag) {
			t.Errorf("%s: %q should carry %q so it does not stall on a prompt", m, cmd, wantFlag)
		}
	}
}

// Every catalogued tool must explain itself in terms of what the user loses.
func TestCatalogEntriesArePurposeful(t *testing.T) {
	for _, d := range Catalog() {
		if d.Name == "" || d.Purpose == "" {
			t.Errorf("catalog entry %+v is missing a name or a purpose", d)
		}
		if len(d.Binaries) == 0 {
			t.Errorf("%s has no way to be detected", d.Name)
		}
		if d.Required {
			t.Errorf("%s is marked required, but text Helix runs without every "+
				"catalogued tool — a required entry would block first run", d.Name)
		}
	}
}

func TestManagerHintIsAlwaysActionable(t *testing.T) {
	if h := ManagerHint(); h == "" || !strings.Contains(strings.ToLower(h), "install") {
		t.Errorf("hint = %q, want something the user can act on", h)
	}
}

// Guard the real probe, since the tests above all stub it out.
func TestLookPathDefaultsToExec(t *testing.T) {
	if lookPath == nil {
		t.Fatal("lookPath must be wired")
	}
	// A binary that exists on every supported host.
	if _, err := exec.LookPath("ls"); err == nil {
		if _, err := lookPath("ls"); err != nil {
			t.Error("the default probe should find what exec.LookPath finds")
		}
	}
}
