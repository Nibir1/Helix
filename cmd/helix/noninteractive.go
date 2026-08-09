// cmd/helix/noninteractive.go
// Purpose: Non-interactive shell bridge for Helix.
// Supports:
//   - helix -c "command"
//   - helix --command "command"
//   - helix script.sh
//   - echo "command" | helix
//
// This path intentionally bypasses the Bubble Tea TUI so Helix can behave
// like a normal shell for scripts, pipes, and command execution.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"helix/internal/commands"
	"helix/internal/shell"
	"helix/internal/utils"
	"helix/internal/ux"

	"github.com/mattn/go-isatty"
)

// maybeRunNonInteractive detects whether Helix was invoked in a non-interactive
// shell mode. If it returns handled=true, main() should exit with the returned code.
//
// Args: none.
// Returns:
//   - handled: true if Helix should terminate after this function.
//   - exitCode: process exit code when handled is true.
//
// Complexity: O(n) in stdin/script size.
func maybeRunNonInteractive() (bool, int) {
	args := os.Args[1:]

	// Case 1: no arguments.
	// If stdin is a terminal, boot the normal interactive experience.
	// If stdin is piped, execute the piped script/command.
	if len(args) == 0 {
		if stdinIsTerminal() {
			return false, 0
		}

		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "helix: failed to read stdin: %v\n", err)
			return true, 1
		}

		if len(bytes.TrimSpace(data)) == 0 {
			return true, 0
		}

		return true, exitCode(runNonInteractiveShell(string(data)))
	}

	// Case 2: explicit command execution.
	// Example:
	//   helix -c "ls -la"
	//   helix --command "git status"
	if args[0] == "-c" || args[0] == "--command" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: helix -c \"command\"")
			return true, 2
		}

		command := strings.Join(args[1:], " ")
		return true, exitCode(runNonInteractiveShell(command))
	}

	// Case 3: script execution.
	// Example:
	//   helix ./scripts/build.sh
	//   helix deploy.sh arg1 arg2
	//
	// We only do this when the first argument is an existing file so that
	// future Helix flags and natural-language TUI behavior remain safe.
	if !strings.HasPrefix(args[0], "-") && fileExists(args[0]) {
		command := strings.Join(args, " ")
		return true, exitCode(runNonInteractiveShell(command))
	}

	return false, 0
}

// stdinIsTerminal reports whether stdin is attached to an interactive terminal.
//
// Args: none.
// Returns: bool.
// Complexity: O(1).
func stdinIsTerminal() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// runNonInteractiveShell validates and executes a command/script without TUI.
//
// Args:
//   - raw: raw command or script text.
//
// Returns:
//   - error: execution, safety, or sandbox error.
//
// Complexity: O(n) in input size.
func runNonInteractiveShell(raw string) error {
	raw = strings.TrimRight(raw, "\r\n")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	env := shell.DetectEnvironment()
	sandbox := commands.NewDirectorySandbox()
	// Safety validation first.
	if err := validateNonInteractiveScript(raw); err != nil {
		fmt.Fprintf(os.Stderr, "helix: %v\n", err)
		return err
	}
	// Sandbox validation second.
	if ok, reason := sandbox.ValidateCommand(raw); !ok {
		err := fmt.Errorf("sandbox violation: %s", reason)
		fmt.Fprintf(os.Stderr, "helix: %v\n", err)
		return err
	}
	return executeNonInteractiveScript(raw, sandbox.GetCurrentDirectory(), env)
}

// validateNonInteractiveScript enforces Helix safety policy in non-interactive mode.
//
// High-risk commands are always blocked.
// Medium-risk commands are blocked unless HELIX_AUTOCONFIRM=1.
//
// Args:
//   - raw: raw command or script text.
//
// Returns:
//   - error: if the command/script is blocked.
//
// Complexity: O(n) in line count.
func validateNonInteractiveScript(raw string) error {
	if !commands.IsCommandSafe(raw) {
		return fmt.Errorf("command blocked by Helix safety policy")
	}

	if !utils.BracesBalanced(raw) {
		return fmt.Errorf("script has unbalanced braces")
	}

	if !utils.HasBalancedQuotes(raw) {
		return fmt.Errorf("script has unbalanced quotes")
	}

	allowMedium := strings.TrimSpace(os.Getenv("HELIX_AUTOCONFIRM")) == "1"

	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		risk, reasons := commands.AnalyzeShellRisk(line)
		switch risk {
		case commands.ShellRiskHigh:
			return fmt.Errorf("high-risk command blocked: %s", strings.Join(reasons, "; "))
		case commands.ShellRiskMedium:
			if !allowMedium {
				return fmt.Errorf(
					"medium-risk command requires interactive confirmation; set HELIX_AUTOCONFIRM=1 to allow non-interactive execution",
				)
			}
		}
	}

	return nil
}

// executeNonInteractiveScript executes the validated command/script.
//
// Args:
//   - raw: raw command/script text.
//   - dir: sandbox working directory.
//   - env: detected Helix shell environment.
//
// Returns:
//   - error: process start error or wrapped exit-status error.
//
// Complexity: O(command execution time).
func executeNonInteractiveScript(raw string, dir string, env shell.Env) error {
	cmd := ux.BuildShellCommand(raw, env.Shell)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("command exited with status %d: %w", exitErr.ExitCode(), err)
		}
		return err
	}

	return nil
}

// exitCode converts an error into a process exit code.
//
// Args:
//   - err: error from command execution.
//
// Returns:
//   - int: exit code.
//
// Complexity: O(1).
func exitCode(err error) int {
	if err == nil {
		return 0
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}

	return 1
}
