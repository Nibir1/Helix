package hooks

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFileIsEmptySet(t *testing.T) {
	set, err := LoadFrom(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("a missing hook config must not be an error: %v", err)
	}
	if len(set.Hooks) != 0 {
		t.Fatalf("expected no hooks, got %d", len(set.Hooks))
	}
}

// TestLoadRejectsInvalidRules is the important one: a malformed hook must fail
// the load loudly rather than being skipped, or a user believes a rule is
// guarding them when it never runs.
func TestLoadRejectsInvalidRules(t *testing.T) {
	cases := map[string]string{
		"unknown event":  `{"hooks":[{"name":"a","event":"pre-nothing","command":"true"}]}`,
		"no command":     `{"hooks":[{"name":"a","event":"pre-shell","command":"  "}]}`,
		"bad regexp":     `{"hooks":[{"name":"a","event":"pre-shell","command":"true","match":"([unclosed"}]}`,
		"malformed json": `{"hooks":[`,
	}
	for name, body := range cases {
		if _, err := LoadFrom(writeConfig(t, body)); err == nil {
			t.Errorf("%s: expected a load error, got none", name)
		}
	}
}

func TestForMatchesEventAndPattern(t *testing.T) {
	path := writeConfig(t, `{"hooks":[
		{"name":"all-shell","event":"pre-shell","command":"true"},
		{"name":"only-rm","event":"pre-shell","command":"true","match":"^rm "},
		{"name":"disabled","event":"pre-shell","command":"true","disabled":true},
		{"name":"git","event":"pre-git","command":"true"}
	]}`)
	set, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}

	got := names(set.For(PreShell, "rm -rf build"))
	want := []string{"all-shell", "only-rm"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("For(pre-shell, rm) = %v, want %v", got, want)
	}

	got = names(set.For(PreShell, "ls -la"))
	if strings.Join(got, ",") != "all-shell" {
		t.Errorf("For(pre-shell, ls) = %v, want [all-shell]", got)
	}

	if got := names(set.For(PostShell, "anything")); len(got) != 0 {
		t.Errorf("post-shell should have no hooks, got %v", got)
	}
}

func names(hs []Hook) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Name)
	}
	return out
}

func TestBlockingPreHookDenies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell exit")
	}
	path := writeConfig(t, `{"hooks":[
		{"name":"refuse","event":"pre-shell","command":"echo nope >&2; exit 3","blocking":true}
	]}`)
	set, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}

	results := set.Run(context.Background(), PreShell, Context{Tool: "shell", Command: "rm -rf /"})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", results[0].ExitCode)
	}
	denied, reason := Denied(results)
	if !denied {
		t.Fatal("a blocking pre-hook exiting non-zero must deny the step")
	}
	// The hook's own message is the only explanation the user gets.
	if !strings.Contains(reason, "nope") {
		t.Errorf("denial reason %q should carry the hook's output", reason)
	}
}

