// internal/commands/sandbox.go
package commands

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"helix/internal/confinement"
	"helix/internal/shell"

	"github.com/fatih/color"
)

// SandboxMode defines the restriction level
type SandboxMode int

const (
	SandboxDisabled SandboxMode = iota
	SandboxCurrentDir
	SandboxStrict
)

// DirectorySandbox with Mode C (read-only outside sandbox)
type DirectorySandbox struct {
	allowedDir  string
	mode        SandboxMode
	originalDir string
}

// evalSymlinksSafe wraps filepath.EvalSymlinks with panic recovery.
//
// The Go standard library's Windows symlink resolver (symlink_windows.go,
// toNorm) has a documented history of panicking on malformed inputs such
// as bare UNC prefixes and truncated drive specs (golang/go#63703,
// golang/go#40966). ValidateSafePath feeds caller-controlled strings into
// symlink resolution, so a hostile or fuzzed path could crash the whole
// shell on Windows.
//
// A panic during resolution is converted into an error so the sandbox
// FAILS CLOSED: an unresolvable path is simply not symlink-validated and
// still must pass the lexical root-prefix check.
//
// Args:
//   - path: candidate path to resolve.
//
// Returns:
//   - string: resolved path ("" when a panic was recovered).
//   - error: resolution error, or a synthesized error for a recovered panic.
//
// Complexity: O(len(path)) plus filesystem symlink traversal.
func evalSymlinksSafe(path string) (resolved string, err error) {
	defer func() {
		if r := recover(); r != nil {
			resolved = ""
			err = fmt.Errorf("path resolution aborted (treated as unsafe): %v", r)
		}
	}()
	return filepath.EvalSymlinks(path)
}

// NewDirectorySandbox creates default sandbox
func NewDirectorySandbox() *DirectorySandbox {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	// FIX: Immediately resolve symlinks to establish the "Canonical Root"
	// (panic-safe: Windows stdlib can panic on malformed paths).
	realCwd, err := evalSymlinksSafe(cwd)
	if err == nil {
		cwd = realCwd
	}
	return &DirectorySandbox{
		allowedDir:  cwd,
		mode:        SandboxCurrentDir,
		originalDir: cwd,
	}
}

// GetCurrentDirectory returns the sandbox root.
func (ds *DirectorySandbox) GetCurrentDirectory() string {
	return ds.allowedDir
}

// ================================================================
// MODE C CORE LOGIC
// Allow READ-ONLY absolute paths anywhere,
// but MODIFY/WRITE actions are blocked outside sandbox.
// ================================================================

// ValidateCommand checks if a command is allowed under sandbox rules
func (ds *DirectorySandbox) ValidateCommand(command string) (bool, string) {
	if ds.mode == SandboxDisabled {
		return true, ""
	}
	commandLower := strings.ToLower(command)

	// Whether this is a write/delete/edit operation is a property of the whole
	// command, not of any one word, so it is decided ONCE, here.
	//
	// It used to be re-decided inside the loop below — and, worse, *after* the
	// filesystem work whose result it gated. Every read-only command therefore
	// paid for a full symlink resolution of every absolute-looking word and
	// then threw the answer away. A fuzzer found it as a stall rather than a
	// wrong answer: ValidateSafePath is one lstat chain per path, so cost grew
	// linearly with the command string, reaching seconds for inputs a model can
	// plausibly emit. Hoisting the invariant is behaviour-preserving — if it is
	// false the loop could never have returned anything.
	if !ds.isDangerousWriteOperation(commandLower) {
		// 2. Directory escape is the only check that still applies.
		if ds.containsDirectoryEscape(commandLower) {
			return false, "command attempts directory escape"
		}
		return true, ""
	}

	// Resolution is a pure function of the path, so the same word is never
	// resolved twice, and the total number of distinct resolutions is capped.
	// Both matter on the slow storage this project targets at the edge.
	checker := &pathChecker{sandbox: ds, seen: make(map[string]error)}

	// Phase 15 Hardening: check absolute paths outside the sandbox for any
	// path-like argument. This closes the gap where extractFileArguments
	// might miss certain path patterns.
	for _, w := range strings.Fields(commandLower) {
		if strings.HasPrefix(w, "-") || !filepath.IsAbs(w) {
			continue
		}
		err, ok := checker.validate(w)
		if !ok {
			return false, pathBudgetRefusal
		}
		if err != nil {
			return false, fmt.Sprintf("sandbox violation: write/delete/edit operation outside sandbox: %s", w)
		}
	}

	// 1. Every file-looking argument of the write operation itself.
	for _, arg := range ds.extractFileArguments(commandLower) {
		if arg == "" {
			continue
		}
		err, ok := checker.validate(arg)
		if !ok {
			return false, pathBudgetRefusal
		}
		if err != nil {
			return false, fmt.Sprintf(
				"sandbox violation: write/delete/edit operation outside sandbox: %s", arg)
		}
	}

	// 2. Detect directory escape attempts (../ etc.)
	if ds.containsDirectoryEscape(commandLower) {
		return false, "command attempts directory escape"
	}
	return true, ""
}

