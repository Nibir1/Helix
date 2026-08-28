package session

import "testing"

func TestTurnStructure(t *testing.T) {
	turn := Turn{Channel: "voice", UserText: "list go files"}
	if turn.Channel != "voice" || turn.UserText == "" {
		t.Error("turn fields must round-trip")
	}
	if DefaultCapacity <= 0 {
		t.Error("session capacity must be positive")
	}
}
