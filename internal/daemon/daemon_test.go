//go:build !windows

// internal/daemon/daemon_test.go
// Purpose: IPC round-trip integration — boots the daemon runtime against a
// temp HOME, drives status/submit/mode/stop over the real Unix socket, and
// proves a pipeline execution happened. No AI, no mic: the submit uses a
// high-confidence direct shell command (classifier bypasses the planner).
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDaemonIPCRoundTrip(t *testing.T) {
	home := shortTmpHome(t)
	t.Setenv("HOME", home)
	t.Setenv("HELIX_DAEMON_TEST", "1")

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()
	defer func() {
		cancel()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(d.Addr()); os.IsNotExist(err) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	// Wait for the socket.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(d.Addr()); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	send := func(t *testing.T, req Request) Response {
		t.Helper()
		conn, err := net.Dial("unix", d.Addr())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		if err := json.NewEncoder(conn).Encode(req); err != nil {
			t.Fatalf("encode: %v", err)
		}
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return resp
	}

	// status
	resp := send(t, Request{Type: TypeStatus})
	if !resp.OK || resp.State["addr"] != d.Addr() {
		t.Fatalf("status failed: %+v", resp)
	}

	// submit a low-risk direct shell command (no AI needed)
	probe := filepath.Join(home, "daemon_probe.txt")
	resp = send(t, Request{Type: TypeSubmit, Text: "touch " + probe})
	if !resp.OK {
		t.Fatalf("submit failed: %+v", resp)
	}
	if _, err := os.Stat(probe); err != nil {
		t.Fatalf("submitted command did not execute: %v", err)
	}

	// mode
	resp = send(t, Request{Type: TypeMode, Text: "manual"})
	if !resp.OK || resp.Meta["mode"] != "manual" {
		t.Fatalf("mode failed: %+v", resp)
	}

	// malformed request
	resp = send(t, Request{Type: "bogus"})
	if resp.OK || resp.Error == "" {
		t.Fatalf("unknown type must error: %+v", resp)
	}

	// journal recorded the submit
	entries := d.journal.Tail(10)
	found := false
	for _, e := range entries {
		if e.Kind == "submit" && e.Text == "touch "+probe {
			found = true
		}
	}
	if !found {
		t.Fatalf("submit not journalled: %+v", entries)
	}

	// stop
	resp = send(t, Request{Type: TypeStop})
	if !resp.OK {
		t.Fatalf("stop failed: %+v", resp)
	}
	time.Sleep(300 * time.Millisecond) // allow socket teardown
}

func TestSecondDaemonRefuses(t *testing.T) {
	home := shortTmpHome(t)
	t.Setenv("HOME", home)

	d, err := New()
	if err != nil {
		t.Fatalf("first daemon: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(d.Addr()); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := Listen(); err == nil {
		t.Fatal("second daemon must refuse to bind over a live one")
	}

	// A stale socket (dead daemon) must be reclaimed, not fatal.
	cancel()
	time.Sleep(300 * time.Millisecond)
	if _, err := Listen(); err != nil {
		t.Fatalf("stale socket must be reclaimed: %v", err)
	}
}

func TestSubmitRefusedWhileTTYActive(t *testing.T) {
	home := shortTmpHome(t)
	t.Setenv("HOME", home)

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(d.Addr()); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	send := func(req Request) Response {
		conn, err := net.Dial("unix", d.Addr())
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		if err := json.NewEncoder(conn).Encode(req); err != nil {
			t.Fatalf("encode: %v", err)
		}
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp
	}

	// No lock yet: submit is allowed (sandbox root = home).
	probe := filepath.Join(home, "daemon_lock_probe.txt")
	if resp := send(Request{Type: TypeSubmit, Text: "touch " + probe}); !resp.OK {
		t.Fatalf("unlocked submit failed: %+v", resp)
	}

	// A fresh TTY lock must make the daemon refuse injected submits (threat V7).
	Heartbeat()
	resp := send(Request{Type: TypeSubmit, Text: "touch " + probe + "2"})
	if resp.OK {
		t.Fatalf("submit must be refused while a TTY session is active: %+v", resp)
	}
	if !strings.Contains(resp.Error, "locked") {
		t.Fatalf("refusal should explain the lock: %+v", resp)
	}
}

func TestTTYLockHeartbeat(t *testing.T) {
	home := shortTmpHome(t)
	t.Setenv("HOME", home)

	if ttyActive() {
		t.Fatal("no lock yet: ttyActive must be false")
	}
	Heartbeat()
	if !ttyActive() {
		t.Fatal("fresh heartbeat must mark the TTY active")
	}

	// A stale lock (older than 5 min) must not hold the mic.
	lockPath := filepath.Join(home, ".helix", "active.lock")
	stale := `{"kind":"tty","pid":1,"ts":1000}`
	if err := os.WriteFile(lockPath, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if ttyActive() {
		t.Fatal("stale lock must not count as active")
	}
}

func TestJournalRedaction(t *testing.T) {
	j, err := NewJournalAt(filepath.Join(t.TempDir(), "interactions.jsonl"))
	if err != nil {
		t.Fatalf("journal: %v", err)
	}
	j.Record("submit", "voice", "clean request", "note\x00with\x01controls")
	long := make([]byte, 900)
	for i := range long {
		long[i] = 'x'
	}
	j.Record("submit", "text", string(long), "")

	entries := j.Tail(2)
	if len(entries) != 2 {
		t.Fatalf("tail: %+v", entries)
	}
	if entries[0].Text != "clean request" {
		t.Fatalf("text mangled: %q", entries[0].Text)
	}
	if len(entries[1].Text) > 600 {
		t.Fatalf("long text must be bounded, got %d runes", len(entries[1].Text))
	}
}

// shortTmpHome returns a SHORT temp HOME: macOS sun_path limits Unix socket
// paths to ~104 chars, and t.TempDir() paths are far longer.
func shortTmpHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hxd")
	if err != nil {
		t.Fatalf("short tmp home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
