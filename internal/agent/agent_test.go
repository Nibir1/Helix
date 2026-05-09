// internal/agent/agent_test.go
package agent

import (
	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/shell"
	"helix/internal/stealth"
	"helix/internal/ux"
	"testing"
)

func TestAgentEnableStealth(t *testing.T) {
	env := shell.Env{OSName: "linux", Shell: "bash"}
	sandbox := commands.NewDirectorySandbox()
	execCfg := commands.DefaultExecuteConfig()
	gui := ux.NewUX()
	stealthExec := stealth.NewStealthExecutor(stealth.DefaultStealthConfig())

	ag := NewAgent(env, nil, sandbox, execCfg, false, gui, stealthExec, nil)

	if !ag.IsStealthEnabled() {
		t.Fatal("expected stealth enabled by default when executor provided")
	}
	ag.EnableStealth(false)
	if ag.IsStealthEnabled() {
		t.Fatal("expected stealth disabled after toggle")
	}
	ag.EnableStealth(true)
	if !ag.IsStealthEnabled() {
		t.Fatal("expected stealth enabled after toggle back")
	}
}

func TestAgentShellStepStealth(t *testing.T) {
	env := shell.Env{OSName: "linux", Shell: "bash"}
	sandbox := commands.NewDirectorySandbox()
	execCfg := commands.DefaultExecuteConfig()
	gui := ux.NewUX()
	stealthExec := stealth.NewStealthExecutor(stealth.DefaultStealthConfig())

	ag := NewAgent(env, nil, sandbox, execCfg, false, gui, stealthExec, nil)

	step := ai.PlanStep{
		Tool:    "shell",
		Command: "echo stealth_test",
	}

	err := ag.handleShellStep(step)
	if err != nil {
		t.Fatalf("shell step with stealth failed: %v", err)
	}
}
