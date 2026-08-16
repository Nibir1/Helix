// internal/shell/loginenv_test.go
// Tests for login-environment resolution: parsing, PATH union, overlay
// semantics, and a real /bin/sh integration probe on Unix.
package shell

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestParseLoginEnvOutputIgnoresRcChatter(t *testing.T) {
	out := "welcome to my zshrc\nversion=1.2 junk line below\n" +
		loginEnvSentinel + "\nFOO=bar\nEMPTY=\nWITH_EQ=a=b=c\nlower_case=x1\n" +
		"1BAD=nope\nBAD KEY=nope\nBAD-KEY=nope\nMOO=hello\r\n"
	env := parseLoginEnvOutput(out)

	want := map[string]string{
		"FOO":        "bar",
		"EMPTY":      "",
		"WITH_EQ":    "a=b=c",
		"lower_case": "x1",
		"MOO":        "hello",
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("parsed env = %#v, want %#v", env, want)
	}
}

func TestParseLoginEnvOutputUsesLastSentinel(t *testing.T) {
	// An rc file echoing sentinel-like text must not be trusted; the real
	// dump after the LAST sentinel wins.
	out := "noise " + loginEnvSentinel + " trailing\nFOO=wrong\n" +
		loginEnvSentinel + "\nFOO=right\n"
	env := parseLoginEnvOutput(out)
	if env["FOO"] != "right" {
		t.Fatalf("FOO = %q, want %q", env["FOO"], "right")
	}
}

func TestParseLoginEnvOutputWithoutSentinelStillParses(t *testing.T) {
	env := parseLoginEnvOutput("A=1\nB=2\n")
	if len(env) != 2 || env["A"] != "1" || env["B"] != "2" {
		t.Fatalf("env = %#v", env)
	}
}

func TestIsValidEnvKey(t *testing.T) {
	valid := []string{"a", "Z_9", "_x", "HOME"}
	invalid := []string{"", "9a", "a-b", "a b", "=x"}
	for _, k := range valid {
		if !isValidEnvKey(k) {
			t.Errorf("key %q should be valid", k)
		}
	}
	for _, k := range invalid {
		if isValidEnvKey(k) {
			t.Errorf("key %q should be invalid", k)
		}
	}
}

// pathList joins entries with the platform's PATH separator so fixtures are
// portable across the Unix and Windows CI matrices (Windows splits on ';',
// where a literal "/a:/b" stays a single entry).
func pathList(entries ...string) string {
	return strings.Join(entries, string(os.PathListSeparator))
}

func TestMergePathEntriesUnionsInOrder(t *testing.T) {
	primary := pathList("/nvm/bin", "/usr/bin")
	secondary := pathList("/usr/bin", "/secret/bin")
	merged := mergePathEntries(primary, secondary)

	entries := filepath.SplitList(merged)
	var nvmIdx, usrIdx, secretIdx = -1, -1, -1
	for i, e := range entries {
		switch e {
		case "/nvm/bin":
			nvmIdx = i
		case "/usr/bin":
			usrIdx = i
		case "/secret/bin":
			secretIdx = i
		}
	}
	if nvmIdx != 0 {
		t.Fatalf("login PATH entry must come first, got entries %v", entries)
	}
	if usrIdx == -1 || secretIdx == -1 {
		t.Fatalf("inherited entries must survive, got entries %v", entries)
	}
	seen := map[string]int{}
	for _, e := range entries {
		seen[e]++
		if seen[e] > 1 {
			t.Fatalf("duplicate entry %q in %v", e, entries)
		}
	}
}

func TestMergePathEntriesEmptyInputs(t *testing.T) {
	merged := mergePathEntries("", "")
	if merged != "" {
		// A machine with zero well-known dirs is unrealistic, but the
		// function must not panic or invent entries that do not exist.
		for _, e := range filepath.SplitList(merged) {
			if fi, err := os.Stat(e); err != nil || !fi.IsDir() {
				t.Fatalf("invented non-existing entry %q", e)
			}
		}
	}
}

