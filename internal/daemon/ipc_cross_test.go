// internal/daemon/ipc_cross_test.go
// Purpose: Phase 7 (P7.4) — exercise the platform IPC transport on every OS:
// Unix domain socket on macOS/Linux, loopback TCP + token file on Windows.
// Uses only cross-platform `mkdir` for the submit, so the Windows CI runner
// gets real daemon coverage instead of a blanket skip.
package daemon

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDaemonIPCCrossPlatform(t *testing.T) {
	home := tmpHome(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir() on Windows reads %USERPROFILE%

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	// Registered after crossTmpHome, so the shutdown wait is ordered ahead of
	// that directory's removal — Windows cannot delete files the daemon still
	// holds open.
	runDaemon(t, d)

	send := func(req Request) (Response, error) {
		conn, err := Dial()
		if err != nil {
			return Response{}, err
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		if err := json.NewEncoder(conn).Encode(req); err != nil {
			return Response{}, err
		}
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			return Response{}, err
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			return Response{}, err
		}
		return resp, nil
	}

	// The transport becomes answerable shortly after Run(): a Unix socket is
	// bound, or the Windows conn.json is published. Retry the status request.
	var resp Response
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err = send(Request{Type: TypeStatus})
		if err == nil && resp.OK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon never answered status (last err=%v): %+v", err, resp)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if resp.State["addr"] != d.Addr() {
		t.Fatalf("status addr mismatch: got %v want %s", resp.State["addr"], d.Addr())
	}

	// submit a cross-platform write command (mkdir exists in cmd.exe and sh).
	probeDir := filepath.Join(home, "cross_probe_dir")
	// Forward slashes in the COMMAND, native separators for the stat.
	//
	// Windows CI runners carry Git Bash, so DetectEnvironment can legitimately
	// pick bash there — and bash reads the backslashes in C:\Users\... as
	// escapes, quietly creating the directory somewhere else. Win32 accepts
	// forward slashes in every path API, so this is unambiguous on both.
	if resp, err := send(Request{Type: TypeSubmit, Text: "mkdir " + filepath.ToSlash(probeDir)}); err != nil || !resp.OK {
		t.Fatalf("submit failed: %+v err=%v", resp, err)
	}
	if st, err := os.Stat(probeDir); err != nil || !st.IsDir() {
		t.Fatalf("submitted mkdir did not execute (stat err=%v)", err)
	}

	if resp, err := send(Request{Type: TypeStop}); err != nil || !resp.OK {
		t.Fatalf("stop failed: %+v err=%v", resp, err)
	}
	time.Sleep(300 * time.Millisecond) // allow transport teardown
}
