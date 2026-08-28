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
	"testing"
	"time"
)

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
