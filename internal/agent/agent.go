// internal/agent/agent.go

package agent

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/shell"
	"helix/internal/telemetry"
	"helix/internal/ux"
)

// ─────────────────────────────────────────────────────────────────────────────
// THESIS TELEMETRY INTEGRATION
// ─────────────────────────────────────────────────────────────────────────────
// This file integrates telemetry collection for thesis evaluation.
// Telemetry is enabled via HELIX_TELEMETRY=1 environment variable.
//
// Telemetry events recorded in this file:
// - Agent initialization
// - User input received
// - Planning started/completed
// - Plan parsing (success/failure)
// - Each step execution with tool selection
// - Risk level classification
// - Confirmation requests (Medium risk)
// - Execution outcomes
// ─────────────────────────────────────────────────────────────────────────────

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

	agent := &Agent{
		env:          env,
		sandbox:      sandbox,
		execConfig:   execConfig,
		gitManager:   gm,
		typingEffect: typingEffect,
		ux:           gui,
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Record agent initialization
	// ─────────────────────────────────────────────────────────────────
	tc := telemetry.GetCollector()
	if tc.IsEnabled() {
		tc.Record(
			tc.GetCurrentTaskID(),
			"agent",
			"agent_core",
			"agent_initialized",
			true,
			map[string]interface{}{
				"os":           env.OSName,
				"shell":        env.Shell,
				"sandbox_mode": sandbox.GetMode().String(),
				"cwd":          sandbox.GetCurrentDirectory(),
			},
		)
	}

	return agent
}

