// internal/shell/resize_unix.go
// Purpose: Instant terminal-resize notification via SIGWINCH on Unix platforms.
//go:build !windows

package shell

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyResize returns a channel that pulses on every terminal resize and a
// stop function that terminates the watcher goroutine cleanly.
func notifyResize() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	done := make(chan struct{})
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGWINCH)

	go func() {
		defer close(ch)
		for {
			select {
			case <-sig:
				// Non-blocking pulse: coalesce rapid resize storms.
				select {
				case ch <- struct{}{}:
				default:
				}
			case <-done:
				signal.Stop(sig)
				return
			}
		}
	}()

	return ch, func() { close(done) }
}