func TestApplyLoginEnvToCurrentOverlaysAndProtects(t *testing.T) {
	t.Setenv("PATH", "/orig/bin")
	t.Setenv("PWD", "/orig/pwd")
	t.Setenv("HELIX_TOKEN", "runtime")
	t.Setenv("SHLVL", "1")

	login := map[string]string{
		"PATH":        pathList("/login/bin", "/orig/bin"),
		"PWD":         "/login/should/not/apply",
		"SHLVL":       "42",
		"HOME":        "/login/home/should/not/apply",
		"HELIX_TOKEN": "should-not-override-runtime",
		"NVM_DIR":     "/users/nvm",
	}
	applyLoginEnvToCurrent(login)

	if got := os.Getenv("NVM_DIR"); got != "/users/nvm" {
		t.Fatalf("NVM_DIR = %q, want overlay applied", got)
	}
	if got := os.Getenv("PWD"); got != "/orig/pwd" {
		t.Fatalf("PWD = %q, want process-local value kept", got)
	}
	if got := os.Getenv("SHLVL"); got != "1" {
		t.Fatalf("SHLVL = %q, want process-local value kept", got)
	}
	if got := os.Getenv("HOME"); got == "/login/home/should/not/apply" {
		t.Fatalf("HOME must not be overlaid from the login snapshot")
	}
	if got := os.Getenv("HELIX_TOKEN"); got != "runtime" {
		t.Fatalf("HELIX_* runtime config must win, got %q", got)
	}

	path := os.Getenv("PATH")
	entries := filepath.SplitList(path)
	if len(entries) == 0 || entries[0] != "/login/bin" {
		t.Fatalf("PATH = %q, login entries must lead", path)
	}
	if !containsEntry(entries, "/orig/bin") {
		t.Fatalf("PATH = %q, inherited entries must survive", path)
	}
}

func TestApplyLoginEnvToCurrentNilStillMergesWellKnownDirs(t *testing.T) {
	t.Setenv("PATH", "/only/bin")
	applyLoginEnvToCurrent(nil)

	entries := filepath.SplitList(os.Getenv("PATH"))
	if !containsEntry(entries, "/only/bin") {
		t.Fatalf("inherited PATH lost: %v", entries)
	}
	// At least one well-known directory exists on every supported Unix
	// builder (/usr/local/bin, /usr/bin variants, or a home-local bin).
	if len(entries) < 2 {
		t.Fatalf("expected well-known dirs to be merged, got %v", entries)
	}
}

func TestResolveLoginEnvRealShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only integration probe")
	}
	for _, shellPath := range []string{"/bin/sh", "/bin/bash", "/bin/zsh"} {
		if _, err := os.Stat(shellPath); err != nil {
			continue
		}
		env := resolveLoginEnv(shellPath)
		if len(env) == 0 {
			t.Fatalf("resolveLoginEnv(%s) returned empty env", shellPath)
		}
		if env["SHELL"] != shellPath {
			t.Errorf("%s: SHELL = %q, want %q", shellPath, env["SHELL"], shellPath)
		}
		if _, ok := env["PATH"]; !ok {
			t.Errorf("%s: PATH missing from snapshot", shellPath)
		}
		return
	}
	t.Skip("no standard Unix shell found")
}

func TestApplyLoginEnvironmentIsIdempotent(t *testing.T) {
	t.Setenv("PATH", "/probe/bin")
	first := os.Getenv("PATH")
	ResetLoginEnvForTest()
	ApplyLoginEnvironment()
	ApplyLoginEnvironment()
	after := os.Getenv("PATH")

	if !containsEntry(filepath.SplitList(after), "/probe/bin") {
		t.Fatalf("inherited PATH entry lost after apply: %q", after)
	}
	if strings.Count(after, "/probe/bin") > 1 && first != after {
		t.Fatalf("PATH polluted with duplicates: %q", after)
	}
}

func containsEntry(entries []string, want string) bool {
	for _, e := range entries {
		if e == want {
			return true
		}
	}
	return false
}
