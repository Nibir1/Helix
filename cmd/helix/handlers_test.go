// cmd/helix/handlers_test.go
// Purpose: Guarantees unknown slash commands are handled gracefully and can
// never fall through to the shell/planner pipeline (the historical hang).
package main

import (
	"testing"
)

// TestHandleSlashCommandUnknownCommandIsHandled ensures a typo'd /command is
// intercepted and answered immediately instead of being executed or planned.
func TestHandleSlashCommandUnknownCommandIsHandled(t *testing.T) {
	if !handleSlashCommand("/definitely-not-a-real-command") {
		t.Fatal("expected unknown slash command to be handled (no fall-through)")
	}
}

// TestHandleSlashCommandAbsolutePathFallsThrough ensures absolute-path
// executables keep their legacy behavior (normal pipeline).
func TestHandleSlashCommandAbsolutePathFallsThrough(t *testing.T) {
	if handleSlashCommand("/usr/bin/echo hi") {
		t.Fatal("expected absolute-path executable to fall through to the pipeline")
	}
}
