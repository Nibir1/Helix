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
	"testing"
	"time"
)

// restoreCwd puts the process's working directory back when the test ends.
//
// daemon.New() calls os.Chdir(os.UserHomeDir()) on purpose — a service started
// by launchd or systemd has no meaningful launch directory, so it gives itself
// a stable one. In a test that redirects HOME to a temporary directory, the
// TEST PROCESS therefore ends up sitting inside the directory the test is
// about to delete.
//
// Unix does not care: a directory can be removed while it is someone's cwd.
// Windows refuses outright — "The process cannot access the file because it is
// being used by another process", reported against the DIRECTORY rather than
// any file in it. That is how this surfaced: a daemon test that passed and
// then failed in cleanup.
//
// Call it immediately after creating the temporary home, so that LIFO cleanup
// ordering runs the restore BEFORE the removal.
func restoreCwd(t *testing.T) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Errorf("could not restore the working directory: %v", err)
		}
	})
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
