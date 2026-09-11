// internal/commands/envpersist.go
// Purpose: make `export` and `unset` outlive the command that ran them.
//
// THE TRAP THIS CLOSES. Every command Helix runs is a FRESH CHILD PROCESS
// (`exec.Command(shell, "-c", command)` in execute.go), so `export PATH=...`
// set a variable inside a process that then exited. Meanwhile `cd` DOES
// persist — it is a slash command that calls os.Chdir on the Helix process
// itself. So the shell behaved like a session for directories and like a
// series of unrelated subshells for the environment, with nothing on screen to
// say which. A real session reported this the obvious way: they ran
//
//	export PATH="/Users/nahasat/go/bin:$PATH"
//	make dev
//
// three times, and `make dev` could not have seen it on any of them.
//
// WHY THE VALUE IS EVALUATED BY THE REAL SHELL. `export PATH="$HOME/go/bin:$PATH"`
// needs quote removal, `$VAR` expansion, `~`, and `$(...)`. Writing a parser
// for that is how you end up with a shell that is subtly not a shell. So only
// the NAMES are parsed here — they sit before an `=` and are unambiguous — and
// the value is read back out of a shell that was asked to perform the
// assignment for real. The shell is the authority on its own syntax.
//
// WHY IT IS TYPED-ONLY. An exported variable is silent and persists for the
// rest of the session, which makes `export PATH=/tmp/x:$PATH` a way to
// redirect every command that follows without appearing in any of them.
// ADR-005's rule is that a transcript carries user authority with no proof of
// who spoke, so this joins /permissions and `/blackbox wake on` as something a
// spoken turn may not do. A spoken export still RUNS — it just does not
// persist, which is exactly today's behaviour — and it says so, because
// silently doing nothing is the trap this file exists to remove.
package commands

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"helix/internal/shell"

	"github.com/fatih/color"
)

// spokenTurn reports whether the line being executed arrived by voice.
//
// Injected rather than imported: the REPL knows the provenance and this
// package cannot see cmd/helix. Nil means "not a voice shell" — the daemon and
// every test — which is the safe default only because it is also the true one
// for those callers.
var spokenTurn func() bool

// SetSpokenTurnFunc installs the provenance predicate. Called once at startup.
func SetSpokenTurnFunc(fn func() bool) { spokenTurn = fn }

// turnIsSpoken reports whether this command came from a transcript.
func turnIsSpoken() bool { return spokenTurn != nil && spokenTurn() }

// envAssignment is one variable a command wants to change.
type envAssignment struct {
	Name  string
	Unset bool
}

