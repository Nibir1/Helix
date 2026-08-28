// Package hooks runs user-defined commands around Helix tool execution.
//
// A hook is the escape hatch for policy Helix cannot know about: "never touch
// the production kubeconfig", "run gofmt after any file write", "log every git
// push to the team channel". The safety pipeline enforces what is universally
// dangerous; hooks enforce what is dangerous HERE.
//
// Security model — hooks are trusted local configuration, and that is a
// deliberate, bounded decision:
//
//   - Hooks come from ~/.helix/hooks.json only. Nothing a model produces, and
//     nothing retrieved from the network, can define or edit one. A planner
//     that wanted to disable a hook would have to write that file, which is
//     itself a shell step subject to the full pipeline.
//   - A hook command runs through the user's shell with the event's details in
//     the environment, never interpolated into the command string: a command
//     containing $HELIX_COMMAND is the user's choice, whereas splicing the
//     model's text into a shell line would make every hook an injection site.
//   - A blocking pre-hook that exits non-zero DENIES the step. This is the
//     only way a hook changes control flow, and it can only ever subtract
//     permission, never add it — a hook cannot approve something the risk
//     tiers rejected, because hooks run after those gates.
package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Event names the moment a hook fires.
type Event string

const (
	// PreShell fires after the safety pipeline approved a shell command and
	// before it runs. A blocking hook here can still refuse it.
	PreShell Event = "pre-shell"

	// PostShell fires after a shell command finished, successfully or not.
	PostShell Event = "post-shell"

	// PreGit / PostGit wrap a planner git action.
	PreGit  Event = "pre-git"
	PostGit Event = "post-git"

	// SessionStart / SessionEnd wrap the interactive shell's lifetime.
	SessionStart Event = "session-start"
	SessionEnd   Event = "session-end"
)

// Events lists every valid event, in the order /hooks prints them.
func Events() []Event {
	return []Event{PreShell, PostShell, PreGit, PostGit, SessionStart, SessionEnd}
}

// ValidEvent reports whether name is a real event, returning the canonical form.
func ValidEvent(name string) (Event, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, e := range Events() {
		if string(e) == name {
			return e, true
		}
	}
	return "", false
}

// Hook is one configured rule.
type Hook struct {
	// Name is a human label used by /hooks remove and in denial messages.
	Name string `json:"name"`

	// Event selects when it fires.
	Event Event `json:"event"`

	// Match is an optional Go regexp against the command (or git action). An
	// empty Match fires on every occurrence of the event.
	Match string `json:"match,omitempty"`

	// Command is the shell command to run.
	Command string `json:"command"`

	// Blocking makes a non-zero exit deny the step. Only meaningful on pre-*
	// events; on post-* events it is ignored, because the action already ran.
	Blocking bool `json:"blocking,omitempty"`

	// TimeoutSec bounds the hook (0 → DefaultTimeout). A hook that hangs must
	// not hang the shell.
	TimeoutSec int `json:"timeout_sec,omitempty"`

	// Disabled keeps a rule on file without running it.
	Disabled bool `json:"disabled,omitempty"`

	re *regexp.Regexp // compiled Match
}

// DefaultTimeout bounds a hook that declares no timeout of its own.
const DefaultTimeout = 30 * time.Second

// Timeout is the hook's effective deadline.
func (h Hook) Timeout() time.Duration {
	if h.TimeoutSec > 0 {
		return time.Duration(h.TimeoutSec) * time.Second
	}
	return DefaultTimeout
}

// Matches reports whether the hook applies to this subject.
func (h Hook) Matches(subject string) bool {
	if h.Disabled {
		return false
	}
	if h.re == nil {
		return true
	}
	return h.re.MatchString(subject)
}

// Set is a loaded, validated hook configuration.
type Set struct {
	Hooks []Hook `json:"hooks"`
	path  string
}

// Result reports one hook's outcome.
type Result struct {
	Hook     Hook
	ExitCode int
	Output   string
	Err      error

	// Denied is true when a blocking pre-hook refused the step.
	Denied bool
}

// ConfigPath returns ~/.helix/hooks.json.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helix", "hooks.json"), nil
}

