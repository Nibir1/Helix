// internal/agent/agent.go
// Package agent provides the core Agent Mode orchestrator.
// It accepts natural language input, plans steps via the AI planner,
// and executes them through a safety‑first pipeline.
// Author: Helix Red Team
// Date: 2026-05-09
package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/rag"
	"helix/internal/recon"
	"helix/internal/shell"
	"helix/internal/stealth"
	"helix/internal/ux"
)

// Agent is the core Agent Mode orchestrator.
type Agent struct {
	env            shell.Env
	rag            *rag.RAGSystem
	sandbox        *commands.DirectorySandbox
	execConfig     commands.ExecuteConfig
	gitManager     *commands.GitManager
	typingEffect   bool
	ux             *ux.UX
	stealth        *stealth.StealthExecutor // stealth engine (memory‑only, log‑free)
	stealthEnabled bool                     // runtime toggle; if true, use stealth execution
	recon          *recon.ReconEngine

	// OnSlashCommand is called when input starts with "/".
	// It receives the full raw input line.  Return true if the command was
	// handled internally; false to pass it to the AI planner.
	OnSlashCommand func(string) bool
}

// NewAgent creates a new Agent instance.
//
// Args:
//
//	env          – detected shell environment
//	rag          – RAG system (may be nil if not initialized)
//	sandbox      – directory confinement manager
//	execConfig   – execution preferences (dry‑run, safe‑mode, etc.)
//	typingEffect – whether to animate AI responses
//	gui          – UX layer (may be nil)
//	stealthExec  – optional memory‑only executor (may be nil)
//	reconEng     – optional reconnaissance orchestrator (may be nil)
func NewAgent(
	env shell.Env,
	ragSystem *rag.RAGSystem, // CHANGED: concrete type instead of interface{}
	sandbox *commands.DirectorySandbox,
	execConfig commands.ExecuteConfig,
	typingEffect bool,
	gui *ux.UX,
	stealthExec *stealth.StealthExecutor,
	reconEng *recon.ReconEngine,
) *Agent {
	gm := commands.NewGitManager(env, execConfig, sandbox)

	// Fallback if nil (e.g. tests)
	if gui == nil {
		gui = ux.NewUX()
	}

	return &Agent{
		env:            env,
		rag:            ragSystem, // NEW: store RAG reference
		sandbox:        sandbox,
		execConfig:     execConfig,
		gitManager:     gm,
		typingEffect:   typingEffect,
		ux:             gui,
		stealth:        stealthExec,
		stealthEnabled: stealthExec != nil,
		recon:          reconEng,
	}
}

// EnableStealth toggles memory‑only execution mode at runtime.
// When enabled, shell commands are executed without leaving history
// or persistent disk traces.
func (a *Agent) EnableStealth(on bool) {
	if a.stealth == nil && on {
		a.ux.PrintWarning("Stealth engine not available – cannot enable stealth mode")
		return
	}
	a.stealthEnabled = on
	if on {
		a.ux.PrintSuccess("Stealth mode ENABLED – commands run without history or disk traces")
	} else {
		a.ux.PrintInfo("Stealth mode DISABLED – commands run normally")
	}
}

// IsStealthEnabled returns the current state of the stealth toggle.
func (a *Agent) IsStealthEnabled() bool {
	return a.stealthEnabled
}

// HandleInput is the main entry point for Agent Mode.
// It intercepts slash‑prefixed commands before the planner if an
// OnSlashCommand callback is set.
func (a *Agent) HandleInput(userInput string) {
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return
	}

	// --- Slash‑command interception ---
	if strings.HasPrefix(userInput, "/") && a.OnSlashCommand != nil {
		if a.OnSlashCommand(userInput) {
			return
		}
	}

	// --- RAG retrieval (if available) ---
	ragContext := ""
	if a.rag != nil && a.rag.IsInitialized() {
		cmds, err := a.rag.Retrieve(userInput)
		if err == nil && len(cmds) > 0 {
			var sb strings.Builder
			sb.WriteString("Relevant system commands (from the knowledge base):\n")
			for _, cmd := range cmds {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", cmd.Name, cmd.Description))
			}
			ragContext = sb.String()
		} else if err != nil {
			a.ux.PrintDebug(fmt.Sprintf("RAG retrieval skipped: %v", err))
		}
	}

	// --- Standard planning ---
	envDesc := fmt.Sprintf(
		"OS: %s, Shell: %s, CWD: %s",
		a.env.OSName,
		a.env.Shell,
		a.sandbox.GetCurrentDirectory(),
	)

	plannerPrompt := ai.BuildPlannerPrompt(userInput, envDesc, ragContext)

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

	// 4) Execute each step
	for i, step := range plan.Steps {
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

		case "recon":
			if err := a.handleReconStep(step); err != nil {
				a.ux.PrintError(fmt.Sprintf("Recon step failed: %v", err))
				return
			}

		default:
			a.ux.PrintWarning(fmt.Sprintf("Unknown tool: %s", step.Tool))
		}
	}

	// Mission complete – Red Team aesthetic
	a.ux.PrintSuccess("Helix :: GRID STATUS :: CLEAR")
}

//
// ──────────────────────────────────────────────────────────────
// SAFETY LAYER
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
// TOOL HANDLERS
// ──────────────────────────────────────────────────────────────
//

