// internal/diagnostics/diagnostics.go
// Purpose: Telemetry-free crash diagnostics. Panics and fatal signals produce a
// local, secret-redacted JSON report under ~/.helix (0600, last 5 retained).
// Reports are NEVER transmitted: this package imports no networking primitives
// (enforced by TestNoNetworkImports in CI). Opt out with HELIX_CRASH_REPORTS=off.
package diagnostics

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// EnvDisable opts out of crash reporting entirely ("off").
	EnvDisable = "HELIX_CRASH_REPORTS"
	// EnvSelftestPanic triggers a deliberate startup panic for verification.
	EnvSelftestPanic = "HELIX_SELFTEST_PANIC"
	// reportPrefix names crash report files.
	reportPrefix = "crash-"
	// maxReports is the retention cap; oldest reports are pruned beyond it.
	maxReports = 5
	// ExitCrash is the exit code when a panic was intercepted by RecoverMain.
	ExitCrash = 42
)

// Version is stamped by main() (config.HelixVersion) so reports identify builds.
var Version = "unknown"

var (
	mu                 sync.Mutex
	installed          bool
	reportsDirOverride string // unit-test hook only
)

// Report is the on-disk crash record.
type Report struct {
	Version   string            `json:"version"`
	OS        string            `json:"os"`
	Arch      string            `json:"arch"`
	GoVersion string            `json:"go_version"`
	Timestamp string            `json:"timestamp"`
	Reason    string            `json:"reason"`
	Stack     string            `json:"stack"`
	Env       map[string]string `json:"env"`
	Telemetry string            `json:"telemetry"`
}

// Summary is the lightweight view surfaced by /doctor.
type Summary struct {
	Path   string
	Reason string
	Time   string
}

// Enabled reports whether crash reporting is active for this process.
//
// Args: none. Returns: bool. Complexity: O(1).
func Enabled() bool {
	return strings.TrimSpace(os.Getenv(EnvDisable)) != "off"
}

// SetReportsDir overrides the report directory (unit tests only).
func SetReportsDir(dir string) {
	mu.Lock()
	reportsDirOverride = dir
	mu.Unlock()
}

// reportsDir resolves the crash report directory (~/.helix).
func reportsDir() string {
	mu.Lock()
	override := reportsDirOverride
	mu.Unlock()
	if override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".helix")
}

// isSensitiveEnv flags env vars whose values must never be persisted.
func isSensitiveEnv(name string) bool {
	n := strings.ToUpper(name)
	return strings.HasSuffix(n, "_KEY") ||
		strings.HasSuffix(n, "_TOKEN") ||
		strings.HasSuffix(n, "_SECRET") ||
		strings.HasSuffix(n, "_PASSWORD") ||
		strings.Contains(n, "APIKEY")
}

// RedactEnv converts an environ slice into a map with sensitive values masked.
//
// Args: env: os.Environ()-style pairs. Returns: redacted map. Complexity: O(n).
func RedactEnv(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		value := parts[1]
		if isSensitiveEnv(parts[0]) {
			value = "[REDACTED]"
		}
		out[parts[0]] = value
	}
	return out
}

// WriteReport persists one crash report (0600) and prunes to maxReports.
//
// Args: reason: human-readable crash reason; stack: captured stack trace.
// Returns: report path, or error (including when reporting is disabled).
// Complexity: O(env + stack size).
func WriteReport(reason string, stack []byte) (string, error) {
	if !Enabled() {
		return "", fmt.Errorf("crash reporting disabled via %s=off", EnvDisable)
	}
	dir := reportsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create crash dir: %w", err)
	}
	rep := Report{
		Version:   Version,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		GoVersion: runtime.Version(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Reason:    reason,
		Stack:     string(stack),
		Env:       RedactEnv(os.Environ()),
		Telemetry: "none — this report never leaves this machine",
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal crash report: %w", err)
	}
	name := reportPrefix + time.Now().UTC().Format("20060102T150405.000") + ".json"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write crash report: %w", err)
	}
	pruneReports()
	return path, nil
}

