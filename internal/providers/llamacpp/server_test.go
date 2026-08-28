// internal/providers/llamacpp/server_test.go
// Purpose: launching llama-server, and telling a slow load from a failed one.
package llamacpp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeServerBin installs a script named llama-server on PATH so Start can find
// something real to launch.
func fakeServerBin(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return path
}

func TestStartRequiresAModel(t *testing.T) {
	fakeServerBin(t, "exit 0\n")
	logPath := filepath.Join(t.TempDir(), "log")

	if _, err := Start(StartOptions{LogPath: logPath}); err == nil {
		t.Error("a launch with no model must be refused")
	}
	// Path and repo are alternatives; accepting both would silently ignore one.
	if _, err := Start(StartOptions{
		ModelPath: "/x.gguf", HFRepo: "org/repo", LogPath: logPath,
	}); err == nil {
		t.Error("specifying both a path and a repo must be refused")
	}
	// A log is required: a detached process whose output goes nowhere cannot be
	// diagnosed when it fails to load.
	if _, err := Start(StartOptions{HFRepo: "org/repo"}); err == nil {
		t.Error("a launch with no log path must be refused")
	}
}

func TestStartRejectsMissingModelFile(t *testing.T) {
	fakeServerBin(t, "exit 0\n")
	_, err := Start(StartOptions{
		ModelPath: filepath.Join(t.TempDir(), "absent.gguf"),
		LogPath:   filepath.Join(t.TempDir(), "log"),
	})
	if err == nil {
		t.Fatal("a model path that does not exist must fail before launching")
	}
	if !strings.Contains(err.Error(), "not readable") {
		t.Errorf("error should name the cause: %v", err)
	}
}

func TestStartLaunchesAndLogs(t *testing.T) {
	fakeServerBin(t, "echo starting; echo loaded >&2; sleep 5\n")

	dir := t.TempDir()
	model := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(model, []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "server.log")

	srv, err := Start(StartOptions{ModelPath: model, Port: "8099", LogPath: logPath})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(srv.PID); err == nil {
			_ = p.Kill()
		}
	})

	if srv.PID <= 0 {
		t.Error("a launched server must report its pid")
	}
	if !strings.Contains(srv.Command, "-m "+model) || !strings.Contains(srv.Command, "--port 8099") {
		t.Errorf("command should record what was run: %q", srv.Command)
	}
	if !strings.Contains(srv.StopHint(), "kill") {
		t.Errorf("the user owns this process; StopHint must say how to stop it: %q", srv.StopHint())
	}

	// Both streams must reach the log — a model-load failure is usually on stderr.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(logPath)
		if strings.Contains(string(data), "starting") && strings.Contains(string(data), "loaded") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	data, _ := os.ReadFile(logPath)
	t.Errorf("log missing stdout and/or stderr: %q", data)
}

// TestStartUsesHFRepoWhenGiven covers the download-and-serve path.
func TestStartUsesHFRepoWhenGiven(t *testing.T) {
	fakeServerBin(t, "sleep 5\n")
	logPath := filepath.Join(t.TempDir(), "log")

	srv, err := Start(StartOptions{HFRepo: "ggml-org/gemma", Port: "8098", LogPath: logPath})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(srv.PID); err == nil {
			_ = p.Kill()
		}
	})
	if !strings.Contains(srv.Command, "-hf ggml-org/gemma") {
		t.Errorf("command should use -hf: %q", srv.Command)
	}
}

func TestWaitReadySucceedsWhenProbePasses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var calls int
	err := WaitReady(ctx, "http://127.0.0.1:8080", func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("not yet")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WaitReady should succeed once the probe passes: %v", err)
	}
	if calls < 3 {
		t.Errorf("probe called %d times, expected polling", calls)
	}
}

// TestWaitReadyReportsTheLastError: "timed out" alone does not say whether the
// port was refused or the model failed to load.
func TestWaitReadyReportsTheLastError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()

	err := WaitReady(ctx, "http://127.0.0.1:8080", func(context.Context) error {
		return errors.New("connection refused")
	})
	if err == nil {
		t.Fatal("expected a timeout")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("timeout should carry the last probe error: %v", err)
	}
}

func TestAliveDetectsExitedProcess(t *testing.T) {
	fakeServerBin(t, "exit 0\n")
	dir := t.TempDir()
	model := filepath.Join(dir, "m.gguf")
	if err := os.WriteFile(model, []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv, err := Start(StartOptions{
		ModelPath: model, Port: "8097", LogPath: filepath.Join(dir, "log"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The script exits immediately; Alive must notice, which is what stops the
	// caller from waiting out a whole load budget for a process that is gone.
	//
	// This is why the child is reaped rather than released: an unreaped child
	// becomes a zombie, signal 0 succeeds on zombies, and a pid-existence check
	// would report it as still running forever.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !srv.Alive() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("Alive still reports a process that exited")
}

func TestLogTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte("a\nb\n\nc\nd\ne\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LogTail(path, 3)
	if strings.Join(got, ",") != "c,d,e" {
		t.Errorf("LogTail = %v, want the last three non-blank lines", got)
	}
	if got := LogTail(filepath.Join(t.TempDir(), "absent"), 5); got != nil {
		t.Errorf("a missing log must yield nothing, got %v", got)
	}
}

func TestDefaultLogPathIsUnderHelixDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}

	got, err := DefaultLogPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got) != filepath.Join(home, ".helix") {
		t.Errorf("log path = %q, want it under ~/.helix", got)
	}
}

// TestExitErrorReportsFailure: a server that dies loading a model must say why,
// since that is the whole reason the log tail is printed.
func TestExitErrorReportsFailure(t *testing.T) {
	fakeServerBin(t, "echo 'error loading model: bad magic' >&2; exit 1\n")
	dir := t.TempDir()
	model := filepath.Join(dir, "m.gguf")
	if err := os.WriteFile(model, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "log")

	srv, err := Start(StartOptions{ModelPath: model, Port: "8096", LogPath: logPath})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && srv.Alive() {
		time.Sleep(50 * time.Millisecond)
	}
	if srv.Alive() {
		t.Fatal("the process should have exited")
	}
	if srv.ExitError() == nil {
		t.Error("a non-zero exit must be reported")
	}
	// And the reason must be recoverable from the log, which is the only place
	// a detached process can put it.
	if tail := LogTail(logPath, 5); len(tail) == 0 ||
		!strings.Contains(strings.Join(tail, " "), "bad magic") {
		t.Errorf("log should carry the load failure, got %v", tail)
	}
}

// TestAliveWithoutStateFallsBack covers a Server that this process did not
// launch, where only the pid-existence check is available.
func TestAliveWithoutStateFallsBack(t *testing.T) {
	// os.Getpid() is certainly alive.
	if !(Server{PID: os.Getpid()}).Alive() {
		t.Error("the current process should read as alive")
	}
	// A pid that cannot exist.
	if (Server{PID: -1}).Alive() {
		t.Error("an impossible pid must not read as alive")
	}
	if err := (Server{PID: os.Getpid()}).ExitError(); err != nil {
		t.Errorf("no state means no exit error, got %v", err)
	}
}