func (a *Agent) handleResponseStep(step ai.PlanStep) {
	msg := strings.TrimSpace(step.Message)
	if msg == "" {
		return
	}
	a.ux.PrintAIMessage(msg, a.typingEffect)
}

// SHELL — includes risk scoring and stealth toggle
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

	// Stealth execution if enabled and engine available
	if a.stealthEnabled && a.stealth != nil {
		a.ux.PrintDebug("Stealth mode: running command from memory")
		output, err := a.stealth.Execute(validCmd)
		if err != nil {
			return err
		}
		if output != "" {
			a.ux.PrintAIMessage(output, false)
		}
		return nil
	}

	// Default sandbox execution
	return a.sandbox.WrapCommand(validCmd, a.execConfig, a.env)
}

// handleReconStep processes a planner step with tool = "recon".
// It now offers to install missing tools before retrying the scan.
func (a *Agent) handleReconStep(step ai.PlanStep) error {
	toolName := strings.TrimSpace(step.Action)
	if toolName == "" {
		return fmt.Errorf("recon step missing action (tool name)")
	}
	args := make([]string, 0)
	if step.Args != nil {
		if flags, ok := step.Args["flags"]; ok {
			args = append(args, strings.Fields(flags)...)
		}
		if target, ok := step.Args["target"]; ok {
			args = append(args, target)
		}
	}
	a.ux.PrintCommand(fmt.Sprintf("Recon %s %s", toolName, strings.Join(args, " ")))

	// First attempt
	result, err := a.recon.RunTool(toolName, args...)
	if err != nil {
		return err // serious internal error
	}

	// Tool not found? → ask user, install, retry.
	if result.Error != nil && strings.Contains(strings.ToLower(result.Error.Error()), "not found") {
		a.ux.PrintInfo(fmt.Sprintf("Recon tool %q is not installed.", toolName))
		if a.ux.AskYesNo(fmt.Sprintf("Install %s now?", toolName)) {
			if installErr := a.installPackage(toolName); installErr != nil {
				a.ux.PrintError(fmt.Sprintf("Installation failed: %v", installErr))
				return nil
			}
			a.ux.PrintSuccess(fmt.Sprintf("%s installed successfully — retrying the scan…", toolName))

			// Retry after installation
			result2, err2 := a.recon.RunTool(toolName, args...)
			if err2 != nil {
				a.ux.PrintError(fmt.Sprintf("Recon retry failed: %v", err2))
				return nil
			}
			if result2.Error != nil {
				a.ux.PrintWarning(fmt.Sprintf("Recon retry still had issue: %v", result2.Error))
				return nil
			}
			a.ux.PrintSuccess(fmt.Sprintf("Recon completed in %v", result2.Elapsed))
			if len(result2.Parsed) > 0 {
				summary, _ := json.MarshalIndent(result2.Parsed, "", "  ")
				a.ux.PrintData(string(summary))
			} else {
				a.ux.PrintInfo("No open ports or interesting results found.")
			}
			return nil
		}
		// User declined installation
		a.ux.PrintInfo(fmt.Sprintf("Skipping recon step with %s (not installed).", toolName))
		return nil
	}

	// Some other execution error (e.g. timeout)
	if result.Error != nil {
		a.ux.PrintWarning(fmt.Sprintf("Recon tool %q issue: %v", toolName, result.Error))
		return nil
	}

	// Success – show results
	a.ux.PrintSuccess(fmt.Sprintf("Recon completed in %v", result.Elapsed))
	if len(result.Parsed) > 0 {
		summary, _ := json.MarshalIndent(result.Parsed, "", "  ")
		a.ux.PrintData(string(summary))
	} else {
		a.ux.PrintInfo("No open ports or interesting results found.")
	}
	return nil
}

// RunReconTool exposes the recon engine for manual /scan commands.
func (a *Agent) RunReconTool(tool, flags, target string) (*recon.ReconResult, error) {
	if a.recon == nil {
		return nil, fmt.Errorf("recon engine not available")
	}
	args := []string{}
	if flags != "" {
		args = append(args, strings.Fields(flags)...)
	}
	args = append(args, target)
	return a.recon.RunTool(tool, args...)
}

// installPackage attempts to install a single package using the detected
// system package manager, after running the normal safety checks.
func (a *Agent) installPackage(pkg string) error {
	if err := commands.IsPackageActionSafe("install", pkg, a.env); err != nil {
		return fmt.Errorf("package safety check failed: %w", err)
	}

	pm := commands.PackageManagerFactory(a.env)
	if pm == nil {
		return fmt.Errorf("no supported package manager found")
	}

	installCmd := pm.InstallCommand(pkg)
	a.ux.PrintInfo(fmt.Sprintf("Running: %s", installCmd))

	// Execute through the sandbox (respects dry‑run, safe‑mode, etc.).
	return a.sandbox.WrapCommand(installCmd, a.execConfig, a.env)
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

// GetUX returns the UX layer (useful for slash commands outside HandleInput).
func (a *Agent) GetUX() *ux.UX {
	return a.ux
}

// GetTypingEffect returns whether typing animation is enabled.
func (a *Agent) GetTypingEffect() bool {
	return a.typingEffect
}