// ListReports returns pending crash report summaries, newest first.
//
// Args: none. Returns: summaries. Complexity: O(number of reports).
func ListReports() []Summary {
	names := reportNames()
	var sums []Summary
	for i := len(names) - 1; i >= 0; i-- { // newest first
		s := Summary{Path: filepath.Join(reportsDir(), names[i])}
		if data, err := os.ReadFile(s.Path); err == nil {
			var r Report
			if json.Unmarshal(data, &r) == nil {
				s.Reason = r.Reason
				s.Time = r.Timestamp
			}
		}
		sums = append(sums, s)
	}
	return sums
}

// PurgeReports deletes every crash report; returns the number removed.
//
// Args: none. Returns: count + error. Complexity: O(number of reports).
func PurgeReports() (int, error) {
	names := reportNames()
	for _, n := range names {
		if err := os.Remove(filepath.Join(reportsDir(), n)); err != nil {
			return 0, err
		}
	}
	return len(names), nil
}

// reportNames lists crash-*.json names sorted ascending (== chronological).
func reportNames() []string {
	entries, err := os.ReadDir(reportsDir())
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), reportPrefix) && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// pruneReports enforces the maxReports retention cap (oldest first).
func pruneReports() {
	names := reportNames()
	for len(names) > maxReports {
		_ = os.Remove(filepath.Join(reportsDir(), names[0]))
		names = names[1:]
	}
}

// sigNum maps fatal signals to conventional numbers for exit codes.
func sigNum(sig os.Signal) int {
	switch sig.String() {
	case "SIGSEGV":
		return 11
	case "SIGABRT":
		return 6
	case "SIGBUS":
		return 10
	case "SIGILL":
		return 4
	case "SIGFPE":
		return 8
	}
	return 0
}

// Install wires the fatal-signal trap (once). No-op when reporting is disabled.
//
// Args: none. Returns: none. Complexity: O(1) plus one background goroutine.
func Install() {
	mu.Lock()
	if installed {
		mu.Unlock()
		return
	}
	installed = true
	enabled := Enabled()
	mu.Unlock()
	if !enabled {
		return
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, fatalSignals()...)
	go func() {
		sig, ok := <-ch
		if !ok {
			return
		}
		path, _ := WriteReport(fmt.Sprintf("fatal signal: %v", sig), debug.Stack())
		fmt.Fprintf(os.Stderr, "\nhelix: fatal signal %v — crash report: %s\n", sig, path)
		signal.Stop(ch)
		os.Exit(128 + sigNum(sig))
	}()
}

// RecoverMain intercepts panics unwinding through main(), writes a redacted
// report, and exits with ExitCrash. Register with `defer` as main's FIRST
// defer so it runs last, after every other cleanup.
//
// Args: none. Returns: none (exits on panic). Complexity: O(stack size).
func RecoverMain() {
	r := recover()
	if r == nil {
		return
	}
	path, err := WriteReport(fmt.Sprintf("panic: %v", r), debug.Stack())
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nhelix: panic (%v); crash report skipped: %v\n", r, err)
	} else {
		fmt.Fprintf(os.Stderr, "\nhelix: panic intercepted — crash report: %s\n", path)
	}
	os.Exit(ExitCrash)
}

// Guard returns a deferred recover for background goroutines so a worker crash
// writes a report instead of killing the whole shell.
//
// Args: name: goroutine label for the report. Returns: defer-able closure.
func Guard(name string) func() {
	return func() {
		r := recover()
		if r == nil {
			return
		}
		_, _ = WriteReport(fmt.Sprintf("goroutine %s panic: %v", name, r), debug.Stack())
	}
}

// SelftestPanicIfRequested triggers a deliberate panic when
// HELIX_SELFTEST_PANIC=1, proving the diagnostics pipeline end-to-end.
func SelftestPanicIfRequested() {
	if strings.TrimSpace(os.Getenv(EnvSelftestPanic)) == "1" {
		panic("helix selftest: deliberate panic (HELIX_SELFTEST_PANIC=1)")
	}
}
