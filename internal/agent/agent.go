package agent

import (
	"fmt"
	"regexp"
	"strings"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/shell"
	"helix/internal/ux"
)

// Agent is the core Agent Mode orchestrator.
type Agent struct {
	env          shell.Env
	sandbox      *commands.DirectorySandbox
	execConfig   commands.ExecuteConfig
	gitManager   *commands.GitManager
	typingEffect bool
	ux           *ux.UX
}

// NewAgent creates a new Agent instance.
// Now accepts an injected UX instance for TUI compatibility.
func NewAgent(
	env shell.Env,
	_ interface{}, // RAG placeholder
	sandbox *commands.DirectorySandbox,
	execConfig commands.ExecuteConfig,
	typingEffect bool,
	gui *ux.UX, // NEW: Injected UX
) *Agent {
	gm := commands.NewGitManager(env, execConfig, sandbox)

	// Fallback if nil (e.g. tests)
	if gui == nil {
		gui = ux.NewUX()
	}

	return &Agent{
		env:          env,
		sandbox:      sandbox,
		execConfig:   execConfig,
		gitManager:   gm,
		typingEffect: typingEffect,
		ux:           gui,
	}
}

// HandleInput is the main entry point for Agent Mode.
func (a *Agent) HandleInput(userInput string) {
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return
	}

	envDesc := fmt.Sprintf(
		"OS: %s, Shell: %s, CWD: %s",
		a.env.OSName,
		a.env.Shell,
		a.sandbox.GetCurrentDirectory(),
	)

	plannerPrompt := ai.BuildPlannerPrompt(userInput, envDesc)

	// 1) Call planner model
	rawPlanOutput, err := ai.RunModel(plannerPrompt)
	if err != nil {
		a.ux.PrintError(fmt.Sprintf("Planner model error: %v", err))
		resp, chatErr := ai.RunModel(userInput)
		if chatErr != nil {
			a.ux.PrintError(fmt.Sprintf("Chat fallback failed: %v", chatErr))
			return
		}
		a.ux.PrintAIMessage(strings.TrimSpace(resp), a.typingEffect)
		return
	}

	rawPlanOutput = strings.TrimSpace(rawPlanOutput)

	// 2) Parse / validate plan
	plan, err := ai.ParsePlanFromModelOutput(rawPlanOutput)
	if err != nil {
		a.ux.PrintWarning(fmt.Sprintf("Planner parse error: %v", err))

		resp, chatErr := ai.RunModel(userInput)
		if chatErr != nil {
			a.ux.PrintError(fmt.Sprintf("Chat fallback failed: %v", chatErr))
			return
		}

		a.ux.PrintAIMessage(strings.TrimSpace(resp), a.typingEffect)
		return
	}

	// 3) Safety layer enhancement
	safePlan, err := a.prepareSafePlan(userInput, plan)
	if err != nil {
		a.ux.PrintWarning(fmt.Sprintf("Safety layer error: %v", err))
		a.ux.PrintWarning("Proceeding with original plan anyway.")
	} else {
		plan = safePlan
	}

	a.ux.PrintSystemMessage(fmt.Sprintf("Agent Intent: %s", plan.Intent))
	//a.ux.PrintDebug(fmt.Sprintf("Steps: %d", len(plan.Steps)))

	// 4) Execute each step
	for i, step := range plan.Steps {
		// Only print step header if there are multiple steps
		if len(plan.Steps) > 1 {
			a.ux.PrintSystemMessage(fmt.Sprintf("--- Step %d ---", i+1))
		}

		switch step.Tool {

		case "response":
			a.handleResponseStep(step)

		case "shell":
			if err := a.handleShellStep(step); err != nil {
				a.ux.PrintError(fmt.Sprintf("Shell step failed: %v", err))
				return
			}

		case "git":
			if err := a.handleGitStep(step); err != nil {
				a.ux.PrintError(fmt.Sprintf("Git step failed: %v", err))
				return
			}

		case "package":
			if err := a.handlePackageStep(step); err != nil {
				a.ux.PrintError(fmt.Sprintf("Package step failed: %v", err))
				return
			}

		default:
			a.ux.PrintWarning(fmt.Sprintf("Unknown tool: %s", step.Tool))
		}
	}

	a.ux.PrintSuccess("Done.")
}

//
// ──────────────────────────────────────────────────────────────
// 🧼 SAFETY LAYER
// ──────────────────────────────────────────────────────────────
//

