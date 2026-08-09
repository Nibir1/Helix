// internal/confinement/confinement.go
// Purpose: Kernel-grade write confinement for /sandbox strict. Detects the best
// OS backend (macOS Seatbelt, Linux bwrap, Linux Landlock) and rewrites child
// argv so writes outside the jail root are denied BY THE KERNEL, not by string
// matching. Unavailable platforms fail closed to advisory mode with a warning.
// Author: Helix Hardening (Phase 13)
// Dependencies: stdlib only (CGO-free).
package confinement

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Backend identifies a confinement engine.
type Backend string

const (
	BackendNone     Backend = "none (advisory only)"
	BackendSeatbelt Backend = "seatbelt (macOS sandbox-exec, best-effort; deprecated by Apple)"
	BackendBwrap    Backend = "bwrap (Linux bubblewrap namespaces)"
	BackendLandlock Backend = "landlock (Linux kernel LSM, re-exec child)"
)

// Profile describes one confinement request.
type Profile struct {
	Root    string   // read-write jail root (symlink-resolved)
	ExtraRW []string // additional writable directories (optional)
	Cwd     string   // working directory for the Landlock re-exec child
}

var (
	detectOnce sync.Once
	detected   Backend
)

// Detect returns the best available backend for this platform (cached).
//
// Args: none. Returns: Backend. Complexity: O(1) after first probe.
func Detect() Backend {
	detectOnce.Do(func() { detected = detectBackend() })
	return detected
}

// Supported reports whether kernel-grade confinement exists on this host.
func Supported() bool { return Detect() != BackendNone }

// BackendName returns a human-readable backend label (for /doctor).
func BackendName() string { return string(Detect()) }

// ResolveRoot canonicalizes a jail root so profiles compare symlink-free paths.
//
// Args: root: raw directory. Returns: resolved path. Complexity: O(path depth).
func ResolveRoot(root string) string {
	if r, err := filepath.EvalSymlinks(root); err == nil {
		return r
	}
	return root
}

func lookPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// BuildSeatbeltProfile renders the macOS sandbox-exec profile. Seatbelt is
// last-match-wins: allow default, deny all writes, then re-allow the jail.
//
// Args: p: confinement profile. Returns: profile text. Complexity: O(len(ExtraRW)).
func BuildSeatbeltProfile(p Profile) string {
	var sb strings.Builder
	sb.WriteString("(version 1)\n")
	sb.WriteString("(allow default)\n")
	sb.WriteString("(deny file-write* (subpath \"/\"))\n")
	sb.WriteString("(allow file-write* (subpath (param \"HELIX_ROOT\")))\n")
	for i := range p.ExtraRW {
		fmt.Fprintf(&sb, "(allow file-write* (subpath (param \"HELIX_EXTRA_%d\")))\n", i)
	}
	return sb.String()
}

// BuildBwrapArgs renders bubblewrap argv: read-only /, writable jail root,
// fresh /proc and /dev, PID/IPC isolation, die-with-parent.
//
// Args: p: confinement profile. Returns: bwrap argv prefix. Complexity: O(len(ExtraRW)).
func BuildBwrapArgs(p Profile) []string {
	args := []string{
		"bwrap",
		"--ro-bind", "/", "/",
		"--bind", p.Root, p.Root,
		"--proc", "/proc",
		"--dev", "/dev",
		"--unshare-pid", "--unshare-ipc",
		"--die-with-parent",
	}
	for _, e := range p.ExtraRW {
		args = append(args, "--bind", e, e)
	}
	return args
}

// childSpec is the JSON contract for the Landlock re-exec child.
type childSpec struct {
	Root    string   `json:"root"`
	ExtraRW []string `json:"extra_rw"`
	Cwd     string   `json:"cwd"`
}

// BuildLandlockChildArgs renders the `helix --confined-child <spec> -- <cmd>`
// argv used when only Landlock is available (Landlock is self-imposing, so the
// child must apply the ruleset to itself before exec'ing the shell).
//
// Args: exe: current executable; p: profile; argv: command to run.
// Returns: full argv or error. Complexity: O(1).
func BuildLandlockChildArgs(exe string, p Profile, argv []string) ([]string, error) {
	spec, err := json.Marshal(childSpec(p))
	if err != nil {
		return nil, fmt.Errorf("marshal confinement spec: %w", err)
	}
	out := []string{exe, "--confined-child", string(spec), "--"}
	return append(out, argv...), nil
}

// WrapCommand rewrites argv so the child runs kernel-confined.
// Returns ok=false when confinement is unavailable (caller stays advisory).
//
// Args: argv: command argv; p: confinement profile.
// Returns: confined argv + ok flag. Complexity: O(1) plus backend probes (cached).
func WrapCommand(argv []string, p Profile) ([]string, bool) {
	p.Root = ResolveRoot(p.Root)
	for i := range p.ExtraRW {
		p.ExtraRW[i] = ResolveRoot(p.ExtraRW[i])
	}
	return wrapCommand(argv, p)
}
