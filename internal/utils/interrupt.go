// internal/utils/interrupt.go
// Purpose: Process-wide SIGINT (Ctrl+C) routing so long-running Helix operations
// can be cancelled without killing the shell. At the raw-mode prompt, Ctrl+C is
// a byte handled by the line editor; during cooked-mode operations the kernel
// delivers SIGINT, and this manager converts it into context cancellation for
// every registered scope.
package utils

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// opEntry is one registered cancellable scope.
type opEntry struct {
	cancel context.CancelFunc
	dead   bool
}

var (
	interruptMu    sync.Mutex
	interruptStack []*opEntry
	interruptOnce  sync.Once
)

// InstallInterruptHandler registers the process-wide SIGINT handler exactly
// once. Every received SIGINT cancels all live registered operations instead
// of terminating Helix.
//
// Args: none.
// Returns: none.
// Complexity: O(1) plus one background goroutine.
func InstallInterruptHandler() {
	interruptOnce.Do(func() {
		ch := make(chan os.Signal, 2)
		signal.Notify(ch, os.Interrupt, syscall.SIGINT)
		go func() {
			for range ch {
				cancelAllOperations()
			}
		}()
	})
}

// RegisterOperation pushes a cancel function onto the interrupt stack so the
// next SIGINT cancels the running operation. Nested scopes (e.g. embeddings
// inside a knowledge update) are supported; one SIGINT cancels ALL live scopes
// so the whole pipeline unwinds.
//
// Args:
//   - cancel: the context.CancelFunc of the running operation.
//
// Returns: an unregister closure that MUST run (defer) when the operation ends.
// Complexity: O(1).
func RegisterOperation(cancel context.CancelFunc) func() {
	e := &opEntry{cancel: cancel}
	interruptMu.Lock()
	interruptStack = append(interruptStack, e)
	interruptMu.Unlock()
	return func() {
		interruptMu.Lock()
		e.dead = true
		// Pop dead tail entries so the stack stays compact.
		for len(interruptStack) > 0 && interruptStack[len(interruptStack)-1].dead {
			interruptStack = interruptStack[:len(interruptStack)-1]
		}
		interruptMu.Unlock()
	}
}

// cancelAllOperations cancels every live scope, outermost first, so dependent
// scopes unwind in order.
//
// Args: none. Returns: none. Complexity: O(n) in registered scopes.
func cancelAllOperations() {
	interruptMu.Lock()
	live := make([]context.CancelFunc, 0, len(interruptStack))
	for _, e := range interruptStack {
		if !e.dead {
			live = append(live, e.cancel)
		}
	}
	interruptMu.Unlock()
	for _, c := range live {
		c()
	}
}

// HasActiveOperation reports whether a cancellable operation is running.
//
// Args: none. Returns: bool. Complexity: O(1).
func HasActiveOperation() bool {
	interruptMu.Lock()
	defer interruptMu.Unlock()
	return len(interruptStack) > 0
}