// HandleInput is the main entry point for Agent Mode.
// Records comprehensive telemetry for the entire processing pipeline.
func (a *Agent) HandleInput(userInput string) {
	tc := telemetry.GetCollector()

	// ─────────────────────────────────────────────────────────────────
	// CRITICAL FIX: Ensure task ID is set from environment for each input
	// This is required for the evaluation harness where each Docker run
	// processes one task
	// ─────────────────────────────────────────────────────────────────
	if taskIDStr := os.Getenv("HELIX_TASK_ID"); taskIDStr != "" {
		if id, err := strconv.Atoi(taskIDStr); err == nil {
			tc.SetCurrentTask(id)
		}
	}

	taskID := tc.GetCurrentTaskID()
	processingStart := time.Now()

	// ─────────────────────────────────────────────────────────────────
	// CRITICAL FIX: Ensure telemetry is flushed even on early returns
	// ─────────────────────────────────────────────────────────────────
	defer func() {
		if tc.IsEnabled() {
			// Save telemetry to file before exit
			if err := tc.SaveToFile(""); err != nil {
				// Silent fail in production, but log in debug
				if os.Getenv("HELIX_DEBUG") == "1" {
					fmt.Fprintf(os.Stderr, "[TELEMETRY] Save failed: %v\n", err)
				}
			}
		}
	}()

	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: User input received
	// ─────────────────────────────────────────────────────────────────
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"agent",
			"agent_core",
			"input_received",
			true,
			map[string]interface{}{
				"input_length": len(userInput),
				"input_preview": func() string {
					if len(userInput) > 100 {
						return userInput[:100] + "..."
					}
					return userInput
				}(),
			},
		)
	}

	envDesc := fmt.Sprintf(
		"OS: %s, Shell: %s, CWD: %s",
		a.env.OSName,
		a.env.Shell,
		a.sandbox.GetCurrentDirectory(),
	)

	plannerPrompt := ai.BuildPlannerPrompt(userInput, envDesc)

	// 1) Call planner model
	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Planning started
	// ─────────────────────────────────────────────────────────────────
	planningStart := time.Now()
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"planning",
			"agent_core",
			"planning_started",
			true,
			map[string]interface{}{
				"user_input":      userInput,
				"env_description": envDesc,
			},
		)
	}

	rawPlanOutput, err := ai.RunModel(plannerPrompt)

	if err != nil {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Planning failed
		// ─────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"planning",
				"agent_core",
				"planning_completed",
				false,
				map[string]interface{}{
					"error":       fmt.Sprintf("model_error: %v", err),
					"duration_ms": time.Since(planningStart).Milliseconds(),
					"fallback":    "chat_mode",
				},
			)
		}

		a.ux.PrintError(fmt.Sprintf("Planner model error: %v", err))
		resp, chatErr := ai.RunModel(userInput)
		if chatErr != nil {
			a.ux.PrintError(fmt.Sprintf("Chat fallback failed: %v", chatErr))

			// ─────────────────────────────────────────────────────────────
			// TELEMETRY: Chat fallback also failed
			// ─────────────────────────────────────────────────────────────
			if tc.IsEnabled() {
				tc.Record(
					taskID,
					"execution",
					"agent_core",
					"execution_completed",
					false,
					map[string]interface{}{
						"error":  fmt.Sprintf("chat_fallback_failed: %v", chatErr),
						"result": "total_failure",
					},
				)
			}
			return
		}

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Chat fallback succeeded
		// ─────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"execution",
				"agent_core",
				"execution_completed",
				true,
				map[string]interface{}{
					"mode":        "chat_fallback",
					"result":      "success",
					"duration_ms": time.Since(processingStart).Milliseconds(),
				},
			)
		}

		a.ux.PrintAIMessage(strings.TrimSpace(resp), a.typingEffect)
		return
	}

	rawPlanOutput = strings.TrimSpace(rawPlanOutput)

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Raw LLM output received
	// ─────────────────────────────────────────────────────────────────
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"planning",
			"agent_core",
			"llm_output_received",
			true,
			map[string]interface{}{
				"raw_output_length": len(rawPlanOutput),
				"raw_output_preview": func() string {
					if len(rawPlanOutput) > 200 {
						return rawPlanOutput[:200] + "..."
					}
					return rawPlanOutput
				}(),
			},
		)
	}

	// 2) Parse / validate plan
	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Plan parsing started
	// ─────────────────────────────────────────────────────────────────
	parseStart := time.Now()
	plan, err := ai.ParsePlanFromModelOutput(rawPlanOutput)

	if err != nil {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Plan parsing failed
		// ─────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"planning",
				"agent_core",
				"plan_parsed",
				false,
				map[string]interface{}{
					"error":       fmt.Sprintf("parse_error: %v", err),
					"duration_ms": time.Since(parseStart).Milliseconds(),
					"fallback":    "chat_mode",
					"raw_output":  rawPlanOutput,
				},
			)
		}

		a.ux.PrintWarning(fmt.Sprintf("Planner parse error: %v", err))

		resp, chatErr := ai.RunModel(userInput)
		if chatErr != nil {
			a.ux.PrintError(fmt.Sprintf("Chat fallback failed: %v", chatErr))
			return
		}

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Chat fallback succeeded after plan parse failure
		// ─────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"execution",
				"agent_core",
				"execution_completed",
				true,
				map[string]interface{}{
					"mode":        "chat_fallback_after_parse_failure",
					"parse_error": err.Error(),
					"result":      "success",
				},
			)
		}

		a.ux.PrintAIMessage(strings.TrimSpace(resp), a.typingEffect)
		return
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Plan parsed successfully
	// ─────────────────────────────────────────────────────────────────
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"planning",
			"agent_core",
			"plan_parsed",
			true,
			map[string]interface{}{
				"intent":      string(plan.Intent),
				"steps_count": len(plan.Steps),
				"duration_ms": time.Since(parseStart).Milliseconds(),
			},
		)
	}

	// 3) Safety layer enhancement
	safePlan, err := a.prepareSafePlan(userInput, plan)
	if err != nil {
		a.ux.PrintWarning(fmt.Sprintf("Safety layer error: %v", err))
		a.ux.PrintWarning("Proceeding with original plan anyway.")

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Safety layer warning
		// ─────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"safety",
				"agent_core",
				"safety_layer_warning",
				true,
				map[string]interface{}{
					"error":  err.Error(),
					"result": "proceeding_with_original_plan",
				},
			)
		}
	} else {
		plan = safePlan

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Safety layer processed plan
		// ─────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"safety",
				"agent_core",
				"safety_layer_processed",
				true,
				map[string]interface{}{
					"intent":      string(plan.Intent),
					"steps_count": len(plan.Steps),
				},
			)
		}
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Plan execution started
	// ─────────────────────────────────────────────────────────────────
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"execution",
			"agent_core",
			"plan_execution_started",
			true,
			map[string]interface{}{
				"intent":      string(plan.Intent),
				"total_steps": len(plan.Steps),
			},
		)
	}

	// 4) Execute each step
	executionResults := []map[string]interface{}{}
	for i, step := range plan.Steps {
		stepStart := time.Now()

		// Only print step header if there are multiple steps
		if len(plan.Steps) > 1 {
			a.ux.PrintSystemMessage(fmt.Sprintf("--- Step %d ---", i+1))
		}

		// ─────────────────────────────────────────────────────────────────
		// TELEMETRY: Step execution started
		// ─────────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"execution",
				"agent_core",
				"step_started",
				true,
				map[string]interface{}{
					"step_number":   i + 1,
					"total_steps":   len(plan.Steps),
					"tool_selected": step.Tool,
					"action":        step.Action,
				},
			)
		}

		var stepResult map[string]interface{}
		switch step.Tool {

		case "response":
			a.handleResponseStep(step)
			stepResult = map[string]interface{}{
				"result":  "success",
				"tool":    "response",
				"step_ms": time.Since(stepStart).Milliseconds(),
			}

		case "shell":
			if err := a.handleShellStep(step); err != nil {
				stepResult = map[string]interface{}{
					"result":  "failed",
					"tool":    "shell",
					"error":   err.Error(),
					"command": step.Command,
					"step_ms": time.Since(stepStart).Milliseconds(),
				}

				// ─────────────────────────────────────────────────────────────
				// TELEMETRY: Shell step failed
				// ─────────────────────────────────────────────────────────────
				if tc.IsEnabled() {
					tc.Record(
						taskID,
						"execution",
						"agent_core",
						"step_completed",
						false,
						stepResult,
					)
				}

				a.ux.PrintError(fmt.Sprintf("Shell step failed: %v", err))
				return
			}
			stepResult = map[string]interface{}{
				"result":  "success",
				"tool":    "shell",
				"command": step.Command,
				"step_ms": time.Since(stepStart).Milliseconds(),
			}

		case "git":
			if err := a.handleGitStep(step); err != nil {
				stepResult = map[string]interface{}{
					"result":  "failed",
					"tool":    "git",
					"action":  step.Action,
					"error":   err.Error(),
					"step_ms": time.Since(stepStart).Milliseconds(),
				}

				// ─────────────────────────────────────────────────────────────
				// TELEMETRY: Git step failed
				// ─────────────────────────────────────────────────────────────
				if tc.IsEnabled() {
					tc.Record(
						taskID,
						"execution",
						"agent_core",
						"step_completed",
						false,
						stepResult,
					)
				}

				a.ux.PrintError(fmt.Sprintf("Git step failed: %v", err))
				return
			}
			stepResult = map[string]interface{}{
				"result":  "success",
				"tool":    "git",
				"action":  step.Action,
				"step_ms": time.Since(stepStart).Milliseconds(),
			}

		case "package":
			if err := a.handlePackageStep(step); err != nil {
				stepResult = map[string]interface{}{
					"result":  "failed",
					"tool":    "package",
					"action":  step.Action,
					"error":   err.Error(),
					"step_ms": time.Since(stepStart).Milliseconds(),
				}

				// ─────────────────────────────────────────────────────────────
				// TELEMETRY: Package step failed
				// ─────────────────────────────────────────────────────────────
				if tc.IsEnabled() {
					tc.Record(
						taskID,
						"execution",
						"agent_core",
						"step_completed",
						false,
						stepResult,
					)
				}

				a.ux.PrintError(fmt.Sprintf("Package step failed: %v", err))
				return
			}
			stepResult = map[string]interface{}{
				"result":  "success",
				"tool":    "package",
				"action":  step.Action,
				"step_ms": time.Since(stepStart).Milliseconds(),
			}

		default:
			a.ux.PrintWarning(fmt.Sprintf("Unknown tool: %s", step.Tool))
			stepResult = map[string]interface{}{
				"result":  "unknown_tool",
				"tool":    step.Tool,
				"step_ms": time.Since(stepStart).Milliseconds(),
			}
		}

		// Record successful step completion
		stepResult["step_number"] = i + 1
		executionResults = append(executionResults, stepResult)

		// ─────────────────────────────────────────────────────────────────
		// TELEMETRY: Step completed successfully
		// ─────────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"execution",
				"agent_core",
				"step_completed",
				true,
				stepResult,
			)
		}
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Plan execution completed successfully
	// ─────────────────────────────────────────────────────────────────
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"execution",
			"agent_core",
			"execution_completed",
			true,
			map[string]interface{}{
				"intent":            string(plan.Intent),
				"total_steps":       len(plan.Steps),
				"successful_steps":  len(executionResults),
				"total_duration_ms": time.Since(processingStart).Milliseconds(),
			},
		)
	}
}

