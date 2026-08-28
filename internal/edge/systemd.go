// internal/edge/systemd.go
//
// Purpose: the `systemd --user` unit for `helix daemon` on headless Linux
// boards (BlackBox P10.4).
//
// The unit lives here rather than inline in the installer because two of its
// directives are load-bearing corrections, not cosmetics, and both deserve to
// be asserted by tests:
//
//   - `After=network-online.target` WITHOUT `Wants=` is a well-known systemd
//     footgun: After only orders against a target that something else pulls in.
//     Nothing else pulls network-online on a minimal headless image, so the
//     ordering silently does nothing and the daemon starts before the network
//     exists — its first acts being a connectivity probe and a cloud STT call.
//
//   - A `--user` service stops at logout and never starts at boot unless
//     lingering is enabled for the account. On an appliance nobody logs into,
//     that is the difference between "installed" and "actually runs".
package edge

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

// SystemdUnitName is the file name written under ~/.config/systemd/user.
const SystemdUnitName = "helix-daemon.service"

// SystemdUnit renders the user-level unit for `helix daemon`.
//
// Args:
//   - execPath: absolute path to the helix binary.
//
// Returns: the unit file contents.
// Complexity: O(1).
func SystemdUnit(execPath string) string {
	return fmt.Sprintf(`[Unit]
Description=Helix BlackBox daemon (voice-first AI appliance)

# Wants= is required, not merely After=. After only ORDERS the unit against a
# target; it does not pull that target in. On a minimal headless image nothing
# else requests network-online, so After alone would be silently inert and the
# daemon would start before the network exists — while its first actions are a
# connectivity probe and (on the cloud path) a remote STT/TTS call.
Wants=network-online.target
After=network-online.target sound.target

# Stop restarting a hopelessly broken daemon instead of hammering a small board.
# These are [Unit] options on systemd >= 230; older systemd expected them under
# [Service], but every board in the deployment matrix ships something newer
# (the oldest, Jetson Nano / Ubuntu 18.04, has systemd 237).
StartLimitIntervalSec=300
StartLimitBurst=5

[Service]
Type=simple
ExecStart=%s daemon
Restart=on-failure
RestartSec=5
TimeoutStopSec=15

# The daemon roots its own working directory at $HOME (it has no meaningful
# launch cwd under systemd), but stating it makes relative paths predictable.
WorkingDirectory=%%h

# Edge knobs — uncomment as needed. NOTE: a literal %% must be doubled in a
# systemd unit, because %% introduces a specifier.
#   Wrong mic picked up on a board with several: see /mictest
#Environment=HELIX_AUDIO_DEVICE=plughw:1,0
#   Noisy room or a hot mic tripping endpointing early:
#Environment=HELIX_SOX_SILENCE_PCT=2%%%%
#   User-managed llama.cpp sidecar (P11.4), e.g. on a Jetson Nano:
#Environment=HELIX_LLAMACPP_URL=http://127.0.0.1:8080

[Install]
WantedBy=default.target
`, execPath)
}

// LingerEnabled reports whether systemd lingering is on for the given user,
// i.e. whether their --user services survive logout and start at boot.
//
// systemd records this as a marker file under /var/lib/systemd/linger. Reading
// it avoids shelling out to loginctl, which may not exist in a container.
//
// Args:
//   - username: account name ("" → the current user).
//
// Returns: enabled, and whether the state could be determined at all.
// Complexity: O(1) stat.
func LingerEnabled(username string) (enabled, known bool) {
	if username == "" {
		u, err := user.Current()
		if err != nil {
			return false, false
		}
		username = u.Username
	}
	dir := lingerDir
	if _, err := os.Stat(dir); err != nil {
		// No linger directory at all: either not systemd, or a systemd too old
		// to record it. Do not claim knowledge we do not have.
		return false, false
	}
	if _, err := os.Stat(filepath.Join(dir, username)); err == nil {
		return true, true
	}
	return false, true
}

// lingerDir is systemd's linger marker directory, overridable for tests.
var lingerDir = "/var/lib/systemd/linger"

// SystemdEdgeNotes returns the post-install guidance for a headless board.
//
// The linger instruction is first and unconditional-ish because it is the one
// step whose omission makes an otherwise perfect install do nothing at all on
// an appliance nobody logs into.
//
// Args:
//   - username: account the service runs as ("" → current user).
//   - lingerOn, lingerKnown: from LingerEnabled.
//
// Returns: lines to print, in order.
// Complexity: O(1).
func SystemdEdgeNotes(username string, lingerOn, lingerKnown bool) []string {
	if username == "" {
		if u, err := user.Current(); err == nil {
			username = u.Username
		} else {
			username = "$USER"
		}
	}

	notes := []string{
		"Enable now:  systemctl --user daemon-reload && systemctl --user enable --now helix-daemon",
		"Logs:        journalctl --user -u helix-daemon -f",
	}

	switch {
	case lingerKnown && lingerOn:
		notes = append(notes,
			"Boot start:  lingering is ENABLED — the daemon starts at boot and survives logout.")
	case lingerKnown:
		notes = append(notes,
			"Boot start:  lingering is OFF — a --user service stops at logout and does NOT start at boot.",
			"             On a headless appliance this means the daemon never runs. Fix:",
			"               sudo loginctl enable-linger "+username)
	default:
		notes = append(notes,
			"Boot start:  a --user service stops at logout and does not start at boot unless lingering is on:",
			"               sudo loginctl enable-linger "+username)
	}

	notes = append(notes,
		"Audio:       microphone and speaker access need group membership:",
		"               sudo usermod -aG audio "+username+"   (re-login to apply)",
		"Silent build? The default Linux binary is CGO-free and cannot speak.",
		"               Rebuild with -tags audio_cgo to hear TTS (docs/edge_deployment.md §3.1).",
		"Verify:      run `helix` then /doctor — the Edge appliance section reports what is really in force.")

	return notes
}

// SystemdUnitPath returns the user-unit destination inside a home directory.
func SystemdUnitPath(home string) string {
	return filepath.Join(home, ".config", "systemd", "user", SystemdUnitName)
}

// EnvironmentKnobs lists the edge environment variables the unit documents.
// It keeps the template, docs/edge_deployment.md §6, and the tests in
// agreement about which knobs exist.
func EnvironmentKnobs() []string {
	return []string{"HELIX_AUDIO_DEVICE", "HELIX_SOX_SILENCE_PCT", "HELIX_LLAMACPP_URL"}
}