// Load reads the hook configuration. A missing file is an empty set, not an
// error: hooks are opt-in.
func Load() (*Set, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads hooks from an explicit path (tests).
func LoadFrom(path string) (*Set, error) {
	s := &Set{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("hooks config is not valid JSON (%s): %w", path, err)
	}
	for i := range s.Hooks {
		if err := s.compile(i); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// compile validates one hook and caches its regexp. An invalid rule fails the
// whole load: silently skipping a malformed hook is how a hook the user
// believes is guarding them turns out never to have run.
func (s *Set) compile(i int) error {
	h := &s.Hooks[i]
	if strings.TrimSpace(h.Command) == "" {
		return fmt.Errorf("hook %q has no command", h.Name)
	}
	if _, ok := ValidEvent(string(h.Event)); !ok {
		return fmt.Errorf("hook %q has unknown event %q (valid: %s)",
			h.Name, h.Event, strings.Join(eventNames(), ", "))
	}
	if h.Match != "" {
		re, err := regexp.Compile(h.Match)
		if err != nil {
			return fmt.Errorf("hook %q has an invalid match pattern: %w", h.Name, err)
		}
		h.re = re
	}
	return nil
}

func eventNames() []string {
	out := make([]string, 0, len(Events()))
	for _, e := range Events() {
		out = append(out, string(e))
	}
	return out
}

// Path reports where this set was loaded from.
func (s *Set) Path() string { return s.path }

// For returns the hooks that would fire for an event and subject.
func (s *Set) For(ev Event, subject string) []Hook {
	if s == nil {
		return nil
	}
	var out []Hook
	for _, h := range s.Hooks {
		if h.Event == ev && h.Matches(subject) {
			out = append(out, h)
		}
	}
	return out
}

// Add validates and appends a hook, then persists the set.
func (s *Set) Add(h Hook) error {
	if strings.TrimSpace(h.Name) == "" {
		return fmt.Errorf("hook name is required")
	}
	for _, existing := range s.Hooks {
		if strings.EqualFold(existing.Name, h.Name) {
			return fmt.Errorf("a hook named %q already exists", h.Name)
		}
	}
	s.Hooks = append(s.Hooks, h)
	if err := s.compile(len(s.Hooks) - 1); err != nil {
		s.Hooks = s.Hooks[:len(s.Hooks)-1]
		return err
	}
	return s.Save()
}

// Remove deletes a hook by name (case-insensitive).
func (s *Set) Remove(name string) error {
	for i, h := range s.Hooks {
		if strings.EqualFold(h.Name, name) {
			s.Hooks = append(s.Hooks[:i], s.Hooks[i+1:]...)
			return s.Save()
		}
	}
	return fmt.Errorf("no hook named %q", name)
}

// SetEnabled toggles a hook by name.
func (s *Set) SetEnabled(name string, enabled bool) error {
	for i := range s.Hooks {
		if strings.EqualFold(s.Hooks[i].Name, name) {
			s.Hooks[i].Disabled = !enabled
			return s.Save()
		}
	}
	return fmt.Errorf("no hook named %q", name)
}

// Save writes the set back to disk at 0600 — a hook command is executable
// local policy and should not be world-readable.
func (s *Set) Save() error {
	if s.path == "" {
		path, err := ConfigPath()
		if err != nil {
			return err
		}
		s.path = path
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Context carries the details a hook is told about, exported as environment
// variables so nothing is spliced into the command string.
type Context struct {
	Tool     string // "shell" | "git" | ...
	Action   string // planner action, when the tool has one
	Command  string // the command (or git action) in question
	Dir      string // working directory
	ExitCode int    // post-* only
	Err      string // post-* only
}

// Env renders the context as HELIX_* environment entries.
func (c Context) Env(ev Event) []string {
	return []string{
		"HELIX_HOOK_EVENT=" + string(ev),
		"HELIX_TOOL=" + c.Tool,
		"HELIX_ACTION=" + c.Action,
		"HELIX_COMMAND=" + c.Command,
		"HELIX_CWD=" + c.Dir,
		fmt.Sprintf("HELIX_EXIT_CODE=%d", c.ExitCode),
		"HELIX_ERROR=" + c.Err,
	}
}

// Run fires every hook registered for the event and returns one result each.
//
// A blocking pre-hook that exits non-zero sets Denied; the caller is
// responsible for refusing the step. Run does not stop at the first denial —
// the user should see every objection, not just the first.
func (s *Set) Run(ctx context.Context, ev Event, c Context) []Result {
	matched := s.For(ev, hookSubject(c))
	if len(matched) == 0 {
		return nil
	}
	results := make([]Result, 0, len(matched))
	for _, h := range matched {
		results = append(results, runHook(ctx, h, ev, c))
	}
	return results
}

// hookSubject is what Match is tested against: the command for shell hooks,
// the action for tools whose step carries no command line.
func hookSubject(c Context) string {
	if c.Command != "" {
		return c.Command
	}
	return c.Action
}

func runHook(ctx context.Context, h Hook, ev Event, c Context) Result {
	res := Result{Hook: h}

	runCtx, cancel := context.WithTimeout(ctx, h.Timeout())
	defer cancel()

	shellPath, flag := hookShell()
	cmd := exec.CommandContext(runCtx, shellPath, flag, h.Command)
	cmd.Env = append(os.Environ(), c.Env(ev)...)
	if c.Dir != "" {
		cmd.Dir = c.Dir
	}
	// Combined output: a hook's stderr is usually the reason it refused, and a
	// denial the user cannot read is a denial they cannot fix.
	out, err := cmd.CombinedOutput()
	res.Output = strings.TrimSpace(string(out))
	res.Err = err
	if ee, ok := err.(*exec.ExitError); ok {
		res.ExitCode = ee.ExitCode()
	} else if err != nil {
		res.ExitCode = -1
	}

	// Fail closed on a blocking pre-hook: a hook that could not run has not
	// approved anything. A timeout, a missing interpreter, and an explicit
	// non-zero exit are all "not approved".
	if err != nil && h.Blocking && isPreEvent(ev) {
		res.Denied = true
	}
	return res
}

func isPreEvent(ev Event) bool {
	return ev == PreShell || ev == PreGit
}

// hookShell picks the interpreter. Hooks are shell one-liners by design, so
// this follows the platform default rather than the user's login shell: a hook
// that works for one user should work for the next.
func hookShell() (string, string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", "/C"
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh, "-c"
	}
	return "/bin/sh", "-c"
}

// Denied reports whether any result refused the step, and why.
func Denied(results []Result) (bool, string) {
	var reasons []string
	for _, r := range results {
		if !r.Denied {
			continue
		}
		reason := fmt.Sprintf("hook %q denied the step (exit %d)", r.Hook.Name, r.ExitCode)
		if r.Output != "" {
			reason += ": " + firstLine(r.Output)
		}
		reasons = append(reasons, reason)
	}
	if len(reasons) == 0 {
		return false, ""
	}
	return true, strings.Join(reasons, "; ")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
