// internal/shell/loginenv.go
//
// Purpose: resolve the user's real login-shell environment so commands
// executed by Helix resolve binaries exactly like they would inside a normal
// terminal — on macOS, Linux, and Windows.
//
// Motivation: Helix can itself be the login shell (`chsh -s /usr/local/bin/helix`).
// In that configuration the process starts with the minimal login PATH
// (`/usr/bin:/bin:/usr/sbin:/sbin`) and none of the user's profile files ever
// run, so any command installed outside that minimal PATH (`code`, `brew`,
// nvm/pyenv/cargo-managed binaries, ...) fails with "command not found" even
// though it works in a regular terminal. The same stripping occurs for GUI
// launches. On Windows the environment is built from the registry and is
// always complete, so resolution is a no-op there.
//
// Approach (the strategy VS Code uses for its integrated terminal): spawn the
// user's real shell once as a login shell — detached into its own session so
// it can never touch Helix's terminal — capture its `env` output, and
// overlay the result onto Helix's own environment. A timeout and silent
// fallback keep a hanging or hostile profile from breaking startup.
package shell

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// loginEnvTimeout bounds a hanging user profile (one that waits for input).
// Past the deadline Helix keeps its inherited environment.
const loginEnvTimeout = 5 * time.Second

// loginEnvSentinel marks where machine-readable `env` output starts so noise
// printed by interactive rc files is ignored.
const loginEnvSentinel = "__HELIX_LOGIN_ENV__"

// loginEnvSkip lists variables never overlaid from the login snapshot:
// PATH is merged separately; the rest are process-local, stale (they belong
// to the probe shell's session), or authoritative for the running process.
var loginEnvSkip = map[string]bool{
	"PATH": true, "PWD": true, "OLDPWD": true, "SHLVL": true, "_": true,
	"TERM": true, "LINES": true, "COLUMNS": true, "HOME": true,
}

var loginEnvOnce sync.Once

// ApplyLoginEnvironment resolves the user's login-shell environment and
// overlays it onto the current process: exported variables are inherited and
// PATH becomes the union of (login PATH, inherited PATH, well-known local
// directories). After this call every child process Helix spawns — typed
// shell commands, agent steps, git/brew helpers, the non-interactive bridge —
// resolves binaries exactly like the user's terminal does.
//
// Safe to call multiple times (work happens once). All failures are silent
// and non-fatal: the well-known PATH directories are still merged so common
// tools (`code`, `brew`) keep working even with a broken profile.
// Set HELIX_DEBUG=1 for a trace.
func ApplyLoginEnvironment() {
	loginEnvOnce.Do(func() {
		if runtime.GOOS == "windows" {
			return
		}
		_, shellPath := detectShellFromEnv()
		if shellPath == "" || strings.Contains(strings.ToLower(shellPath), "helix") {
			debugf("no real user shell detected (%q); keeping inherited environment", shellPath)
			return
		}
		login := resolveLoginEnv(shellPath)
		entries := applyLoginEnvToCurrent(login)
		if len(login) == 0 {
			debugf("resolution failed for %s; merged well-known PATH directories only (%d entries)",
				shellPath, entries)
			return
		}
		debugf("applied %d variables from %s; PATH now has %d entries", len(login), shellPath, entries)
	})
}

// resolveLoginEnv runs `<shell> -l -c 'echo <sentinel>; env'` with a minimal
// seed environment and parses the exported variables. Login-only sourcing
// (no `-i`) is deliberate: an interactive probe would perform job-control
// ioctls on Helix's controlling terminal and corrupt raw-mode state, and
// rc-only PATH additions are covered by the well-known directory fallback.
// The probe runs in its own session (Setsid) so it can never touch the
// user's terminal even if a profile tries to.
func resolveLoginEnv(shellPath string) map[string]string {
	if _, err := os.Stat(shellPath); err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), loginEnvTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, shellPath, "-l", "-c", "echo "+loginEnvSentinel+"; env")
	cmd.Env = minimalProbeEnv(shellPath)
	cmd.SysProcAttr = sysProcAttrDetached()
	// Stdin stays nil (child reads /dev/null): profiles that call `read`
	// see EOF instead of blocking; any diagnostics go to discarded stderr.
	out, err := cmd.Output()
	if ctx.Err() != nil || err != nil {
		return nil
	}
	return parseLoginEnvOutput(string(out))
}

