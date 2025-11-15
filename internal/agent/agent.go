package agent

import (
	"fmt"
	"regexp"
	"strings"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/shell"
	"helix/internal/ux"

	"github.com/fatih/color"
)

// Agent is the core "Agent Mode" orchestrator.
// It calls the planner LLM, runs the safety layer, then executes
// each step with the appropriate tools.
type Agent struct {
	env          shell.Env
	sandbox      *commands.DirectorySandbox
	execConfig   commands.ExecuteConfig
	gitManager   *commands.GitManager
	typingEffect bool
	ux           *ux.UX
}

// NewAgent creates a new Agent instance.
func NewAgent(
	env shell.Env,
	_ *ai.PromptBuilder, // kept for backwards compatibility, not used directly
	_ interface{}, // ragSystem placeholder to match your existing signature
	sandbox *commands.DirectorySandbox,
	execConfig commands.ExecuteConfig,
	typingEffect bool,
) *Agent {
	gm := commands.NewGitManager(env, execConfig, sandbox)

	return &Agent{
		env:          env,
		sandbox:      sandbox,
		execConfig:   execConfig,
		gitManager:   gm,
		typingEffect: typingEffect,
		ux:           ux.NewUX(),
	}
}

// HandleInput is the main entry point for Agent Mode.
// It takes raw user text (no slash prefix), asks the planner LLM for a JSON plan,
// runs the safety layer, then executes the resulting steps.
func (a *Agent) HandleInput(userInput string) {
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return
	}

	// 1) Build planner prompt with environment context
	envDesc := fmt.Sprintf(
		"OS: %s, Shell: %s, CWD: %s",
		a.env.OSName,
		a.env.Shell,
		a.sandbox.GetCurrentDirectory(),
	)

	plannerPrompt := ai.BuildPlannerPrompt(userInput, envDesc)

	// 2) Call planner model
	rawPlanOutput, err := ai.RunModel(plannerPrompt)
	if err != nil {
		color.Red("❌ Planner model error: %v", err)
		// Fallback: simple chat
		resp, chatErr := ai.RunModel(userInput)
		if chatErr != nil {
			color.Red("❌ Chat fallback failed: %v", chatErr)
			return
		}
		fmt.Println(strings.TrimSpace(resp))
		return
	}

	rawPlanOutput = strings.TrimSpace(rawPlanOutput)
	color.Yellow("🔍 Planner raw output: %s", rawPlanOutput)

	// 3) Parse and validate planner output
	plan, err := ai.ParsePlanFromModelOutput(rawPlanOutput)
	if err != nil {
		color.Yellow("⚠️  Planner parse error: %v", err)
		// Fallback: treat as plain chat
		resp, chatErr := ai.RunModel(userInput)
		if chatErr != nil {
			color.Red("❌ Chat fallback failed: %v", chatErr)
			return
		}
		fmt.Println(strings.TrimSpace(resp))
		return
	}

	// 4) Run safety layer to sanitize and augment plan
	safePlan, err := a.prepareSafePlan(userInput, plan)
	if err != nil {
		color.Yellow("⚠️  Safety layer error: %v", err)
		color.Yellow("⚠️  Proceeding with original planner plan.")
	} else {
		plan = safePlan
	}

	color.Cyan("🤖 Agent Intent: %s", plan.Intent)
	color.Cyan("🔧 Steps: %d", len(plan.Steps))

	// 5) Execute each step
	for i, step := range plan.Steps {
		color.Cyan("\n--- Step %d ---", i+1)

		switch step.Tool {
		case "response":
			a.handleResponseStep(step)

		case "shell":
			if err := a.handleShellStep(step); err != nil {
				color.Red("❌ Shell step failed: %v", err)
				return
			}

		case "git":
			if err := a.handleGitStep(step); err != nil {
				color.Red("❌ Git step failed: %v", err)
				return
			}

		case "package":
			if err := a.handlePackageStep(step); err != nil {
				color.Red("❌ Package step failed: %v", err)
				return
			}

		default:
			color.Yellow("⚠️  Unknown tool in step: %s", step.Tool)
		}
	}

	color.Green("\n🎉 Done.")
}

// --------------------------------------------------------
// Safety layer
// --------------------------------------------------------