//
// ──────────────────────────────────────────────────────────────
// SAFETY LAYER
// ──────────────────────────────────────────────────────────────
//

func (a *Agent) prepareSafePlan(userInput string, plan *ai.Plan) (*ai.Plan, error) {
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

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

		// ─────────────────────────────────────────────────────────────────
		// TELEMETRY: Safety layer inserted git add step
		// ─────────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"safety",
				"agent_core",
				"safety_step_inserted",
				true,
				map[string]interface{}{
					"step_type":     "git_add",
					"mutated_paths": mutatedPaths,
				},
			)
		}

		a.ux.PrintSuccess(fmt.Sprintf("Safety layer inserted git add for: %s", strings.Join(mutatedPaths, " ")))
	}

	return &safe, nil
}

func extractSemanticVersion(text string) string {
	re := regexp.MustCompile(`\b\d+\.\d+\b`)
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
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

	msg := strings.TrimSpace(step.Message)
	if msg == "" {
		return
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Response step handled
	// ─────────────────────────────────────────────────────────────────
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"execution",
			"response_handler",
			"response_generated",
			true,
			map[string]interface{}{
				"message_length": len(msg),
			},
		)
	}

	a.ux.PrintAIMessage(msg, a.typingEffect)
}

// SHELL — includes risk scoring (Phase 3.5)
func (a *Agent) handleShellStep(step ai.PlanStep) error {
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

	cmd := strings.TrimSpace(step.Command)
	if cmd == "" {
		return fmt.Errorf("empty shell command")
	}

	a.ux.PrintCommand(cmd)

	// Hard safety validation
	validCmd, err := commands.ValidateAndCleanCommand(cmd)
	if err != nil {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Shell command blocked by static analysis
		// ─────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"safety",
				"shell_handler",
				"command_blocked",
				false,
				map[string]interface{}{
					"original_command": cmd,
					"error":            err.Error(),
					"intervention":     telemetry.InterventionStatic,
				},
			)
		}
		return fmt.Errorf("invalid shell command: %w", err)
	}

	// Soft risk layer
	risk, reasons := commands.AnalyzeShellRisk(validCmd)

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Risk level classified
	// ─────────────────────────────────────────────────────────────────
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"safety",
			"shell_handler",
			"risk_classified",
			true,
			map[string]interface{}{
				"command":          validCmd,
				"risk_level":       risk.String(),
				"reasons":          reasons,
				"requires_confirm": risk.RequiresConfirmation(),
			},
		)
	}

	switch risk {

	case commands.ShellRiskLow:
		// execute directly
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Low risk - direct execution
		// ─────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"execution",
				"shell_handler",
				"execution_decision",
				true,
				map[string]interface{}{
					"command":    validCmd,
					"risk_level": "Low",
					"decision":   "auto_execute",
				},
			)
		}

	case commands.ShellRiskMedium:
		a.ux.PrintWarning("Medium risk shell command:")
		for _, r := range reasons {
			a.ux.PrintWarning(fmt.Sprintf(" • %s", r))
		}

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Medium risk - awaiting user confirmation
		// ─────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"safety",
				"shell_handler",
				"awaiting_confirmation",
				true,
				map[string]interface{}{
					"command":    validCmd,
					"risk_level": "Medium",
					"reasons":    reasons,
				},
			)
		}

		// --- UPDATED INTERACTIVE CONFIRMATION ---
		if !a.ux.AskYesNo("Execute anyway?") {
			// ─────────────────────────────────────────────────────────────
			// TELEMETRY: User declined medium risk command
			// ─────────────────────────────────────────────────────────────
			if tc.IsEnabled() {
				tc.Record(
					taskID,
					"safety",
					"shell_handler",
					"user_declined",
					true,
					map[string]interface{}{
						"command":    validCmd,
						"risk_level": "Medium",
						"decision":   "declined",
					},
				)
			}
			a.ux.PrintWarning("Command skipped")
			return nil
		}

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: User confirmed medium risk command
		// ─────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"safety",
				"shell_handler",
				"user_confirmed",
				true,
				map[string]interface{}{
					"command":    validCmd,
					"risk_level": "Medium",
					"decision":   "confirmed",
				},
			)
		}

	case commands.ShellRiskHigh:
		a.ux.PrintError("HIGH RISK — blocked")
		for _, r := range reasons {
			a.ux.PrintError(fmt.Sprintf(" • %s", r))
		}

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: High risk command blocked
		// ─────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"safety",
				"shell_handler",
				"high_risk_blocked",
				false,
				map[string]interface{}{
					"command":      validCmd,
					"risk_level":   "High",
					"reasons":      reasons,
					"decision":     "blocked",
					"intervention": telemetry.InterventionRisk,
				},
			)
		}
		return fmt.Errorf("high-risk shell command blocked")
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Executing command
	// ─────────────────────────────────────────────────────────────────
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"execution",
			"shell_handler",
			"command_executing",
			true,
			map[string]interface{}{
				"command": validCmd,
			},
		)
	}

	err = a.sandbox.WrapCommand(validCmd, a.execConfig, a.env)

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Command execution result
	// ─────────────────────────────────────────────────────────────────
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"execution",
			"shell_handler",
			"command_executed",
			err == nil,
			map[string]interface{}{
				"command": validCmd,
				"success": err == nil,
				"error": func() string {
					if err != nil {
						return err.Error()
					}
					return ""
				}(),
			},
		)
	}

	return err
}