// TestNonBlockingHookDoesNotDeny: a reporting hook that fails must not stop work.
func TestNonBlockingHookDoesNotDeny(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell exit")
	}
	set, err := LoadFrom(writeConfig(t,
		`{"hooks":[{"name":"noisy","event":"pre-shell","command":"exit 1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if denied, _ := Denied(set.Run(context.Background(), PreShell, Context{Command: "ls"})); denied {
		t.Error("a non-blocking hook must never deny a step")
	}
}

// TestPostHookNeverDenies: the action already happened, so there is nothing left
// to refuse — treating a post-hook failure as a denial would misreport it.
func TestPostHookNeverDenies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell exit")
	}
	set, err := LoadFrom(writeConfig(t,
		`{"hooks":[{"name":"after","event":"post-shell","command":"exit 9","blocking":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if denied, _ := Denied(set.Run(context.Background(), PostShell, Context{Command: "ls"})); denied {
		t.Error("a post-* hook must not deny; the action already ran")
	}
}

// TestHookFailsClosedOnTimeout: a hook that could not finish has approved nothing.
func TestHookFailsClosedOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX sleep")
	}
	set, err := LoadFrom(writeConfig(t,
		`{"hooks":[{"name":"slow","event":"pre-shell","command":"sleep 5","blocking":true,"timeout_sec":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	results := set.Run(context.Background(), PreShell, Context{Command: "ls"})
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("the hook timeout was not enforced (took %v)", elapsed)
	}
	if denied, _ := Denied(results); !denied {
		t.Error("a blocking hook that timed out must deny: it approved nothing")
	}
}

// TestHookReceivesContextInEnvironment pins the contract that hook commands read
// the step's details from the environment rather than from an interpolated
// command string — which is what keeps a hook from being an injection site.
func TestHookReceivesContextInEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell variable expansion")
	}
	set, err := LoadFrom(writeConfig(t,
		`{"hooks":[{"name":"echo-env","event":"pre-shell","command":"printf '%s|%s|%s' \"$HELIX_HOOK_EVENT\" \"$HELIX_TOOL\" \"$HELIX_COMMAND\""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	results := set.Run(context.Background(), PreShell, Context{
		Tool: "shell", Command: "rm -rf build",
	})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	want := "pre-shell|shell|rm -rf build"
	if results[0].Output != want {
		t.Errorf("hook saw %q, want %q", results[0].Output, want)
	}
}

// TestCommandIsNotInterpolated is the security property stated in the package
// doc: a command containing shell metacharacters reaches the hook as data.
func TestCommandIsNotInterpolated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	set, err := LoadFrom(writeConfig(t,
		`{"hooks":[{"name":"safe","event":"pre-shell","command":"printf '%s' \"$HELIX_COMMAND\""}]}`))
	if err != nil {
		t.Fatal(err)
	}
	// If the command were spliced into the hook's shell line, the injected
	// `touch` would run and the marker file would exist.
	marker := filepath.Join(t.TempDir(), "pwned")
	hostile := `ls"; touch ` + marker + `; echo "`
	results := set.Run(context.Background(), PreShell, Context{Tool: "shell", Command: hostile})

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the step's command was interpolated into the hook shell line")
	}
	if len(results) != 1 || results[0].Output != hostile {
		t.Errorf("hook output = %q, want the command verbatim", results[0].Output)
	}
}

func TestAddRemoveEnableRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	set, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := set.Add(Hook{Name: "fmt", Event: PostShell, Command: "gofmt -l ."}); err != nil {
		t.Fatal(err)
	}
	if err := set.Add(Hook{Name: "FMT", Event: PostShell, Command: "true"}); err == nil {
		t.Error("duplicate names (case-insensitive) must be rejected")
	}
	if err := set.Add(Hook{Name: "", Event: PostShell, Command: "true"}); err == nil {
		t.Error("an unnamed hook must be rejected: /hooks rm needs a handle")
	}
	if err := set.Add(Hook{Name: "bad", Event: PostShell, Command: "true", Match: "([w"}); err == nil {
		t.Error("an invalid pattern must be rejected on add, not at fire time")
	}
	if len(set.Hooks) != 1 {
		t.Fatalf("a rejected add must not leave a partial entry: %d hooks", len(set.Hooks))
	}

	reloaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Hooks) != 1 || reloaded.Hooks[0].Name != "fmt" {
		t.Fatalf("round trip lost the hook: %+v", reloaded.Hooks)
	}

	if err := set.SetEnabled("fmt", false); err != nil {
		t.Fatal(err)
	}
	if got := set.For(PostShell, "anything"); len(got) != 0 {
		t.Error("a disabled hook must not match")
	}
	if err := set.Remove("nope"); err == nil {
		t.Error("removing an absent hook must report it")
	}
	if err := set.Remove("FMT"); err != nil {
		t.Fatalf("remove should be case-insensitive: %v", err)
	}
	if len(set.Hooks) != 0 {
		t.Fatalf("expected an empty set, got %d", len(set.Hooks))
	}
}

func TestConfigIsNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	path := filepath.Join(t.TempDir(), "hooks.json")
	set, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Add(Hook{Name: "x", Event: PreShell, Command: "true"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Hook commands are executable local policy; another user must not be able
	// to read (or learn how to sidestep) them.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("hooks.json mode = %o, want 600", perm)
	}
}

func TestValidEventAndTimeoutDefaults(t *testing.T) {
	if _, ok := ValidEvent("  PRE-SHELL "); !ok {
		t.Error("ValidEvent should normalize case and whitespace")
	}
	if _, ok := ValidEvent("pre-everything"); ok {
		t.Error("ValidEvent accepted an unknown event")
	}
	if got := (Hook{}).Timeout(); got != DefaultTimeout {
		t.Errorf("default timeout = %v, want %v", got, DefaultTimeout)
	}
	if got := (Hook{TimeoutSec: 5}).Timeout(); got != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", got)
	}
}

func TestRunWithNoMatchingHooksIsCheap(t *testing.T) {
	var nilSet *Set
	if got := nilSet.For(PreShell, "ls"); got != nil {
		t.Error("a nil set must be safe to query")
	}
	set := &Set{}
	if got := set.Run(context.Background(), PreShell, Context{Command: "ls"}); got != nil {
		t.Error("an empty set must return no results")
	}
}
