package agent

import (
	"testing"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/shell"
)

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	env := shell.Env{OSName: "darwin", Shell: "zsh"}
	sandbox := commands.NewDirectorySandbox()
	execCfg := commands.DefaultExecuteConfig()
	return NewAgent(env, nil, nil, sandbox, execCfg, false)
}

func TestPrepareSafePlan_InsertsGitAddForVersionWorkflow(t *testing.T) {
	a := newTestAgent(t)

	userInput := "update version in README to 2.0.1 and commit with tag v2.0.1"
	plan := &ai.Plan{
		Intent: "multi_step",
		Steps: []ai.PlanStep{
			{Tool: "shell", Command: "sed -i '' 's/Current Version: .*/Current Version: NEW_VERSION/' README.md"},
			{Tool: "git", Action: "commit", Args: map[string]string{"message": "Update version in README"}},
			{Tool: "git", Action: "tag", Args: map[string]string{"name": "NEW_VERSION"}},
		},
	}

	safe, err := a.prepareSafePlan(userInput, plan)
	if err != nil {
		t.Fatalf("prepareSafePlan error: %v", err)
	}

	// Check that we now have a git add step
	foundAdd := false
	foundCommit := false
	foundTag := false

	for _, s := range safe.Steps {
		if s.Tool == "git" && s.Action == "add" {
			foundAdd = true
		}
		if s.Tool == "git" && s.Action == "commit" {
			foundCommit = true
		}
		if s.Tool == "git" && s.Action == "tag" {
			foundTag = true
		}
	}

	if !foundAdd {
		t.Fatalf("expected git add step to be inserted, but it was not")
	}
	if !foundCommit {
		t.Fatalf("expected commit step to remain")
	}
	if !foundTag {
		t.Fatalf("expected tag step to remain")
	}
}