// GIT — supports safe + dangerous (Option C)
func (a *Agent) handleGitStep(step ai.PlanStep) error {
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

	action := strings.TrimSpace(step.Action)
	if action == "" {
		return fmt.Errorf("missing git action")
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Git action selected
	// ─────────────────────────────────────────────────────────────────
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"execution",
			"git_handler",
			"action_selected",
			true,
			map[string]interface{}{
				"action": action,
				"args":   step.Args,
			},
		)
	}

	a.ux.PrintCommand(fmt.Sprintf("git action: %s", action))
	err := a.gitManager.ExecutePlannedAction(action, step.Args)

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Git action result
	// ─────────────────────────────────────────────────────────────────
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"execution",
			"git_handler",
			"action_executed",
			err == nil,
			map[string]interface{}{
				"action":  action,
				"args":    step.Args,
				"success": err == nil,
				"error": func() string {
					if err != nil {
						return err.Error()
					}
					return ""
				}(),
			},
		)
	}

	return err
}

// PACKAGE MANAGER
func (a *Agent) handlePackageStep(step ai.PlanStep) error {
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

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
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Package action blocked by safety check
		// ─────────────────────────────────────────────────────────────
		if tc.IsEnabled() {
			tc.Record(
				taskID,
				"safety",
				"package_handler",
				"safety_violation",
				false,
				map[string]interface{}{
					"action":  action,
					"package": name,
					"error":   err.Error(),
				},
			)
		}
		a.ux.PrintError(fmt.Sprintf("Package safety violation: %v", err))
		return err
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Package action selected
	// ─────────────────────────────────────────────────────────────────
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"execution",
			"package_handler",
			"action_selected",
			true,
			map[string]interface{}{
				"action":  action,
				"package": name,
			},
		)
	}

	a.ux.PrintCommand(fmt.Sprintf("Package: %s %s", action, name))

	commands.HandlePackageCommand(
		[]string{action, name},
		a.env,
		false,
		a.execConfig,
	)

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Package action completed
	// ─────────────────────────────────────────────────────────────────
	if tc.IsEnabled() {
		tc.Record(
			taskID,
			"execution",
			"package_handler",
			"action_completed",
			true,
			map[string]interface{}{
				"action":  action,
				"package": name,
			},
		)
	}

	return nil
}
