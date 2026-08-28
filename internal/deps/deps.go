// Package deps knows what Helix needs from the host system, how to tell
// whether it is there, and how to install it on this particular machine.
//
// Purpose: a fresh install of a precompiled binary has no way to know that
// speaking to Helix needs sox and seeing needs ffmpeg. Before this package,
// each site that hit a missing tool printed its own `brew install …` hint —
// eight of them across the tree, macOS-only, and all of them reached AFTER the
// user had already tried to use the feature and watched it fail. Setup is the
// right time to learn that a microphone needs a recorder.
//
// Two rules, both inherited from the sidecar installers this mirrors:
//
//  1. Detect by capability, not by package. The question is "is there a binary
//     that can record audio", not "is the sox package installed" — a user who
//     built sox from source, or who has ffmpeg instead, is not missing anything.
//  2. Offer only an unambiguous command. Where Helix does not know a single
//     correct install command for the host, it says so and points at the docs
//     rather than guessing at a package name. A wrong `install` line is worse
//     than none: it fails in the user's shell with Helix's name on it.
//
// This package decides WHAT and HOW; it never executes anything. Running an
// install is the caller's job, behind the caller's confirmation.
package deps

import (
	"os/exec"
	"runtime"
)

// Manager is a host package manager Helix can name an install command for.
type Manager string

const (
	ManagerBrew    Manager = "brew"
	ManagerApt     Manager = "apt"
	ManagerDnf     Manager = "dnf"
	ManagerPacman  Manager = "pacman"
	ManagerZypper  Manager = "zypper"
	ManagerApk     Manager = "apk"
	ManagerWinget  Manager = "winget"
	ManagerChoco   Manager = "choco"
	ManagerUnknown Manager = ""
)

// managerProbes maps a manager to the binary that proves it is present, in the
// order they are tried. Order matters only where two can coexist.
var managerProbes = []struct {
	manager Manager
	binary  string
}{
	{ManagerBrew, "brew"},
	{ManagerApt, "apt-get"},
	{ManagerDnf, "dnf"},
	{ManagerPacman, "pacman"},
	{ManagerZypper, "zypper"},
	{ManagerApk, "apk"},
	{ManagerWinget, "winget"},
	{ManagerChoco, "choco"},
}

// lookPath is the PATH probe, swappable so tests never depend on what the
// machine running them happens to have installed.
var lookPath = exec.LookPath

// DetectManager returns the host's package manager, or ManagerUnknown.
func DetectManager() Manager {
	for _, p := range managerProbes {
		if _, err := lookPath(p.binary); err == nil {
			return p.manager
		}
	}
	return ManagerUnknown
}

// Dependency is one external tool Helix uses.
type Dependency struct {
	// Name is what the user calls it.
	Name string

	// Purpose says what stops working without it, in the user's terms — not
	// "audio backend" but "speaking to Helix".
	Purpose string

	// Binaries satisfy this dependency; ANY one of them present is enough.
	Binaries []string

	// Packages is the package name per manager. A manager absent from the map
	// means Helix does not know a correct command for it and will say so.
	Packages map[Manager]string

	// Required marks a dependency Helix cannot work at all without. None are,
	// today: every one of these buys a capability, and text Helix runs fine
	// without any of them.
	Required bool

	// Optional keeps a dependency OUT of the first-run offer while leaving it
	// installable on demand.
	//
	// The catalogue is deliberately short, because a setup flow that asks about
	// a dozen tools is one people quit halfway through. But "not worth
	// mentioning on first boot" and "Helix has no idea how to install this"
	// are different statements, and the wizard needs the second one to be rare.
	// An optional entry is skipped by Missing() and found by Lookup().
	Optional bool
}

// Lookup returns a catalogue entry by name, including optional ones.
//
// This is how a wizard resolves a prerequisite it has just discovered is
// missing, rather than reporting a dead end at the moment the user has already
// chosen the thing that needs it.
func Lookup(name string) (Dependency, bool) {
	for _, d := range Catalog() {
		if d.Name == name {
			return d, true
		}
	}
	return Dependency{}, false
}