func (a *Agent) prepareSafePlan(userInput string, plan *ai.Plan) (*ai.Plan, error) {
	if plan == nil {
		return nil, fmt.Errorf("nil plan")
	}

	// Deep copy
	safe := *plan
	safe.Steps = make([]ai.PlanStep, len(plan.Steps))
	copy(safe.Steps, plan.Steps)

	requestedVersion := extractSemanticVersion(userInput)

	var mutatedPaths []string
	hasGitCommit := false
	hasGitAdd := false

	// Scan + modify commands
	for i, s := range safe.Steps {
		switch s.Tool {

		case "shell":
			cmd := strings.TrimSpace(s.Command)

			// Replace version placeholders
			if requestedVersion != "" {
				for _, ph := range []string{"NEW_VERSION", "new_version", "VERSION_HERE"} {
					cmd = strings.ReplaceAll(cmd, ph, requestedVersion)
				}
			}

			s.Command = cmd
			safe.Steps[i] = s

			if isFileMutatingShell(cmd) {
				if strings.Contains(cmd, "README.md") {
					mutatedPaths = append(mutatedPaths, "README.md")
				}
			}

		case "git":
			action := strings.ToLower(s.Action)
			if action == "commit" {
				hasGitCommit = true
			}
			if action == "add" {
				hasGitAdd = true
			}

			if action == "tag" && requestedVersion != "" {
				name := strings.TrimSpace(s.Args["name"])
				if name == "" ||
					name == "NEW_VERSION" ||
					name == "new_version" ||
					name == "VERSION_HERE" {

					tag := requestedVersion
					if strings.Contains(userInput, "v"+requestedVersion) {
						tag = "v" + requestedVersion
					}

					s.Args["name"] = tag
					safe.Steps[i] = s
				}
			}
		}
	}

	mutatedPaths = uniqueStrings(mutatedPaths)

	// Auto-insert git add
	if hasGitCommit && len(mutatedPaths) > 0 && !hasGitAdd {
		addStep := ai.PlanStep{
			Tool:   "git",
			Action: "add",
			Args:   map[string]string{"paths": strings.Join(mutatedPaths, " ")},
		}

		insertIndex := len(safe.Steps)
		for i, st := range safe.Steps {
			if st.Tool == "git" && st.Action == "commit" {
				insertIndex = i
				break
			}
		}

		safe.Steps = append(safe.Steps, ai.PlanStep{})
		copy(safe.Steps[insertIndex+1:], safe.Steps[insertIndex:])
		safe.Steps[insertIndex] = addStep

		a.ux.PrintSuccess(fmt.Sprintf("Safety layer inserted git add for: %s", strings.Join(mutatedPaths, " ")))
	}

	return &safe, nil
}

func extractSemanticVersion(text string) string {
	re := regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)
	return re.FindString(text)
}

func isFileMutatingShell(cmd string) bool {
	lc := strings.ToLower(cmd)
	return strings.Contains(lc, "sed ") ||
		strings.Contains(lc, " -i ") ||
		strings.Contains(lc, ">>") ||
		strings.Contains(lc, " > ") ||
		strings.Contains(lc, " tee ")
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

//
// ──────────────────────────────────────────────────────────────
// 🔧 TOOL HANDLERS
// ──────────────────────────────────────────────────────────────
//

func (a *Agent) handleResponseStep(step ai.PlanStep) {
	msg := strings.TrimSpace(step.Message)
	if msg == "" {
		return
	}
	// UX handles typing effect check if needed, or we pass explicit flag
	a.ux.PrintAIMessage(msg, a.typingEffect)
}

// SHELL — includes risk scoring (Phase 3.5)
func (a *Agent) handleShellStep(step ai.PlanStep) error {
	cmd := strings.TrimSpace(step.Command)
	if cmd == "" {
		return fmt.Errorf("empty shell command")
	}

	a.ux.PrintCommand(cmd)

	// Hard safety validation
	validCmd, err := commands.ValidateAndCleanCommand(cmd)
	if err != nil {
		return fmt.Errorf("invalid shell command: %w", err)
	}

	// Soft risk layer
	risk, reasons := commands.AnalyzeShellRisk(validCmd)
	switch risk {

	case commands.ShellRiskLow:
		// execute directly

	case commands.ShellRiskMedium:
		a.ux.PrintWarning("Medium risk shell command:")
		for _, r := range reasons {
			a.ux.PrintWarning(fmt.Sprintf("   • %s", r))
		}

		// --- UPDATED INTERACTIVE CONFIRMATION ---
		if !a.ux.AskYesNo("Execute anyway?") {
			a.ux.PrintWarning("Command skipped")
			return nil
		}

	case commands.ShellRiskHigh:
		a.ux.PrintError("HIGH RISK — blocked")
		for _, r := range reasons {
			a.ux.PrintError(fmt.Sprintf("   • %s", r))
		}
		return fmt.Errorf("high-risk shell command blocked")
	}

	return a.sandbox.WrapCommand(validCmd, a.execConfig, a.env)
}

// GIT — supports safe + dangerous (Option C)
func (a *Agent) handleGitStep(step ai.PlanStep) error {
	action := strings.TrimSpace(step.Action)
	if action == "" {
		return fmt.Errorf("missing git action")
	}

	a.ux.PrintCommand(fmt.Sprintf("git action: %s", action))
	return a.gitManager.ExecutePlannedAction(action, step.Args)
}

// PACKAGE MANAGER
func (a *Agent) handlePackageStep(step ai.PlanStep) error {
	action := strings.ToLower(strings.TrimSpace(step.Action))
	if action == "" {
		return fmt.Errorf("package step missing action")
	}

	switch action {
	case "install", "update", "remove":
	default:
		return fmt.Errorf("unsupported package action: %s", action)
	}

	rawName := step.Args["name"]
	name := strings.TrimSpace(rawName)
	if name == "" {
		return fmt.Errorf("package step missing args.name")
	}

	if strings.TrimSpace(step.Command) != "" {
		return fmt.Errorf("invalid package step: must not have 'command'")
	}

	if err := commands.IsPackageActionSafe(action, name, a.env); err != nil {
		a.ux.PrintError(fmt.Sprintf("Package safety violation: %v", err))
		return err
	}

	a.ux.PrintCommand(fmt.Sprintf("Package: %s %s", action, name))

	commands.HandlePackageCommand(
		[]string{action, name},
		a.env,
		false,
		a.execConfig,
	)

	return nil
}
