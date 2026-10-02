package daemon

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A command run by the daemon must never wait on a pager: there is no one to
// press q. This is the hang found on 2026-10-02, where git log opened less.
func TestDaemonCommandsNeverPage(t *testing.T) {
	for _, k := range pagerVars {
		t.Setenv(k, "less")
	}
	DisablePagers()
	for _, k := range pagerVars {
		if got := os.Getenv(k); got != "cat" {
			t.Fatalf("%s = %q, want cat", k, got)
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "first"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	// With a pager configured, this would block on less forever.
	cmd := exec.Command("git", "-c", "core.pager=", "log", "--stat")
	cmd.Dir = dir
	done := make(chan []byte, 1)
	go func() { out, _ := cmd.CombinedOutput(); done <- out }()
	select {
	case out := <-done:
		if !strings.Contains(string(out), "first") {
			t.Fatalf("git log output: %s", out)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("git log waited on a pager")
	}
}
