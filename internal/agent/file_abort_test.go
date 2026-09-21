// internal/agent/file_abort_test.go
// Purpose: the abort decision itself, at the call site rather than in a helper.
//
// fileMutates being correct is worth nothing if the dispatcher does not consult
// it — which is the mistake this session made twice. The dispatcher needs a
// full Agent to drive, so the property is pinned where it is decided.
package agent

import (
	"os"
	"strings"
	"testing"
)

func TestPlanDispatcherAbortsOnMutationsOnly(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	body := string(src)

	// Find the file case and check what it does with an error.
	start := strings.Index(body, `case "file":`)
	if start < 0 {
		t.Fatal("no file case in the plan dispatcher — file steps are not dispatched at all")
	}
	end := strings.Index(body[start:], `case "web":`)
	if end < 0 {
		t.Fatal("could not bound the file case")
	}
	block := body[start : start+end]

	if !strings.Contains(block, "if fileMutates(step.Action) {") {
		t.Error("the file dispatcher does not branch on fileMutates when a step fails — " +
			"either every failure aborts the plan (a not-found read costs a planner " +
			"round trip) or none does (a failed edit lets the plan continue as though " +
			"it had worked)")
	}
	if !strings.Contains(block, "continue") {
		t.Error("a failed read does not continue to the next step")
	}
	if !strings.Contains(block, "return obs") {
		t.Error("a failed mutation does not abort the plan")
	}
}
