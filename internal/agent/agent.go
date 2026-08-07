// internal/agent/agent.go
// Package agent provides the core Agent Mode orchestrator.
// It accepts natural language input, plans steps via the AI planner,
// and executes them through a safety‑first pipeline.

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/rag"
	"helix/internal/recon"
	"helix/internal/shell"
	"helix/internal/stealth"
	"helix/internal/utils"
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
func NewAgent(
	env shell.Env,
	ragSystem *rag.RAGSystem,
	sandbox *commands.DirectorySandbox,
	execConfig commands.ExecuteConfig,
	typingEffect bool,
	gui *ux.UX,
	stealthExec *stealth.StealthExecutor,
	reconEng *recon.ReconEngine,
) *Agent {
	gm := commands.NewGitManager(env, execConfig, sandbox)

	if gui == nil {
		gui = ux.NewUX()
	}

	return &Agent{
		env:            env,
		rag:            ragSystem,
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

// EnableStealth toggles local private-history mode.
func (a *Agent) EnableStealth(on bool) {
	if a.stealth == nil && on {
		a.ux.PrintWarning("Private execution engine not available")
		return
	}

	a.stealthEnabled = on

	if on {
		a.ux.PrintSuccess("Private history mode ENABLED — commands avoid writing shell history")
	} else {
		a.ux.PrintInfo("Private history mode DISABLED — commands run normally")
	}
}

// IsStealthEnabled returns the current state of the stealth toggle.
func (a *Agent) IsStealthEnabled() bool {
	return a.stealthEnabled
}

// HandleInput is the main entry point for Agent Mode.
func (a *Agent) HandleInput(userInput string) {
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return
	}

	// --- Slash-command interception ---
	if strings.HasPrefix(userInput, "/") && a.OnSlashCommand != nil {
		if a.OnSlashCommand(userInput) {
			return
		}
	}

	// --- Unified shell input classification ---
	classification := shell.Classify(userInput)
	a.ux.PrintDebug(fmt.Sprintf(
		"shell.classify: kind=%s confidence=%.2f root=%q reason=%q",
		classification.Kind, classification.Confidence,
		classification.RootCommand, classification.Reason,
	))
	if classification.Kind == shell.KindShellCommand && classification.Confidence >= shell.HighConfidence {
		if err := a.runDirectShellCommand(userInput); err != nil {
			a.ux.PrintError(fmt.Sprintf("Command failed: %v", err))
			return
		}
		return
	}

	// --- RAG retrieval (if available) ---
	ragContext := ""
	if a.rag != nil && a.rag.IsInitialized() {
		cmds, err := a.rag.Retrieve(userInput)
		if err == nil && len(cmds) > 0 {
			var sb strings.Builder
			sb.WriteString("Relevant system commands (from the knowledge base):\n")
			for _, cmd := range cmds {
				fmt.Fprintf(&sb, "- %s: %s\n", cmd.Name, cmd.Description)
			}
			ragContext = sb.String()
		} else if err != nil {
			a.ux.PrintDebug(fmt.Sprintf("RAG retrieval skipped: %v", err))
		}
	}

	// --- Standard planning ---
	// FIX: Feed the LIVE CWD to the planner, not just the sandbox root.
	cwd := a.sandbox.GetCurrentDirectory()
	if wd, err := os.Getwd(); err == nil && wd != "" {
		cwd = wd
	}
	envDesc := fmt.Sprintf(
		"OS: %s, Shell: %s, CWD: %s",
		a.env.OSName,
		a.env.Shell,
		cwd,
	)
	plannerPrompt := ai.BuildPlannerPrompt(userInput, envDesc, ragContext)

	// HELIX THINKER: animate the neural link while the planner reasons,
	// so the user always sees Helix thinking instead of a frozen prompt.
	think := ux.NewThinker("HELIX :: REASONING")
	think.Start()
	rawPlanOutput, err := ai.RunPlannerWithRetry(plannerPrompt)
	think.Stop()
	if err != nil {
		a.ux.PrintError(fmt.Sprintf("Planner model error: %v", err))
		think.Start()
		resp, chatErr := ai.RunModel(userInput)
		think.Stop()
		if chatErr != nil {
			a.ux.PrintError(fmt.Sprintf("Chat fallback failed: %v", chatErr))
			return
		}
		a.ux.PrintAIMessage(strings.TrimSpace(resp), a.typingEffect)
		return
	}
	rawPlanOutput = strings.TrimSpace(rawPlanOutput)
	plan, err := ai.ParsePlanFromModelOutput(rawPlanOutput)
	if err != nil {
		a.ux.PrintWarning(fmt.Sprintf("Planner parse error: %v", err))
		think.Start()
		resp, chatErr := ai.RunModel(userInput)
		think.Stop()
		if chatErr != nil {
			a.ux.PrintError(fmt.Sprintf("Chat fallback failed: %v", chatErr))
			return
		}
		a.ux.PrintAIMessage(strings.TrimSpace(resp), a.typingEffect)
		return
	}

	safePlan, err := a.prepareSafePlan(userInput, plan)
	if err != nil {
		a.ux.PrintWarning(fmt.Sprintf("Safety layer error: %v", err))
		a.ux.PrintWarning("Proceeding with original plan anyway.")
	} else {
		plan = safePlan
	}

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
}

// runDirectShellCommand executes a user-typed shell command through the full safety pipeline.
func (a *Agent) runDirectShellCommand(command string) error {
	a.ux.PrintDebug("shell.classify: direct shell execution (AI bypass)")
	step := ai.PlanStep{Tool: "shell", Command: command}
	return a.handleShellStep(step)
}

// ──────────────────────────────────────────────────────────────
// SAFETY LAYER
// ──────────────────────────────────────────────────────────────

func (a *Agent) prepareSafePlan(userInput string, plan *ai.Plan) (*ai.Plan, error) {
	if plan == nil {
		return nil, fmt.Errorf("nil plan")
	}

	safe := *plan
	safe.Steps = make([]ai.PlanStep, len(plan.Steps))
	copy(safe.Steps, plan.Steps)

	requestedVersion := extractSemanticVersion(userInput)

	var mutatedPaths []string
	hasGitCommit := false
	hasGitAdd := false

	for i, s := range safe.Steps {
		switch s.Tool {
		case "shell":
			cmd := strings.TrimSpace(s.Command)
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
				if name == "" || name == "NEW_VERSION" || name == "new_version" || name == "VERSION_HERE" {
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

// ──────────────────────────────────────────────────────────────
// TOOL HANDLERS
// ──────────────────────────────────────────────────────────────

func (a *Agent) handleResponseStep(step ai.PlanStep) {
	msg := strings.TrimSpace(step.Message)
	if msg == "" {
		return
	}
	a.ux.PrintAIMessage(msg, a.typingEffect)
}

// SHELL — includes risk scoring, stealth toggle, and native cd interception
func (a *Agent) handleShellStep(step ai.PlanStep) error {
	cmd := strings.TrimSpace(step.Command)
	if cmd == "" {
		return fmt.Errorf("empty shell command")
	}

	// NATIVE CD INTERCEPTION: a `cd` run in a child shell vanishes when the
	// child exits. Apply it to the live Helix process instead.
	if segments := splitShellChain(cmd); len(segments) > 0 && isCdCommand(segments[0]) {
		return a.executeNativeCd(cmd, segments)
	}

	// NATIVE HISTORY INTERCEPTION: `history`, `fc`, and `!!` run in a child
	// shell will only see the child's empty history. Intercept them and read
	// Helix's actual persistent history file.
	if isHistoryQuery(cmd) {
		return a.executeNativeHistory(cmd)
	}

	a.ux.PrintCommand(cmd)

	validCmd, err := commands.ValidateAndCleanCommand(cmd)
	if err != nil {
		return fmt.Errorf("invalid shell command: %w", err)
	}

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

	if a.execConfig.DryRun {
		a.ux.PrintWarning(fmt.Sprintf("[Dry Run] Would execute: %s", validCmd))
		return nil
	}

	if ok, reason := a.sandbox.ValidateCommand(validCmd); !ok {
		return fmt.Errorf("sandbox violation: %s", reason)
	}

	// FIX: execute child commands in the LIVE working directory, not the
	// sandbox root, so prior `cd` calls affect subsequent commands.
	wd, wdErr := os.Getwd()
	if wdErr != nil || wd == "" {
		wd = a.sandbox.GetCurrentDirectory()
	}
	return a.ux.RunShellCommand(validCmd, wd, a.env.Shell)
}

// executeNativeCd applies every `cd` segment to the live Helix process and
// runs any remaining chained commands through the normal safety pipeline.
func (a *Agent) executeNativeCd(original string, segments []string) error {
	a.ux.PrintCommand(original)

	var rest []string
	for _, seg := range segments {
		if !isCdCommand(seg) {
			rest = append(rest, seg)
			continue
		}
		if a.execConfig.DryRun {
			a.ux.PrintWarning(fmt.Sprintf("[Dry Run] Would change directory: %s", cdTarget(seg)))
			continue
		}
		if err := a.changeWorkingDir(cdTarget(seg)); err != nil {
			return err
		}
	}

	if len(rest) == 0 {
		return nil
	}
	return a.handleShellStep(ai.PlanStep{Tool: "shell", Command: strings.Join(rest, " && ")})
}

// changeWorkingDir moves the live Helix process, routed through the sandbox.
func (a *Agent) changeWorkingDir(target string) error {
	if target == "-" {
		return fmt.Errorf("cd - is not supported; use an explicit path")
	}
	if target == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		target = home
	}
	if strings.HasPrefix(target, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			if target == "~" {
				target = home
			} else if strings.HasPrefix(target, "~/") {
				target = filepath.Join(home, target[2:])
			}
		}
	}
	if a.sandbox != nil {
		if err := a.sandbox.ChangeDirectory(target); err != nil {
			a.ux.PrintWarning(fmt.Sprintf("cd blocked by sandbox confinement (%v). Use /sandbox off to roam freely.", err))
			return err
		}
		return nil
	}
	return os.Chdir(target)
}

func isCdCommand(seg string) bool {
	return seg == "cd" || strings.HasPrefix(seg, "cd ") || strings.HasPrefix(seg, "cd\t")
}

func cdTarget(seg string) string {
	t := strings.TrimSpace(seg)
	if t == "cd" {
		return ""
	}
	t = strings.TrimSpace(strings.TrimPrefix(t, "cd"))
	return strings.Trim(t, `"'`)
}

func splitShellChain(cmd string) []string {
	var parts []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch c {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
			cur.WriteByte(c)
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
			cur.WriteByte(c)
		case ';':
			if !inSingle && !inDouble {
				parts = append(parts, cur.String())
				cur.Reset()
			} else {
				cur.WriteByte(c)
			}
		case '&':
			if !inSingle && !inDouble && i+1 < len(cmd) && cmd[i+1] == '&' {
				parts = append(parts, cur.String())
				cur.Reset()
				i++
			} else {
				cur.WriteByte(c)
			}
		default:
			cur.WriteByte(c)
		}
	}
	parts = append(parts, cur.String())

	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, strings.TrimSpace(p))
		}
	}
	return out
}

