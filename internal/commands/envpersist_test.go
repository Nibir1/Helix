// internal/commands/envpersist_test.go
// Purpose: the parser's boundaries and the persistence itself.
//
// The boundaries matter more than the happy path. Anything this recognises
// skips the ordinary execution path, so a parser that is too eager turns a
// compound command into a silent variable assignment — the opposite of the
// trap it was written to close.
package commands

import (
	"os"
	"strings"
	"testing"

	"helix/internal/shell"
)

func posixEnv() shell.Env { return shell.Env{Shell: "sh"} }

// The reported case, end to end: the value has quotes and a $VAR in it, and it
// has to survive into this process so the NEXT command sees it.
func TestExportPersistsIntoThisProcess(t *testing.T) {
	t.Setenv("HELIX_ENV_TEST_BASE", "/base")
	t.Setenv("HELIX_ENV_TEST_PATH", "/original")

	assigns, ok := parseEnvCommand(`export HELIX_ENV_TEST_PATH="$HELIX_ENV_TEST_BASE/go/bin:$HELIX_ENV_TEST_PATH"`)
	if !ok {
		t.Fatal("a plain export was not recognised")
	}
	if _, err := applyEnvCommand(
		`export HELIX_ENV_TEST_PATH="$HELIX_ENV_TEST_BASE/go/bin:$HELIX_ENV_TEST_PATH"`,
		assigns, posixEnv()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got := os.Getenv("HELIX_ENV_TEST_PATH")
	if got != "/base/go/bin:/original" {
		t.Errorf("HELIX_ENV_TEST_PATH = %q, want the expanded, unquoted value", got)
	}
}

// Values are evaluated by a real shell, so tilde and command substitution work
// without this package owning a parser for them.
func TestExportUsesRealShellExpansion(t *testing.T) {
	t.Setenv("HELIX_ENV_TEST_SUB", "")
	cmd := `export HELIX_ENV_TEST_SUB=$(printf 'a b')`
	assigns, ok := parseEnvCommand(cmd)
	if !ok {
		t.Fatal("not recognised")
	}
	if _, err := applyEnvCommand(cmd, assigns, posixEnv()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := os.Getenv("HELIX_ENV_TEST_SUB"); got != "a b" {
		t.Errorf("got %q, want the substituted value with its space intact", got)
	}
}

func TestUnsetRemovesTheVariable(t *testing.T) {
	t.Setenv("HELIX_ENV_TEST_GONE", "here")
	assigns, ok := parseEnvCommand("unset HELIX_ENV_TEST_GONE")
	if !ok {
		t.Fatal("unset was not recognised")
	}
	if _, err := applyEnvCommand("unset HELIX_ENV_TEST_GONE", assigns, posixEnv()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, present := os.LookupEnv("HELIX_ENV_TEST_GONE"); present {
		t.Error("the variable survived unset")
	}
}

func TestMultipleAssignmentsInOneExport(t *testing.T) {
	cmd := "export HELIX_ENV_TEST_A=1 HELIX_ENV_TEST_B=2"
	assigns, ok := parseEnvCommand(cmd)
	if !ok || len(assigns) != 2 {
		t.Fatalf("parsed %d assignments, ok=%v", len(assigns), ok)
	}
	t.Setenv("HELIX_ENV_TEST_A", "")
	t.Setenv("HELIX_ENV_TEST_B", "")
	if _, err := applyEnvCommand(cmd, assigns, posixEnv()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if os.Getenv("HELIX_ENV_TEST_A") != "1" || os.Getenv("HELIX_ENV_TEST_B") != "2" {
		t.Errorf("A=%q B=%q", os.Getenv("HELIX_ENV_TEST_A"), os.Getenv("HELIX_ENV_TEST_B"))
	}
}

// THE important set. Everything here must fall through to ordinary execution,
// because recognising it would mean running a DIFFERENT command than the one
// that was typed — and for the compound cases it would mean silently dropping
// the part after the separator.
func TestParserRefusesAnythingThatIsNotOnlyAnAssignment(t *testing.T) {
	for _, cmd := range []string{
		// A prefix assignment is a one-shot override in every shell. Persisting
		// it would surprise in the opposite direction.
		`PATH="$HOME/go/bin:$PATH" make dev`,
		`FOO=bar ./script.sh`,
		// Compound: the tail must not be discarded.
		`export FOO=1; rm -rf ~`,
		`export FOO=1 && make dev`,
		`export FOO=1 || true`,
		`export FOO=1 | tee log`,
		`export FOO=1 &`,
		"export FOO=1\nrm -rf ~",
		// Lists the environment, which here contains API keys. No persistence
		// semantics, so it keeps going to the child exactly as before.
		`export`,
		`unset`,
		// Flags change the meaning entirely.
		`export -n FOO`,
		`unset -f myfunc`,
		// Re-exporting a shell variable this process cannot see.
		`export FOO`,
		// Not an assignment at all.
		`make dev`,
		`echo export FOO=1`,
		// Invalid names must not become environment variables.
		`export 9FOO=1`,
		`export FOO-BAR=1`,
		`export =1`,
		`unset FOO=1`,
	} {
		if assigns, ok := parseEnvCommand(cmd); ok {
			t.Errorf("%q was intercepted as an assignment (%v); it must run normally",
				cmd, assigns)
		}
	}
}

func TestParserAcceptsTheOrdinaryForms(t *testing.T) {
	for _, cmd := range []string{
		`export FOO=bar`,
		`export FOO=`,
		`  export   FOO=bar  `,
		`export PATH="$HOME/go/bin:$PATH"`,
		`unset FOO`,
		`unset FOO BAR`,
		`export _PRIVATE=1`,
		`export F00=1`,
	} {
		if _, ok := parseEnvCommand(cmd); !ok {
			t.Errorf("%q should have been recognised", cmd)
		}
	}
}

// ADR-005: a transcript carries user authority with no proof of who spoke, and
// an exported PATH silently redirects every command that follows. So a spoken
// turn may run it but never persist it.
func TestSpokenTurnsCannotPersistEnvironment(t *testing.T) {
	prev := spokenTurn
	t.Cleanup(func() { spokenTurn = prev })

	SetSpokenTurnFunc(func() bool { return true })
	if !turnIsSpoken() {
		t.Fatal("the provenance hook was not installed")
	}

	t.Setenv("HELIX_ENV_TEST_VOICE", "untouched")
	assigns, ok := parseEnvCommand("export HELIX_ENV_TEST_VOICE=changed")
	if !ok {
		t.Fatal("not recognised")
	}
	if err := applyEnvCommandForTurn("export HELIX_ENV_TEST_VOICE=changed",
		assigns, ExecuteConfig{AutoConfirm: true}, posixEnv()); err != nil {
		t.Fatalf("the spoken path errored instead of degrading: %v", err)
	}
	if got := os.Getenv("HELIX_ENV_TEST_VOICE"); got != "untouched" {
		t.Errorf("a spoken export persisted: %q", got)
	}
}

// With no hook installed — the daemon, and every test — nothing is spoken.
func TestProvenanceDefaultsToTyped(t *testing.T) {
	prev := spokenTurn
	t.Cleanup(func() { spokenTurn = prev })
	spokenTurn = nil
	if turnIsSpoken() {
		t.Error("with no hook installed a turn must not read as spoken")
	}
}

// A dry run must not change the session either.
func TestDryRunDoesNotPersist(t *testing.T) {
	t.Setenv("HELIX_ENV_TEST_DRY", "before")
	assigns, _ := parseEnvCommand("export HELIX_ENV_TEST_DRY=after")
	if err := applyEnvCommandForTurn("export HELIX_ENV_TEST_DRY=after",
		assigns, ExecuteConfig{DryRun: true}, posixEnv()); err != nil {
		t.Fatalf("dry run errored: %v", err)
	}
	if got := os.Getenv("HELIX_ENV_TEST_DRY"); got != "before" {
		t.Errorf("a dry run changed the environment: %q", got)
	}
}

// The confirmation line is also what a transcript log keeps, so a value that
// looks like a credential must not be repeated into it.
func TestSecretValuesAreNotEchoed(t *testing.T) {
	secret := "sk-abcdefghijklmnopqrstuvwxyz"
	for _, name := range []string{"OPENAI_API_KEY", "GH_TOKEN", "MY_SECRET", "DB_PASSWORD"} {
		if got := redactEnvValue(name, secret); strings.Contains(got, "abcdef") {
			t.Errorf("%s echoed its value: %q", name, got)
		}
	}
	// An ordinary variable is shown, because seeing what PATH became is the
	// entire point of the line.
	if got := redactEnvValue("PATH", "/usr/bin"); got != "/usr/bin" {
		t.Errorf("PATH was redacted: %q", got)
	}
	// And a very long value is bounded rather than wrapping the panel.
	long := strings.Repeat("x", 500)
	if got := redactEnvValue("PATH", long); len(got) > 80 {
		t.Errorf("a long value was not bounded: %d chars", len(got))
	}
}

// Windows shells do not have `export`, and the evaluation invokes the shell
// with -c, which they do not take.
func TestWindowsShellsAreNotIntercepted(t *testing.T) {
	for _, sh := range []string{"powershell", "cmd"} {
		if posixShell(shell.Env{Shell: sh}) {
			t.Errorf("%s was treated as POSIX", sh)
		}
	}
	for _, sh := range []string{"zsh", "bash", "sh", "fish"} {
		if !posixShell(shell.Env{Shell: sh}) {
			t.Errorf("%s was not treated as POSIX", sh)
		}
	}
}

// The splitter finds WORD BOUNDARIES only — the shell still evaluates values.
// Its job is to stop a space inside a quoted value or a substitution from
// making the line unrecognisable, which is how `export PATH="/my dir:$PATH"`
// silently failed to persist.
func TestSplitShellWordsRespectsQuotingAndSubstitution(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{`export FOO=bar`, []string{"export", "FOO=bar"}},
		{`export PATH="/my dir:$PATH"`, []string{"export", `PATH="/my dir:$PATH"`}},
		{`export FOO='a b'`, []string{"export", `FOO='a b'`}},
		{`export FOO=$(printf 'a b')`, []string{"export", `FOO=$(printf 'a b')`}},
		{`export FOO=${BAR:-a b}`, []string{"export", `FOO=${BAR:-a b}`}},
		{"export FOO=`printf 'a b'`", []string{"export", "FOO=`printf 'a b'`"}},
		{`export A=1   B=2`, []string{"export", "A=1", "B=2"}},
		{`export FOO=a\ b`, []string{"export", `FOO=a\ b`}},
	} {
		got, ok := splitShellWords(tc.line)
		if !ok {
			t.Errorf("%q reported unbalanced", tc.line)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%q -> %q, want %q", tc.line, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q word %d = %q, want %q", tc.line, i, got[i], tc.want[i])
			}
		}
	}

	// Anything left open is not a line to interpret; falling through to
	// ordinary execution is the safe outcome.
	for _, line := range []string{
		`export FOO="unterminated`,
		`export FOO='unterminated`,
		`export FOO=$(unterminated`,
		"export FOO=`unterminated",
	} {
		if _, ok := splitShellWords(line); ok {
			t.Errorf("%q was reported balanced", line)
		}
	}
}

// A quoted value with spaces has to survive all the way into this process —
// the case that exposed the splitter bug.
func TestQuotedValueWithSpacesPersists(t *testing.T) {
	t.Setenv("HELIX_ENV_TEST_SPACES", "")
	cmd := `export HELIX_ENV_TEST_SPACES="/my dir/bin:/other"`
	assigns, ok := parseEnvCommand(cmd)
	if !ok {
		t.Fatal("a quoted value with a space was not recognised")
	}
	if _, err := applyEnvCommand(cmd, assigns, posixEnv()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := os.Getenv("HELIX_ENV_TEST_SPACES"); got != "/my dir/bin:/other" {
		t.Errorf("got %q, want the space preserved and the quotes removed", got)
	}
}