// Catalog is everything Helix would like the host to have.
//
// Deliberately short. A setup flow that asks about a dozen optional tools is a
// flow people quit halfway through, so this lists only what unlocks a headline
// capability the user came for.
func Catalog() []Dependency {
	return []Dependency{
		{
			Name:    "sox",
			Purpose: "microphone capture — speaking to Helix at all",
			// `rec` is sox's recording front end and ships split from `sox`
			// itself on some installs; either proves the capability.
			Binaries: []string{"rec", "sox"},
			Packages: map[Manager]string{
				ManagerBrew:   "sox",
				ManagerApt:    "sox",
				ManagerDnf:    "sox",
				ManagerPacman: "sox",
				ManagerZypper: "sox",
				ManagerApk:    "sox",
				// No Windows entry on purpose: the winget and choco IDs for sox
				// are not stable enough to put in a command Helix runs for
				// someone. Guidance beats a confident wrong package name.
			},
		},
		{
			Name:    "python3",
			Purpose: "the piper voice server, when the standalone binary is unavailable",
			// Optional: Helix prefers the interpreter-free piper binary, which
			// it downloads and checksum-verifies itself. This entry exists so a
			// host that needs the Python path is INSTALLED rather than skipped
			// — the wizard used to reach "no single install command" and stop,
			// which left the user with a chain it had just told them to pick.
			Optional: true,
			Binaries: []string{"python3", "python"},
			Packages: map[Manager]string{
				ManagerBrew:   "python",
				ManagerApt:    "python3",
				ManagerDnf:    "python3",
				ManagerPacman: "python",
				ManagerZypper: "python3",
				ManagerApk:    "python3",
				ManagerWinget: "Python.Python.3.12",
				ManagerChoco:  "python",
			},
		},
		{
			Name:    "git",
			Purpose: "fetching sources Helix builds locally, such as the CSM voice server",
			// Optional: only a from-source sidecar needs it, and most users
			// never build one.
			Optional: true,
			Binaries: []string{"git"},
			Packages: map[Manager]string{
				ManagerBrew:   "git",
				ManagerApt:    "git",
				ManagerDnf:    "git",
				ManagerPacman: "git",
				ManagerZypper: "git",
				ManagerApk:    "git",
				ManagerWinget: "Git.Git",
				ManagerChoco:  "git",
			},
		},
		{
			Name:    "rust",
			Purpose: "building the CSM voice server, which ships as source",
			// Optional, and cargo is the binary that matters: rustc alone
			// cannot build the crate.
			Optional: true,
			Binaries: []string{"cargo"},
			Packages: map[Manager]string{
				ManagerBrew:   "rust",
				ManagerApt:    "cargo",
				ManagerDnf:    "cargo",
				ManagerPacman: "rust",
				ManagerZypper: "cargo",
				ManagerApk:    "cargo",
				ManagerWinget: "Rustlang.Rustup",
				ManagerChoco:  "rust",
			},
		},
		{
			Name:     "ffmpeg",
			Purpose:  "camera frames for live mode, and a fallback audio recorder",
			Binaries: []string{"ffmpeg"},
			Packages: map[Manager]string{
				ManagerBrew:   "ffmpeg",
				ManagerApt:    "ffmpeg",
				ManagerDnf:    "ffmpeg",
				ManagerPacman: "ffmpeg",
				ManagerZypper: "ffmpeg",
				ManagerApk:    "ffmpeg",
				ManagerWinget: "Gyan.FFmpeg",
				ManagerChoco:  "ffmpeg",
			},
		},
	}
}

// Present reports whether any of the dependency's binaries is on PATH.
func (d Dependency) Present() bool {
	for _, b := range d.Binaries {
		if _, err := lookPath(b); err == nil {
			return true
		}
	}
	return false
}

// InstallCommand renders the command that would install this dependency on the
// given manager, and whether Helix knows one.
//
// Linux managers get `sudo` because they need it, and the command is shown to
// the user before it runs — a hidden privilege escalation would be far worse
// than a visible one.
func (d Dependency) InstallCommand(m Manager) (string, bool) {
	pkg, ok := d.Packages[m]
	if !ok || pkg == "" {
		return "", false
	}
	switch m {
	case ManagerBrew:
		return "brew install " + pkg, true
	case ManagerApt:
		return "sudo apt-get install -y " + pkg, true
	case ManagerDnf:
		return "sudo dnf install -y " + pkg, true
	case ManagerPacman:
		return "sudo pacman -S --noconfirm " + pkg, true
	case ManagerZypper:
		return "sudo zypper install -y " + pkg, true
	case ManagerApk:
		return "sudo apk add " + pkg, true
	case ManagerWinget:
		return "winget install --id " + pkg + " -e --source winget", true
	case ManagerChoco:
		return "choco install -y " + pkg, true
	default:
		return "", false
	}
}

// Missing returns the catalog entries this host does not satisfy.
func Missing() []Dependency {
	var out []Dependency
	for _, d := range Catalog() {
		if d.Optional {
			continue // installable on demand, not part of the first-run offer
		}
		if !d.Present() {
			out = append(out, d)
		}
	}
	return out
}

// ManagerHint explains how to get a package manager, for a host that has none.
// Without it the "no install command" path is a dead end on exactly the
// machines that need the most help.
func ManagerHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "No package manager found. Install Homebrew from https://brew.sh, " +
			"then re-run setup."
	case "windows":
		return "No package manager found. winget ships with modern Windows; " +
			"otherwise install Chocolatey from https://chocolatey.org/install."
	default:
		return "No supported package manager found (apt, dnf, pacman, zypper, apk). " +
			"Install the tools below with whatever your distribution uses."
	}
}
