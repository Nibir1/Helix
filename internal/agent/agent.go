package agent

import (
	"fmt"
	"strings"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/rag"
	"helix/internal/shell"
	"helix/internal/utils"

	"github.com/fatih/color"
)

// Agent is the executor that interprets planner results and performs tasks.
type Agent struct {
	env          shell.Env
	pb           *ai.PromptBuilder
	rag          *rag.RAGSystem
	sandbox      *commands.DirectorySandbox
	execConfig   commands.ExecuteConfig
	typingEffect bool
	gitManager   *commands.GitManager
}

// NewAgent creates the Agent (Executor).
func NewAgent(
	env shell.Env,
	pb *ai.PromptBuilder,
	rag *rag.RAGSystem,
	sandbox *commands.DirectorySandbox,
	execConfig commands.ExecuteConfig,
	typingEffect bool,
) *Agent {
	return &Agent{
		env:          env,
		pb:           pb,
		rag:          rag,
		sandbox:      sandbox,
		execConfig:   execConfig,
		typingEffect: typingEffect,
		gitManager:   commands.NewGitManager(env, execConfig, sandbox),
	}
}

// HandleInput is called when user types a *non-slash* message.
func (a *Agent) HandleInput(userInput string) {

	// Build planner prompt
	plannerPrompt := a.pb.BuildPlannerPrompt(userInput)

	// Get structured plan from LLM
	plan, err := PlanFromLLM(plannerPrompt)
	if err != nil {
		color.Red("❌ Planner error: %v", err)
		return
	}

	color.Cyan("🤖 Agent Intent: %s", plan.Intent)
	if len(plan.Steps) > 1 {
		color.Cyan("🔧 Steps: %d", len(plan.Steps))
	}

	// Execute steps
	for i, step := range plan.Steps {
		color.Yellow("\n--- Step %d ---", i+1)
		if err := a.executeStep(step); err != nil {
			color.Red("❌ Step failed: %v", err)
			break
		}
	}

	color.Green("\n🎉 Done.")
}

// =========================================================
// EXECUTOR — HANDLES EACH STEP
// =========================================================

func (a *Agent) executeStep(step PlannerStep) error {

	switch step.Tool {
	// -----------------------------------------------------
	// 1) RESPONSE / CHAT
	// -----------------------------------------------------
	case "response":
		msg := strings.TrimSpace(step.Message)
		if msg == "" {
			return fmt.Errorf("empty response message")
		}

		color.Cyan("💬 %s", msg)
		return nil

	// -----------------------------------------------------
	// 2) SHELL
	// -----------------------------------------------------
	case "shell":
		command := strings.TrimSpace(step.Command)
		if command == "" {
			return fmt.Errorf("planner shell step missing command")
		}

		color.Blue("🖥️ Shell: %s", command)

		// Validate & clean
		valid, err := commands.ValidateAndCleanCommand(command)
		if err != nil {
			return fmt.Errorf("shell command invalid: %v", err)
		}

		// Sandbox enforced
		return a.sandbox.WrapCommand(valid, a.execConfig, a.env)

	// -----------------------------------------------------
	// 3) GIT
	// -----------------------------------------------------
	case "git":
		action := strings.TrimSpace(step.Action)
		if action == "" {
			return fmt.Errorf("planner git step missing action")
		}

		color.Blue("🌿 Git: action=%s", action)

		// GitManager already supports natural-language handling
		return a.gitManager.HandleGitRequest(action)

	// -----------------------------------------------------
	// 4) PACKAGE MANAGER
	// -----------------------------------------------------
	case "package":
		if step.Name == "" {
			return fmt.Errorf("package step missing name")
		}
		if step.Action == "" {
			return fmt.Errorf("package step missing action")
		}

		color.Blue("📦 Package: %s %s", step.Action, step.Name)

		// Pass args as if user typed a slash command
		commands.HandlePackageCommand(
			[]string{step.Action, step.Name},
			a.env,
			false,
			a.execConfig,
		)

		return nil

	// -----------------------------------------------------
	// 5) MULTI-STEP (planner already provided array of steps)
	// -----------------------------------------------------
	case "multi_step":
		// Should not occur here; multi-step is handled in HandleInput
		return nil

	default:
		return fmt.Errorf("unknown tool type: %s", step.Tool)
	}
}

// =========================================================
// UTILITY HELPERS
// =========================================================

// PrintTypingEffect prints text with a typewriter effect (if enabled)
func (a *Agent) PrintTypingEffect(text string) {
	if !a.typingEffect {
		fmt.Println(text)
		return
	}
	utils.Typewriter(text)
}