// maxDistinctPathChecks bounds how many DIFFERENT paths one command may make
// the sandbox resolve. No real command names hundreds of distinct absolute
// paths; a generated or hostile one can name thousands, and each costs a chain
// of lstat calls. Refusing past the cap fails CLOSED — the sandbox never
// permits a path it declined to check.
const maxDistinctPathChecks = 512

const pathBudgetRefusal = "sandbox violation: too many distinct paths to validate in one command"

// pathChecker memoises ValidateSafePath and enforces the budget above.
//
// Memoising is sound because ValidateSafePath only reads: it resolves a path
// against the sandbox root and returns, touching nothing. The same word in one
// command therefore cannot produce two answers.
type pathChecker struct {
	sandbox *DirectorySandbox
	seen    map[string]error
}

// validate returns the validation error for path, and whether the command is
// still within its path budget. A false second return means "refuse", not "ok".
func (p *pathChecker) validate(path string) (error, bool) {
	if err, done := p.seen[path]; done {
		return err, true
	}
	if len(p.seen) >= maxDistinctPathChecks {
		return nil, false
	}
	_, err := p.sandbox.ValidateSafePath(path)
	p.seen[path] = err
	return err, true
}

// ================================================================
// ROBUST PATH VALIDATION
// ================================================================

// ValidateSafePath checks if a target path is inside the sandbox.
// It handles Symlinks, Case-Sensitivity (macOS/Windows), and Non-Existent files.
// FIX: Resolves relative paths against the CURRENT working directory,
// but validates them against the ORIGINAL sandbox root (the jail cell).
func (ds *DirectorySandbox) ValidateSafePath(targetPath string) (string, error) {
	if ds.mode == SandboxDisabled {
		return targetPath, nil
	}
	// 1. Absolutize against the CURRENT working directory (not just sandbox root)
	if !filepath.IsAbs(targetPath) {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = ds.allowedDir // Fallback to cached root if os.Getwd fails
		}
		targetPath = filepath.Join(cwd, targetPath)
	}
	// 2. Clean
	cleanTarget := filepath.Clean(targetPath)
	// 3. Resolve Symlinks (Target) — panic-safe on Windows.
	realTarget, err := evalSymlinksSafe(cleanTarget)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist; check parent directory instead
			parent := filepath.Dir(cleanTarget)
			realParent, perr := evalSymlinksSafe(parent)
			if perr == nil {
				realTarget = filepath.Join(realParent, filepath.Base(cleanTarget))
			} else {
				realTarget = cleanTarget
			}
		} else {
			realTarget = cleanTarget
		}
	}
	// 4. Resolve Symlinks (Root) - Ensure base comparison is accurate
	realRoot, err := evalSymlinksSafe(ds.allowedDir)
	if err != nil {
		realRoot = ds.allowedDir
	}
	// 5. Case-Insensitive Comparison (fixes /Users vs /users on macOS)
	rootCheck := strings.ToLower(realRoot)
	targetCheck := strings.ToLower(realTarget)
	// 6. The Security Check — the target must be the root itself or a child of
	// it. A plain prefix match would also admit sibling directories whose name
	// merely extends the root (root /tmp/jail, target /tmp/jail-x).
	sep := string(os.PathSeparator)
	inRoot := targetCheck == rootCheck ||
		strings.HasPrefix(targetCheck, rootCheck+sep)
	// A root that already ends in a separator (e.g. "/" on Unix) must not be
	// doubled — "/"+sep would be "//" and reject every absolute path.
	if strings.HasSuffix(rootCheck, sep) {
		inRoot = strings.HasPrefix(targetCheck, rootCheck)
	}
	if !inRoot {
		return "", fmt.Errorf("path %s is outside root %s", cleanTarget, ds.allowedDir)
	}
	return cleanTarget, nil
}