// minimalProbeEnv is the seed environment for the probe shell. Only the
// variables login/rc files plausibly need; everything else must come from
// the user's own profile so the captured snapshot reflects a real session.
func minimalProbeEnv(shellPath string) []string {
	env := []string{"SHELL=" + shellPath, "TERM=dumb", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		env = append(env, "HOME="+home)
	}
	if user := os.Getenv("USER"); user != "" {
		env = append(env, "USER="+user)
	} else if user := os.Getenv("USERNAME"); user != "" {
		env = append(env, "USER="+user)
	}
	return env
}

// parseLoginEnvOutput parses KEY=VALUE lines. Only output after the LAST
// sentinel is trusted; rc-file chatter before it is dropped. Multi-line
// values lose their continuation lines (those lines never parse as
// KEY=VALUE), matching the tolerance of comparable resolvers.
func parseLoginEnvOutput(out string) map[string]string {
	if idx := strings.LastIndex(out, loginEnvSentinel); idx >= 0 {
		out = out[idx+len(loginEnvSentinel):]
	}
	env := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := line[:eq]
		if !isValidEnvKey(key) {
			continue
		}
		env[key] = line[eq+1:]
	}
	return env
}

// isValidEnvKey reports whether k is a valid exported variable name
// (letter/underscore first, then letters/digits/underscores).
func isValidEnvKey(k string) bool {
	for i := 0; i < len(k); i++ {
		c := k[i]
		switch {
		case c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return len(k) > 0
}

// applyLoginEnvToCurrent overlays the login snapshot onto the process
// environment and unions PATH. A nil map still merges well-known PATH
// directories (last-resort net for `code`/`brew` with broken profiles).
// Returns the merged PATH entry count for debug reporting.
func applyLoginEnvToCurrent(login map[string]string) int {
	for k, v := range login {
		if loginEnvSkip[k] || strings.HasPrefix(k, "HELIX_") || strings.ContainsRune(v, 0) {
			continue
		}
		_ = os.Setenv(k, v)
	}
	merged := mergePathEntries(login["PATH"], os.Getenv("PATH"))
	_ = os.Setenv("PATH", merged)
	return len(filepath.SplitList(merged))
}

// mergePathEntries unions PATH strings in priority order: primary (the login
// snapshot — the user's preferred ordering), then secondary (Helix's
// inherited) entries not already present, then well-known directories that
// exist on this machine. Duplicates collapse on first appearance; explicit
// user entries are kept verbatim even when they point at missing directories.
func mergePathEntries(primary, secondary string) string {
	seen := make(map[string]bool)
	var out []string
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		out = append(out, dir)
	}
	for _, d := range filepath.SplitList(primary) {
		add(d)
	}
	for _, d := range filepath.SplitList(secondary) {
		add(d)
	}
	for _, d := range wellKnownPathDirs() {
		add(d)
	}
	if len(out) == 0 {
		return secondary
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// wellKnownPathDirs lists common user-local installation roots, filtered to
// directories that actually exist on this machine.
func wellKnownPathDirs() []string {
	dirs := []string{
		"/usr/local/bin", "/usr/local/sbin",
		"/opt/homebrew/bin", "/opt/homebrew/sbin",
		"/snap/bin",
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "bin"),
			filepath.Join(home, ".cargo", "bin"),
		)
	}
	existing := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			existing = append(existing, d)
		}
	}
	return existing
}

// debugf prints a startup trace when HELIX_DEBUG=1.
func debugf(format string, args ...interface{}) {
	if strings.TrimSpace(os.Getenv("HELIX_DEBUG")) == "1" {
		fmt.Fprintf(os.Stderr, "[login-env] "+format+"\n", args...)
	}
}

// ResetLoginEnvForTest allows tests to re-run the once-guarded resolution.
func ResetLoginEnvForTest() {
	loginEnvOnce = sync.Once{}
}
