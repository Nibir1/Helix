// FILE: internal/utils/interrupt_test.go
// Purpose: Verify the interrupt manager cancels registered scopes and unwinds.
package utils

import (
	"context"
	"testing"
)

// TestRegisterOperationCancellation simulates a SIGINT and ensures every live
// scope is cancelled and the stack empties after unregistering.
func TestRegisterOperationCancellation(t *testing.T) {
	outer, outerCancel := context.WithCancel(context.Background())
	inner, innerCancel := context.WithCancel(context.Background())
	unregOuter := RegisterOperation(outerCancel)
	unregInner := RegisterOperation(innerCancel)

	cancelAllOperations() // same path the SIGINT goroutine uses

	if outer.Err() == nil || inner.Err() == nil {
		t.Fatal("expected interrupt to cancel all registered scopes")
	}
	unregInner()
	unregOuter()
	if HasActiveOperation() {
		t.Fatal("expected interrupt stack to be empty after unregister")
	}
}