// ================================================================
// Dangerous write/edit/delete operations
// ================================================================

func (ds *DirectorySandbox) isDangerousWriteOperation(cmd string) bool {
	dangerous := []string{
		"rm ", "rm -rf", "mv ", "cp ", "dd ", "truncate",
		"chmod", "chown", "tee ", "echo " + ">",
		"sed -i", "perl -pi", "> ", ">> ", "git clone",
		"touch ", "mkdir ", "install ", "mkfifo ", "mknod ", "ln ",
	}
	for _, d := range dangerous {
		if strings.Contains(cmd, d) {
			return true
		}
	}
	return false
}

// ================================================================
// Escape detection
// ================================================================

func (ds *DirectorySandbox) containsDirectoryEscape(cmd string) bool {
	escapePatterns := []string{
		"../", "..\\", "cd ..", "cd ../", "cd ..\\",
	}

	for _, p := range escapePatterns {
		if strings.Contains(cmd, p) {
			return true
		}
	}
	return false
}

// ================================================================
// Extract file paths from a shell command
// ================================================================

func (ds *DirectorySandbox) extractFileArguments(cmd string) []string {
	var out []string

	cleanCmd := strings.ReplaceAll(cmd, "\"", "")
	cleanCmd = strings.ReplaceAll(cleanCmd, "'", "")

	words := strings.Fields(cleanCmd)

	for _, w := range words {
		if strings.HasPrefix(w, "-") {
			continue
		}
		if ds.isCommonNonFileArgument(w) {
			continue
		}
		if w == "|" || w == ">" || w == ">>" || w == "echo" {
			continue
		}
		out = append(out, w)
	}

	return out
}

func (ds *DirectorySandbox) isCommonNonFileArgument(arg string) bool {
	patterns := []string{
		"http://", "https://", "ftp://",
		"localhost", "127.0.0.1", "0.0.0.0",
		"yes", "no", "true", "false",
	}

	for _, p := range patterns {
		if strings.Contains(arg, p) {
			return true
		}
	}
	return false
}

// ================================================================
// Directory control
// ================================================================

// ChangeDirectory moves and announces the move, for callers acting on a
// request the user just typed.
func (ds *DirectorySandbox) ChangeDirectory(newDir string) error {
	if err := ds.SetDirectory(newDir); err != nil {
		return err
	}
	color.Green("📁 Changed directory: %s", ds.GetCurrentDirectory())
	return nil
}

// SetDirectory moves WITHOUT announcing it.
//
// The announcement and the move were one function, which is right for /cd — the
// user asked, so telling them is the answer — and wrong for anything that moves
// on the user's behalf. /reboot restoring the working directory printed a green
// "📁 Changed directory" line at column zero, ahead of the panel that was about
// to report the same fact properly. A function that reports on its caller's
// behalf leaves that caller no way to say it better.
func (ds *DirectorySandbox) SetDirectory(newDir string) error {
	if ds.mode == SandboxDisabled {
		return os.Chdir(newDir)
	}

	safePath, err := ds.ValidateSafePath(newDir)
	if err != nil {
		return fmt.Errorf("directory change outside sandbox: %s", newDir)
	}
	return os.Chdir(safePath)
}

// buildArgv converts a command + detected shell into an argv slice.
// Mirrors the platform logic of ux.BuildShellCommand without importing ux.
func buildArgv(cmd, shellName string) []string {
	shellName = strings.TrimSpace(shellName)
	lower := strings.ToLower(shellName)
	if lower == "unknown" {
		lower = ""
	}
	if runtime.GOOS == "windows" {
		switch lower {
		case "powershell", "powershell.exe":
			return []string{"powershell", "-NoProfile", "-Command", cmd}
		case "pwsh", "pwsh.exe":
			return []string{"pwsh", "-NoProfile", "-Command", cmd}
		case "cmd", "cmd.exe", "":
			return []string{"cmd", "/C", cmd}
		default:
			return []string{shellName, "-c", cmd}
		}
	}
	switch lower {
	case "powershell", "pwsh":
		bin := lower
		if shellName != "" {
			bin = shellName
		}
		return []string{bin, "-NoProfile", "-Command", cmd}
	case "", "sh":
		return []string{"/bin/sh", "-c", cmd}
	default:
		return []string{shellName, "-c", cmd}
	}
}

