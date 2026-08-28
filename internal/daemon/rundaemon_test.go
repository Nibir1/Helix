// internal/daemon/rundaemon_test.go
//
// Purpose: start a daemon in a test and guarantee it has fully STOPPED before
// the test's temporary directories are removed.
//
// Every daemon test used to do `go func() { _ = d.Run(ctx) }()` and never wait
// for it to return. On Unix that is invisible: an open file can be unlinked,
// so t.TempDir's RemoveAll succeeds whatever the daemon still holds. Windows
// refuses to delete a file another handle has open, so the same test failed in
// cleanup — after passing — with "The process cannot access the file because
// it is being used by another process".
//
// The ordering that makes this work: t.Cleanup runs LIFO, and t.TempDir
// registers its own removal when it is called. A cleanup registered AFTER the
// directory exists therefore runs BEFORE the directory is removed.
package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tmpHome creates a temporary HOME for a daemon test and owns its teardown.
//
// It exists because the teardown has a REQUIRED ORDER, and expressing that
// through two separate t.Cleanup registrations is a trap I already fell into:
// t.Cleanup is LIFO, so the LAST thing registered runs FIRST. Registering a
// chdir-restore before t.TempDir() therefore runs it AFTER the removal — the
// exact opposite of the intent, and invisible on Unix.
//
// Why the order matters at all: daemon.New() calls os.Chdir(os.UserHomeDir())
// on purpose, because a service started by launchd or systemd has no
// meaningful launch directory. With HOME redirected here, that moves the TEST
// PROCESS into the directory the test is about to delete. Unix removes a
// directory that is someone's cwd without complaint; Windows refuses with
// "The process cannot access the file because it is being used by another
// process", reported against the DIRECTORY rather than any file in it.
//
// So both steps live in ONE cleanup, in explicit order, with no LIFO reasoning
// required — and the removal is asserted rather than ignored, so a failure is
// attributed here instead of arriving from the testing framework.
func tmpHome(t *testing.T) string {
	t.Helper()

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir, err := os.MkdirTemp("", "hxhome")
	if err != nil {
		t.Fatalf("temp home: %v", err)
	}
	// Resolve symlinks so comparisons against os.Getwd() hold: macOS hands out
	// /var/... which resolves to /private/var/...
	if resolved, rerr := filepath.EvalSymlinks(dir); rerr == nil {
		dir = resolved
	}

	t.Cleanup(func() {
		// 1. Leave the directory before trying to delete it.
		if cerr := os.Chdir(orig); cerr != nil {
			t.Errorf("could not restore the working directory: %v", cerr)
		}
		// 2. Prove nothing is still standing in it.
		if cwd, werr := os.Getwd(); werr == nil && strings.HasPrefix(cwd, dir) {
			t.Errorf("the process is still inside the temporary home (%s); "+
				"Windows cannot remove a directory that is a process's cwd", cwd)
		}
		// 3. Remove it, and SAY SO if that fails.
		if rerr := os.RemoveAll(dir); rerr != nil {
			t.Errorf("temporary home was not removable: %v\n"+
				"something still holds a handle to it or to a file inside it", rerr)
		}
	})
	return dir
}

// runDaemon starts d and registers the shutdown wait.
//
// Call it after the temporary home has been created, so the wait is ordered
// ahead of that directory's removal.
func runDaemon(t *testing.T, d *Daemon) context.CancelFunc {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = d.Run(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			// Report it rather than hanging the suite: a daemon that will not
			// stop is a real defect, and the cleanup failure that follows on
			// Windows would otherwise be blamed on the temp directory.
			t.Error("daemon did not shut down within 10s of cancellation")
		}
	})
	return cancel
}

// TestMain proves no test in this package leaves the process sitting in a
// directory it asked to have deleted.
//
// This is the invariant behind the Windows cleanup failure, asserted on every
// platform — because on Unix the symptom is invisible and the cause is not.
// A test that forgets restoreCwd fails here, on the developer's own machine,
// instead of three days later on a Windows runner.
func TestMain(m *testing.M) {
	before, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "getwd:", err)
		os.Exit(1)
	}

	code := m.Run()

	after, err := os.Getwd()
	switch {
	case err != nil:
		// Getwd FAILING is the strongest form of the bug: the directory the
		// process is sitting in has already been removed underneath it.
		fmt.Fprintf(os.Stderr,
			"\nthe working directory is gone after these tests (%v).\n"+
				"A test chdir'd into a temporary home and did not restore it — "+
				"call restoreCwd(t) when creating that home.\n", err)
		if code == 0 {
			code = 1
		}
	case after != before:
		fmt.Fprintf(os.Stderr,
			"\nthe working directory moved during these tests:\n  before: %s\n  after:  %s\n"+
				"daemon.New() chdirs to $HOME by design, so a test that redirects HOME "+
				"must call restoreCwd(t). On Windows the leftover cwd makes the "+
				"temporary directory undeletable.\n", before, after)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
