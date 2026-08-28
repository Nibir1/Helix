// internal/agent/agent_test.go
package agent

import (
	"errors"
	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/input"
	"helix/internal/shell"
	"helix/internal/stealth"
	"helix/internal/ux"
	"strings"
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

// TestNormalizeUserInputSmartQuotes verifies that Unicode curly quotes are
// converted to ASCII, preventing the planner-empty-output class of failures.
//
// Args:
//   - t: test runner.
//
// Returns: none.
// Complexity: O(1).
func TestNormalizeUserInputSmartQuotes(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"\u201CHello\u201D", `"Hello"`},
		{"\u2018single\u2019", `'single'`},
		{"\u2014dash\u2013", "-dash-"},
		{"\u2026", "..."},
		{"plain ascii", "plain ascii"},
	}
	for _, tc := range cases {
		got := normalizeUserInput(tc.input)
		if got != tc.want {
			t.Errorf("normalizeUserInput(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestExtractMutatedFiles verifies that file-creation and file-editing
// commands are detected so prepareSafePlan can auto-insert git add.
//
// Args:
//   - t: test runner.
//
// Returns: none.
// Complexity: O(1).
func TestExtractMutatedFiles(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"cat > Helix.go << 'EOF'\npackage main\nEOF", []string{"Helix.go"}},
		{"printf '%s\\n' 'hello' > Helix.go", []string{"Helix.go"}},
		{"sed -i '' 's/old/new/g' README.md", []string{"README.md"}},
		{"sed -i 's/old/new/g' README.md", []string{"README.md"}},
		{"echo hello >> output.txt", []string{"output.txt"}},
		{"touch newfile.txt", []string{"newfile.txt"}},
		{"ls -la", nil},
		{"git status", nil},
	}
	for _, tc := range cases {
		got := extractMutatedFiles(tc.cmd)
		if len(got) == 0 && len(tc.want) == 0 {
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("extractMutatedFiles(%q) = %v (len %d), want %v (len %d)",
				tc.cmd, got, len(got), tc.want, len(tc.want))
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("extractMutatedFiles(%q)[%d] = %q, want %q",
					tc.cmd, i, got[i], tc.want[i])
			}
		}
	}
}

// TestPrepareSafePlanAutoGitAddForMultipleFiles verifies that the safety
// layer inserts a git add step covering ALL mutated files, not just README.md.
//
// Args:
//   - t: test runner.
//
// Returns: none.
// Complexity: O(1).
func TestPrepareSafePlanAutoGitAddForMultipleFiles(t *testing.T) {
	env := shell.Env{OSName: "linux", Shell: "bash"}
	sandbox := commands.NewDirectorySandbox()
	execCfg := commands.DefaultExecuteConfig()
	gui := ux.NewUX()
	stealthExec := stealth.NewStealthExecutor(stealth.DefaultStealthConfig())
	ag := NewAgent(env, nil, sandbox, execCfg, false, gui, stealthExec, nil)

	plan := &ai.Plan{
		Intent: ai.IntentMultiStep,
		Steps: []ai.PlanStep{
			{Tool: "shell", Command: "printf 'package main' > Helix.go"},
			{Tool: "shell", Command: "sed -i '' 's/Old/New/g' README.md"},
			{Tool: "git", Action: "commit", Args: map[string]string{"message": "test commit"}},
		},
	}

	safe, err := ag.prepareSafePlan("update files and commit", plan)
	if err != nil {
		t.Fatalf("prepareSafePlan failed: %v", err)
	}

	// Find the auto-inserted git add step.
	var addStep *ai.PlanStep
	for i := range safe.Steps {
		if safe.Steps[i].Tool == "git" && safe.Steps[i].Action == "add" {
			addStep = &safe.Steps[i]
			break
		}
	}
	if addStep == nil {
		t.Fatal("expected auto-inserted git add step")
	}

	paths := addStep.Args["paths"]
	if !strings.Contains(paths, "Helix.go") {
		t.Errorf("git add paths must include Helix.go, got: %q", paths)
	}
	if !strings.Contains(paths, "README.md") {
		t.Errorf("git add paths must include README.md, got: %q", paths)
	}
}

// TestBrainFailureUtterance covers the spoken form of a model failure.
//
// The gap it closes: a voice turn against an unreachable model printed the error
// and a DEGRADED status line and said nothing at all. To someone speaking to the
// shell that is indistinguishable from a microphone that stopped working.
func TestBrainFailureUtterance(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, "could not reach"},
		{errors.New("context deadline exceeded"), "timed out"},
		{errors.New("no AI provider configured"), "slash setup"},
		{errors.New("HTTP 401: invalid api key"), "API key"},
		{errors.New(`HTTP 400: {"code":"unsupported_parameter"}`), "rejected the request"},
		{errors.New("dial tcp: connection refused"), "could not reach"},
	}
	for _, tc := range cases {
		got := brainFailureUtterance(tc.err)
		if !strings.Contains(got, tc.want) {
			t.Errorf("brainFailureUtterance(%v) = %q, want it to mention %q", tc.err, got, tc.want)
		}
		// Spoken text must not read out a JSON blob or a stack of provider
		// detail — the terminal has that, and it is unlistenable.
		if strings.ContainsAny(got, "{}\n") {
			t.Errorf("spoken text must stay prose, got %q", got)
		}
		if len(got) > 160 {
			t.Errorf("spoken text is %d chars; keep it short: %q", len(got), got)
		}
	}
}

// TestSpeakBrainFailureIsVoiceOnly: a typed session already sees the error, and
// speaking it there would be noise.
func TestSpeakBrainFailureIsVoiceOnly(t *testing.T) {
	ag, spoken := newTestAgent(t)

	ag.channel = input.ChannelText
	ag.speakBrainFailure(errors.New("boom"))
	if len(*spoken) != 0 {
		t.Errorf("a typed turn must not speak the error, said %v", *spoken)
	}

	ag.channel = input.ChannelVoice
	ag.speakBrainFailure(errors.New("boom"))
	if len(*spoken) != 1 {
		t.Fatalf("a voice turn must speak the failure, said %v", *spoken)
	}
}