// runArgv executes argv with inherited stdio. lenient=true treats any
// non-zero exit as success (interactive shell semantics); lenient=false
// preserves WrapCommand's historical isNonFatalExit behavior for git/pkg flows.
func runArgv(argv []string, dir string, lenient bool) error {
	return runArgvEnv(argv, dir, lenient, nil)
}

// runArgvEnv is runArgv plus extra environment variables appended to the
// process environment (used by stealth mode for history suppression).
func runArgvEnv(argv []string, dir string, lenient bool, extraEnv []string) error {
	return runArgvEnvCapture(argv, dir, lenient, extraEnv, nil)
}

// runArgvEnvCapture is runArgvEnv with optional output tee-ing (P8.6).
//
// Why capture is opt-in rather than always on: assigning an *os.File to
// cmd.Stdout hands the child the terminal's file descriptor directly, so it
// sees a TTY and behaves interactively — colors, progress bars, pagers. Any
// other writer makes os/exec insert an os.Pipe and a copying goroutine, and
// the child's isatty check now fails. That is a real behavior change, so the
// default path (capture == nil) keeps the inherited descriptors byte for byte,
// and only an explicitly agentic turn pays the cost.
func runArgvEnvCapture(
	argv []string, dir string, lenient bool, extraEnv []string, capture *OutputCapture,
) error {
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = dir
	c.Env = os.Environ()
	if len(extraEnv) > 0 {
		c.Env = append(c.Env, extraEnv...)
	}
	if capture != nil {
		// Tee, never swallow: the user still sees the full live output.
		c.Stdout = io.MultiWriter(os.Stdout, capture.Stdout)
		c.Stderr = io.MultiWriter(os.Stderr, capture.Stderr)
	} else {
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
	}
	c.Stdin = os.Stdin
	err := c.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			// Record the true exit status before leniency discards it, so the
			// agentic observation trace can see a failure the user was
			// deliberately not bothered with.
			if capture != nil {
				capture.ExitCode = code
			}
			if code == 127 {
				return exitErr
			}
			// Phase 15 Fix: Surface permission denied errors (exit 1) for write commands
			// even in lenient mode. This ensures kernel confinement denials are visible.
			if code == 1 && isWriteCommand(strings.Join(argv, " ")) {
				return fmt.Errorf("command execution failed (exit %d): permission denied or sandbox violation", code)
			}
			if lenient || isNonFatalExit(strings.Join(argv, " "), code) {
				return nil
			}
			return fmt.Errorf("command execution failed (exit %d): %w", code, err)
		}
		return fmt.Errorf("command execution failed: %w", err)
	}
	return nil
}

// isWriteCommand reports whether a command is a known write operation.
func isWriteCommand(cmd string) bool {
	lc := strings.ToLower(cmd)
	writes := []string{"touch ", "mkdir ", "rm ", "mv ", "cp ", "chmod ", "chown ", "echo ", "cat ", "sed ", "tee "}
	for _, w := range writes {
		if strings.Contains(lc, w) {
			return true
		}
	}
	return false
}

// confinedArgv applies kernel-grade confinement when strict mode is active.
// Falls back silently to the advisory argv when no backend exists (the
// user-visible warning is emitted once, at SetMode time).
func (ds *DirectorySandbox) confinedArgv(argv []string, dir string) []string {
	if ds.mode != SandboxStrict {
		return argv
	}
	if wrapped, ok := confinement.WrapCommand(argv, confinement.Profile{Root: ds.allowedDir, Cwd: dir}); ok {
		return wrapped
	}
	return argv
}

// RunShellCommand executes a validated shell command honoring strict
// kernel confinement. Used by the Agent for direct/planner shell steps.
//
// Args: cmd: validated command; dir: working directory; shellName: shell.
// Returns: error only for launch failures (non-zero exits are informational).
// Complexity: O(command execution time).
func (ds *DirectorySandbox) RunShellCommand(cmd, dir, shellName string) error {
	return runArgv(ds.confinedArgv(buildArgv(cmd, shellName), dir), dir, true)
}

