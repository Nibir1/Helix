// tests/e2e/daemon_remote_test.go
// Purpose: BlackBox Phase 4 (P4.15) — prove the real `helix daemon` + `helix
// remote` IPC path end-to-end on every OS: boot a daemon subprocess in an
// isolated HOME, drive status/submit/stop over the platform transport (Unix
// socket on macOS/Linux, loopback TCP + token file on Windows), and confirm a
// low-risk submit executes inside the daemon's home-rooted sandbox. No AI,
// no mic, no network.
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestE2E_DaemonRemoteStatus(t *testing.T) {
	home, err := shortE2EHome(t)
	if err != nil {
		t.Fatalf("short tmp home: %v", err)
	}

	daemonProc := exec.Command(binPath, "daemon")
	daemonProc.Env = []string{
		"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH"),
	}
	var daemonLog bytes.Buffer
	daemonProc.Stdout = &daemonLog
	daemonProc.Stderr = &daemonLog
	if err := daemonProc.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	defer func() {
		if daemonProc.Process != nil {
			_ = daemonProc.Process.Kill()
		}
		_ = daemonProc.Wait()
	}()

	remote := func(args ...string) (string, error) {
		cmd := exec.Command(binPath, append([]string{"remote"}, args...)...)
		cmd.Env = []string{
			"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH"),
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// The transport becomes answerable shortly after boot (Unix socket bound,
	// or Windows conn.json published). Retry status until it answers.
	var status string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, err = remote("status")
		if err == nil && strings.Contains(status, "uptime_s") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(status, "uptime_s") {
		t.Fatalf("daemon never became ready\n----- daemon log -----\n%s\n----- last status -----\n%s (err=%v)",
			daemonLog.String(), status, err)
	}

	// submit: a cross-platform low-risk command (mkdir exists in cmd.exe and sh).
	probe := filepath.Join(home, "remote_probe_dir")
	// Forward slashes in the COMMAND, native separators for the stat: Windows
	// runners carry Git Bash, and bash reads the backslashes in an absolute
	// Windows path as escapes. Win32 accepts forward slashes everywhere.
	submit, err := remote("submit", "mkdir "+filepath.ToSlash(probe))
	if err != nil || !strings.Contains(submit, "reply") {
		t.Fatalf("remote submit did not return a reply (err=%v):\n%s", err, submit)
	}
	if st, serr := os.Stat(probe); serr != nil || !st.IsDir() {
		t.Fatalf("submit did not execute (stat err=%v)", serr)
	}

	// logs: the interaction journal tail is served over IPC.
	logs, err := remote("logs")
	if err != nil || !strings.Contains(logs, "submit") {
		t.Fatalf("remote logs missing journal entries (err=%v):\n%s", err, logs)
	}

	// stop: graceful shutdown acknowledged.
	stop, err := remote("stop")
	if err != nil || !strings.Contains(stop, "stopping") {
		t.Fatalf("remote stop unexpected (err=%v):\n%s", err, stop)
	}
}

// shortE2EHome returns a temp HOME; on macOS the Unix-socket sun_path limit
// (~104 chars) forces a short /tmp path, while Windows tolerates t.TempDir().
func shortE2EHome(t *testing.T) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return t.TempDir(), nil
	}
	return os.MkdirTemp("/tmp", "hxe2e")
}