// parseEnvCommand recognises a command that ONLY sets or unsets variables.
//
// Deliberately narrow, and each restriction is load-bearing:
//
//   - The line must be exactly an `export` or `unset` statement. A PREFIX
//     assignment (`PATH=... make dev`) is a one-shot override in every shell
//     and must keep behaving that way; persisting it would be a surprise in
//     the opposite direction.
//   - No `;`, `&&`, `||`, `|`, `&` or newline. `export A=1; rm -rf ~` is not an
//     export statement, and anything this does not recognise falls through to
//     the ordinary path where the safety checks live. Refusing to parse is
//     always the safe outcome here.
//   - A bare `export` with no arguments is NOT recognised. It lists the
//     environment, which on this machine includes OPENAI_API_KEY and friends;
//     it has no persistence semantics, so it goes to the child shell exactly
//     as before rather than gaining a new way to print secrets.
//
// Returns the assignments and whether the whole line was one of these.
func parseEnvCommand(command string) ([]envAssignment, bool) {
	line := strings.TrimSpace(command)
	if strings.ContainsAny(line, ";|&\n\r") {
		return nil, false
	}

	fields, balanced := splitShellWords(line)
	if !balanced || len(fields) < 2 {
		return nil, false // bare `export` / bare `unset`, or unparseable
	}

	unset := false
	switch fields[0] {
	case "export":
	case "unset":
		unset = true
	default:
		return nil, false
	}

	out := make([]envAssignment, 0, len(fields)-1)
	for _, arg := range fields[1:] {
		if strings.HasPrefix(arg, "-") {
			return nil, false // flags (unset -f, export -n) change the meaning
		}
		name := arg
		if i := strings.IndexByte(arg, '='); i >= 0 {
			if unset {
				return nil, false // `unset A=1` is not valid
			}
			name = arg[:i]
		} else if !unset {
			// `export NAME` with no value re-exports a SHELL variable that
			// this process cannot see, so there is nothing to carry over.
			return nil, false
		}
		if !validEnvName(name) {
			return nil, false
		}
		out = append(out, envAssignment{Name: name, Unset: unset})
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// splitShellWords splits a line on TOP-LEVEL whitespace only.
//
// strings.Fields cannot be used here and the difference is not cosmetic: it
// splits inside quotes, so `export PATH="/my dir:$PATH"` came apart into three
// fields and the whole line was silently not recognised — falling through to a
// child process, which is the exact bug this file exists to fix, hidden behind
// a space.
//
// This finds WORD BOUNDARIES and nothing else. It does not remove quotes,
// expand anything, or interpret `$(...)` — the real shell still does all of
// that when the value is evaluated. Quote tracking is only here so a space
// inside a quoted value, or inside a command substitution that contains one,
// does not end a word.
//
// Returns the words and whether the quoting was balanced; unbalanced quoting
// means this is not something to interpret.
func splitShellWords(line string) ([]string, bool) {
	var (
		words []string
		cur   strings.Builder
		quote byte // 0, '\'' or '"'
		depth int  // inside $( ) or ${ }
		tick  bool // inside `backticks`
		esc   bool
		open  bool
	)
	flush := func() {
		if open {
			words = append(words, cur.String())
			cur.Reset()
			open = false
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case esc:
			cur.WriteByte(c)
			open, esc = true, false
		case c == '\\' && quote != '\'':
			// A backslash escapes the next byte everywhere but inside single
			// quotes, where it is literal.
			cur.WriteByte(c)
			open, esc = true, true
		case quote != 0:
			cur.WriteByte(c)
			open = true
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			cur.WriteByte(c)
			open, quote = true, c
		case c == '`':
			cur.WriteByte(c)
			open, tick = true, !tick
		case c == '$' && i+1 < len(line) && (line[i+1] == '(' || line[i+1] == '{'):
			// A substitution is ONE word however many spaces it contains:
			// `export X=$(printf 'a b')` is a single assignment, and splitting
			// it produced a line this parser then refused — so the export fell
			// through and silently did not persist.
			cur.WriteByte(c)
			cur.WriteByte(line[i+1])
			i++
			depth++
			open = true
		case depth > 0 && (c == ')' || c == '}'):
			cur.WriteByte(c)
			depth--
			open = true
		case (c == ' ' || c == '\t') && depth == 0 && !tick:
			flush()
		default:
			cur.WriteByte(c)
			open = true
		}
	}
	flush()
	// Anything left open means this is not a line to interpret. Falling
	// through to ordinary execution is always the safe outcome here.
	if quote != 0 || esc || depth != 0 || tick {
		return nil, false
	}
	return words, true
}

// validEnvName reports whether s is a POSIX environment variable name.
func validEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// applyEnvCommand performs an export/unset on the Helix process itself.
//
// Returns the names that changed. Every later command inherits them, because
// exec.Cmd with a nil Env hands the child os.Environ().
func applyEnvCommand(command string, assigns []envAssignment, env shell.Env) ([]string, error) {
	if assigns[0].Unset {
		// No evaluation needed: unset takes plain names.
		names := make([]string, 0, len(assigns))
		for _, a := range assigns {
			if err := os.Unsetenv(a.Name); err != nil {
				return nil, fmt.Errorf("unset %s: %w", a.Name, err)
			}
			names = append(names, a.Name)
		}
		return names, nil
	}

	// Ask a real shell to perform the assignment and read the results back,
	// NUL-separated so a value containing newlines or spaces survives intact.
	// This is what buys correct handling of quotes, $VAR, ~ and $(...) without
	// a parser here.
	var printf strings.Builder
	printf.WriteString(command)
	printf.WriteString("; printf '%s\\0'")
	for _, a := range assigns {
		printf.WriteString(` "$` + a.Name + `"`)
	}

	shellToUse := resolveShell(env)
	out, err := exec.Command(shellToUse, "-c", printf.String()).Output()
	if err != nil {
		return nil, fmt.Errorf("%s could not evaluate the assignment: %w", shellToUse, err)
	}

	parts := strings.Split(string(out), "\x00")
	if len(parts) < len(assigns) {
		return nil, fmt.Errorf("%s returned %d values for %d variables",
			shellToUse, len(parts), len(assigns))
	}
	names := make([]string, 0, len(assigns))
	for i, a := range assigns {
		if err := os.Setenv(a.Name, parts[i]); err != nil {
			return nil, fmt.Errorf("set %s: %w", a.Name, err)
		}
		names = append(names, a.Name)
	}
	return names, nil
}

// posixShell reports whether this shell understands `export`/`unset`.
//
// cmd and PowerShell do not — they spell it `set` and `$env:`, with different
// semantics — and the evaluation below invokes the shell with `-c`, which
// those two do not take either. On Windows the interception is simply not
// offered and the command runs as it always has.
func posixShell(env shell.Env) bool {
	switch env.Shell {
	case "powershell", "cmd":
		return false
	case "", "unknown":
		return runtime.GOOS != "windows"
	default:
		return true
	}
}

// resolveShell picks the shell to evaluate with, mirroring ExecuteCommand.
func resolveShell(env shell.Env) string {
	switch env.Shell {
	case "powershell", "cmd":
		return env.Shell
	case "", "unknown":
		if runtime.GOOS == "windows" {
			return "cmd"
		}
		return "sh"
	default:
		return env.Shell
	}
}

// secretishNames are substrings that mark a value as not for the screen.
var secretishNames = []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL"}

// redactEnvValue renders a variable's new value for the confirmation line.
//
// Bounded and redacted, because this line is also what a transcript log keeps.
// It is NOT a complete defence — ExecuteCommand echoes the command itself
// before this runs, so `export OPENAI_API_KEY=sk-…` has already been printed —
// but repeating a secret is worse than printing it once, and the length is
// worth knowing when the value is a PATH.
func redactEnvValue(name, value string) string {
	upper := strings.ToUpper(name)
	for _, marker := range secretishNames {
		if strings.Contains(upper, marker) {
			return fmt.Sprintf("(%d characters, not shown)", len(value))
		}
	}
	const max = 72
	if len(value) > max {
		return value[:max] + "…"
	}
	return value
}

// applyEnvCommandForTurn is the ExecuteCommand hook: it decides whether this
// turn may persist a variable, does it, and says what changed.
//
// Saying so is not decoration. The reason this was worth building is that the
// old behaviour was SILENT — the command appeared to succeed and changed
// nothing — so a version that silently succeeds for typed turns and silently
// does nothing for spoken ones would have moved the trap rather than removed
// it.
func applyEnvCommandForTurn(command string, assigns []envAssignment,
	config ExecuteConfig, env shell.Env) error {

	if config.DryRun {
		fmt.Println(color.YellowString("  would set: ") + envNamesOf(assigns))
		return nil
	}

	if turnIsSpoken() {
		// Runs in a child as before, so the turn is not a no-op, but nothing
		// persists — and the difference is stated rather than left to be
		// discovered. ADR-005: voice may reduce what is collected or what is
		// reachable, never increase it.
		color.Yellow("  spoken commands cannot change the session environment — "+
			"%s ran in a subshell only. Type it to make it stick.", envNamesOf(assigns))
		return runInChild(command, env)
	}

	names, err := applyEnvCommand(command, assigns, env)
	if err != nil {
		return err
	}
	for _, name := range names {
		if assigns[0].Unset {
			color.Green("  unset %s  ·  later commands will not see it", name)
			continue
		}
		color.Green("  %s=%s", name, redactEnvValue(name, os.Getenv(name)))
	}
	return nil
}

// envNamesOf renders the variable names for a one-line notice.
func envNamesOf(assigns []envAssignment) string {
	names := make([]string, 0, len(assigns))
	for _, a := range assigns {
		names = append(names, a.Name)
	}
	return strings.Join(names, ", ")
}

// runInChild executes a command the ordinary way, for the spoken path that is
// deliberately denied persistence.
func runInChild(command string, env shell.Env) error {
	cmd := exec.Command(resolveShell(env), "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