// handleReconStep processes planner recon steps.
func (a *Agent) handleReconStep(step ai.PlanStep) error {
	toolName := strings.TrimSpace(step.Action)
	if toolName == "" {
		return fmt.Errorf("recon step missing action (tool name)")
	}

	target := strings.TrimSpace(step.Args["target"])
	if target == "" {
		return fmt.Errorf("recon step missing args.target")
	}

	if a.recon == nil {
		return fmt.Errorf("recon engine not available")
	}

	if !a.recon.IsTargetAuthorized(target) {
		a.ux.PrintError(fmt.Sprintf("Recon target %q is not authorized", target))
		a.ux.PrintWarning(fmt.Sprintf("Authorize first: /scan authorize %s --reason \"<written scope>\"", target))
		return fmt.Errorf("unauthorized recon target: %s", target)
	}

	args := make([]string, 0)
	if flags, ok := step.Args["flags"]; ok {
		args = append(args, strings.Fields(flags)...)
	}
	args = append(args, target)

	a.ux.PrintCommand(fmt.Sprintf("Recon %s %s", toolName, strings.Join(args, " ")))

	result, err := a.recon.RunTool(toolName, args...)
	if err != nil {
		return err
	}

	if result.Error != nil && strings.Contains(strings.ToLower(result.Error.Error()), "not found") {
		a.ux.PrintInfo(fmt.Sprintf("Recon tool %q is not installed.", toolName))

		if a.ux.AskYesNo(fmt.Sprintf("Install %s now?", toolName)) {
			if installErr := a.installPackage(toolName); installErr != nil {
				a.ux.PrintError(fmt.Sprintf("Installation failed: %v", installErr))
				return nil
			}

			a.ux.PrintSuccess(fmt.Sprintf("%s installed successfully — retrying scan…", toolName))

			result2, err2 := a.recon.RunTool(toolName, args...)
			if err2 != nil {
				a.ux.PrintError(fmt.Sprintf("Recon retry failed: %v", err2))
				return nil
			}

			if result2.Error != nil {
				a.ux.PrintWarning(fmt.Sprintf("Recon retry issue: %v", result2.Error))
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

		a.ux.PrintInfo(fmt.Sprintf("Skipping recon step with %s (not installed).", toolName))
		return nil
	}

	if result.Error != nil {
		a.ux.PrintWarning(fmt.Sprintf("Recon tool %q issue: %v", toolName, result.Error))
		return nil
	}

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

// installPackage attempts to install a single package using the detected system package manager.
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

// GetUX returns the UX layer.
func (a *Agent) GetUX() *ux.UX {
	return a.ux
}

// GetTypingEffect returns whether typing animation is enabled.
func (a *Agent) GetTypingEffect() bool {
	return a.typingEffect
}

// AuthorizeRecon explicitly authorizes a recon target.
func (a *Agent) AuthorizeRecon(target, reason string) {
	if a.recon == nil {
		a.ux.PrintError("Recon engine not available")
		return
	}

	a.recon.AuthorizeTarget(target, reason)
	a.ux.PrintSuccess(fmt.Sprintf("Recon target authorized: %s", target))
}

// IsReconTargetAuthorized reports whether a target is authorized.
func (a *Agent) IsReconTargetAuthorized(target string) bool {
	if a.recon == nil {
		return false
	}

	return a.recon.IsTargetAuthorized(target)
}

// ListAuthorizedReconTargets returns authorized targets and reasons.
func (a *Agent) ListAuthorizedReconTargets() map[string]string {
	if a.recon == nil {
		return map[string]string{}
	}

	return a.recon.AuthorizedTargets()
}

// isHistoryQuery reports whether a command is asking for shell history.
func isHistoryQuery(cmd string) bool {
	c := strings.TrimSpace(strings.ToLower(cmd))
	return c == "history" || strings.HasPrefix(c, "history ") ||
		c == "fc -ln -1" || c == "fc -l" || c == "!!" ||
		strings.HasPrefix(c, "fc ")
}

// executeNativeHistory reads the persistent Helix history file and prints
// the last N entries, bypassing the child-shell history isolation.
func (a *Agent) executeNativeHistory(cmd string) error {
	a.ux.PrintCommand(cmd)

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot resolve home directory")
	}

	histPath := filepath.Join(home, ".helix_history")
	lines, err := utils.LoadHistory(histPath)
	if err != nil || len(lines) == 0 {
		a.ux.PrintInfo("No command history found.")
		return nil
	}

	// Default to last 15 lines, parse limit if provided (e.g., "history 20")
	limit := 15
	parts := strings.Fields(cmd)
	if len(parts) > 1 {
		if n, err := strconv.Atoi(parts[len(parts)-1]); err == nil && n > 0 {
			limit = n
		}
	}

	start := len(lines) - limit
	if start < 0 {
		start = 0
	}

	for i := start; i < len(lines); i++ {
		fmt.Printf("%5d  %s\n", i+1, lines[i])
	}
	return nil
}