// prepareSafePlan returns a sanitized / augmented copy of the plan.
// - Replaces version placeholders in shell/tag using the version found in the user input
// - Detects file-mutating shell steps and records target files
// - Inserts a git add step before git commit when needed
func (a *Agent) prepareSafePlan(userInput string, plan *ai.Plan) (*ai.Plan, error) {
	if plan == nil {
		return nil, fmt.Errorf("nil plan")
	}

	// Deep copy
	safe := *plan
	safe.Steps = make([]ai.PlanStep, len(plan.Steps))
	for i, s := range plan.Steps {
		safe.Steps[i] = s
	}

	requestedVersion := extractSemanticVersion(userInput)

	var mutatedPaths []string
	hasGitCommit := false
	hasGitAdd := false

	for i, s := range safe.Steps {
		switch strings.ToLower(s.Tool) {
		case "shell":
			cmd := strings.TrimSpace(s.Command)

			// Replace obvious placeholders with the user-requested version
			if requestedVersion != "" {
				for _, ph := range []string{"NEW_VERSION", "new_version", "VERSION_HERE"} {
					if strings.Contains(cmd, ph) {
						cmd = strings.ReplaceAll(cmd, ph, requestedVersion)
					}
				}
			}

			s.Command = cmd
			safe.Steps[i] = s

			// Detect file-mutating shell commands and record common targets
			if isFileMutatingShell(cmd) {
				if strings.Contains(cmd, "README.md") {
					mutatedPaths = append(mutatedPaths, "README.md")
				} else if strings.Contains(cmd, "README") {
					mutatedPaths = append(mutatedPaths, "README.md")
				}
			}

		case "git":
			action := strings.ToLower(strings.TrimSpace(s.Action))
			if action == "commit" {
				hasGitCommit = true
			}
			if action == "add" {
				hasGitAdd = true
			}

			if action == "tag" && requestedVersion != "" {
				name := strings.TrimSpace(s.Args["name"])
				if name == "" || name == "NEW_VERSION" || name == "new_version" || name == "VERSION_HERE" {
					if s.Args == nil {
						s.Args = map[string]string{}
					}

					// Prefer vX.Y.Z if user mentioned it that way, otherwise just X.Y.Z
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

	// Insert git add if:
	// - we have a commit
	// - we have mutated files
	// - we don't already have a git add action
	if hasGitCommit && len(mutatedPaths) > 0 && !hasGitAdd {
		addStep := ai.PlanStep{
			Tool:   "git",
			Action: "add",
			Args: map[string]string{
				"paths": strings.Join(mutatedPaths, " "),
			},
		}

		// Insert before the first commit step
		insertIdx := len(safe.Steps)
		for i, s := range safe.Steps {
			if strings.ToLower(s.Tool) == "git" &&
				strings.ToLower(strings.TrimSpace(s.Action)) == "commit" {
				insertIdx = i
				break
			}
		}

		steps := append(safe.Steps, ai.PlanStep{})
		copy(steps[insertIdx+1:], steps[insertIdx:])
		steps[insertIdx] = addStep
		safe.Steps = steps

		color.Green("✅ Safety layer: inserted git add for paths: %s", strings.Join(mutatedPaths, " "))
	}

	return &safe, nil
}

// extractSemanticVersion finds the first semantic version like 1.2.3 in the text.
func extractSemanticVersion(text string) string {
	re := regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)
	return re.FindString(text)
}

// isFileMutatingShell does a simple heuristic check for commands that likely modify files.
func isFileMutatingShell(cmd string) bool {
	cmd = strings.ToLower(cmd)
	if strings.Contains(cmd, "sed ") || strings.Contains(cmd, "sed\t") {
		return true
	}
	if strings.Contains(cmd, " -i ") || strings.Contains(cmd, " -i''") || strings.Contains(cmd, " -i ''") {
		return true
	}
	if strings.Contains(cmd, ">>") || strings.Contains(cmd, " > ") {
		return true
	}
	if strings.Contains(cmd, " tee ") {
		return true
	}
	return false
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// --------------------------------------------------------
// Tool handlers
// --------------------------------------------------------

// handleResponseStep outputs the response message to the user.
func (a *Agent) handleResponseStep(step ai.PlanStep) {
	msg := strings.TrimSpace(step.Message)
	if msg == "" {
		return
	}

	if a.typingEffect {
		a.ux.Typewriter(msg)
	} else {
		fmt.Println(msg)
	}
}

// handleShellStep validates and executes a shell command step.
func (a *Agent) handleShellStep(step ai.PlanStep) error {
	cmd := strings.TrimSpace(step.Command)
	if cmd == "" {
		return fmt.Errorf("empty shell command in plan")
	}

	color.Cyan("🖥️ Shell: %s", cmd)

	// Validate and clean inside existing pipeline (includes safety checks)
	valid, err := commands.ValidateAndCleanCommand(cmd)
	if err != nil {
		return fmt.Errorf("invalid shell command: %w", err)
	}

	return a.sandbox.WrapCommand(valid, a.execConfig, a.env)
}

// handleGitStep validates and executes a git action step.
func (a *Agent) handleGitStep(step ai.PlanStep) error {
	action := strings.TrimSpace(step.Action)
	if action == "" {
		return fmt.Errorf("missing git action in plan")
	}

	color.Cyan("🌿 Git: action=%s", action)
	return a.gitManager.ExecutePlannedAction(action, step.Args)
}

// handlePackageStep validates and executes a package manager action step.
func (a *Agent) handlePackageStep(step ai.PlanStep) error {
	// Validate action
	action := strings.TrimSpace(step.Action)
	if action == "" {
		return fmt.Errorf("package step missing action (install/update/remove)")
	}

	// Extract name from args
	name := strings.TrimSpace(step.Args["name"])
	if name == "" {
		return fmt.Errorf("package step missing args.name")
	}

	// Validate allowed actions (safety guarantee)
	switch action {
	case "install", "update", "remove":
		// OK
	default:
		return fmt.Errorf("unsupported package action: %s", action)
	}

	// Safety: package steps must NOT include command
	if step.Command != "" {
		return fmt.Errorf("invalid package step: must not include shell command (got: %s)", step.Command)
	}

	color.Cyan("📦 Package: %s %s", action, name)

	// Delegate to package manager
	commands.HandlePackageCommand(
		[]string{action, name},
		a.env,
		false,        // not mock mode
		a.execConfig, // executes through sandbox
	)

	return nil
}