// RunShellCommandEnv is RunShellCommand with extra environment variables
// (stealth history suppression). It honors strict kernel confinement the
// same way, so private execution cannot escape the jail.
func (ds *DirectorySandbox) RunShellCommandEnv(cmd, dir, shellName string, extraEnv []string) error {
	argv := ds.confinedArgv(buildArgv(cmd, shellName), dir)
	return runArgvEnv(argv, dir, true, extraEnv)
}

// RunShellCommandCaptured is RunShellCommandEnv that ALSO tees stdout/stderr
// into a bounded tail buffer for the agentic harness (P8.6). Confinement,
// argv construction, and exit-code semantics are identical — capture changes
// only where the bytes go, never what runs or under what restrictions.
//
// A nil capture selects the original inherited-fd path exactly, which matters:
// see runArgvEnvCapture for why tee-ing is not free.
//
// Args: cmd/dir/shellName as RunShellCommand; extraEnv may be nil;
// capture may be nil.
// Returns: the same errors as RunShellCommand.
// Complexity: O(command execution time).
func (ds *DirectorySandbox) RunShellCommandCaptured(
	cmd, dir, shellName string, extraEnv []string, capture *OutputCapture,
) error {
	argv := ds.confinedArgv(buildArgv(cmd, shellName), dir)
	return runArgvEnvCapture(argv, dir, true, extraEnv, capture)
}

// WrapCommand executes the command strictly within the sandbox logic and, in
// strict mode, under kernel-grade confinement.
func (ds *DirectorySandbox) WrapCommand(cmd string, cfg ExecuteConfig, env shell.Env) error {
	// 1. Validate against the sandbox boundary
	if ok, reason := ds.ValidateCommand(cmd); !ok {
		// Phase 15 Fix: Prevent double "sandbox violation:" prefix
		if strings.HasPrefix(reason, "sandbox violation: ") {
			return fmt.Errorf("%s", reason)
		}
		return fmt.Errorf("sandbox violation: %s", reason)
	}
	if cfg.DryRun {
		color.Yellow("[Dry Run] Would execute: %s", cmd)
		return nil
	}
	// 2. Prepare execution context (dynamic CWD, confinement-aware argv).
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		cwd = ds.allowedDir
	}
	return runArgv(ds.confinedArgv(buildArgv(cmd, env.Shell), cwd), cwd, false)
}

func (ds *DirectorySandbox) PrintStatus() {
	color.Cyan("🔒 Sandbox Status:")
	color.Cyan("  Mode: %s", ds.ModeString())
	if ds.mode == SandboxStrict {
		color.Cyan("  Confinement: %s", confinement.BackendName())
	}
	color.Cyan("  Allowed Directory: %s", ds.allowedDir)

	cwd, _ := os.Getwd()
	color.Cyan("  Current Working Directory: %s", cwd)
}

// ================================================================
// Helpers
// ================================================================

func (ds *DirectorySandbox) SetMode(mode SandboxMode) {
	ds.mode = mode
	color.Yellow("🔒 Sandbox mode set to: %s", ds.ModeString())
	if mode == SandboxStrict {
		if confinement.Supported() {
			color.Green("🛡 kernel-grade confinement active: %s", confinement.BackendName())
		} else {
			color.Yellow("⚠ kernel confinement unavailable on this platform; strict mode remains advisory")
		}
	}
}

func (ds *DirectorySandbox) GetMode() SandboxMode {
	return ds.mode
}

func (ds *DirectorySandbox) ModeString() string {
	switch ds.mode {
	case SandboxDisabled:
		return "Disabled (no restrictions)"
	case SandboxCurrentDir:
		return "Current Directory Only"
	case SandboxStrict:
		return "Strict (current dir + subdirs only)"
	default:
		return "Unknown"
	}
}

func (ds *DirectorySandbox) ResetToOriginal() error {
	if err := os.Chdir(ds.originalDir); err != nil {
		return err
	}

	ds.allowedDir = ds.originalDir
	color.Green("📁 Reset to: %s", ds.originalDir)
	return nil
}
