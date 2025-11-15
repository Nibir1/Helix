package agent

import (
	"fmt"
	"strings"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/rag"
	"helix/internal/shell"
	"helix/internal/ux"

	"github.com/fatih/color"
)

// Agent is the core "Agent Mode" orchestrator.
// It uses the planner (LLM) to turn a user request into a structured plan,
// then executes each step with the appropriate tools.
type Agent struct {
	env          shell.Env
	pb           *ai.PromptBuilder
	rag          *rag.RAGSystem
	sandbox      *commands.DirectorySandbox
	execConfig   commands.ExecuteConfig
	typingEffect bool
	gitManager   *commands.GitManager
	ux           *ux.UX
}

// NewAgent creates a new Agent instance.
// Note: We create our own GitManager here so main.go doesn't have to pass it in.
func NewAgent(
	env shell.Env,
	pb *ai.PromptBuilder,
	ragSystem *rag.RAGSystem,
	sandbox *commands.DirectorySandbox,
	execConfig commands.ExecuteConfig,
	typingEffect bool,
) *Agent {
	gm := commands.NewGitManager(env, execConfig, sandbox)

	return &Agent{
		env:          env,
		pb:           pb,
		rag:          ragSystem,
		sandbox:      sandbox,
		execConfig:   execConfig,
		typingEffect: typingEffect,
		gitManager:   gm,
		ux:           ux.NewUX(),
	}
}

// HandleInput is the main entry point for Agent Mode.
// It takes raw user text (no slash prefix), asks the planner LLM for a JSON plan,
// then executes the resulting steps.
func (a *Agent) HandleInput(userInput string) {
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return
	}

	// 1) Ask the planner LLM to build a JSON plan
	plannerPrompt := a.pb.BuildPlannerPrompt(userInput)

	raw, err := ai.RunModel(plannerPrompt)
	if err != nil {
		color.Red("❌ Planner model error: %v", err)
		return
	}

	// 2) Decode planner JSON into a PlannerResult
	plan, err := decodePlannerResult(raw)
	if err != nil {
		color.Red("❌ Failed to parse planner output: %v", err)
		color.Yellow("🔍 Raw planner output:\n%s", strings.TrimSpace(raw))
		return
	}

	// 3) Execute the plan
	a.executePlan(userInput, plan)
}

// executePlan runs each step in the planner result.
func (a *Agent) executePlan(userInput string, plan *PlannerResult) {
	if plan == nil {
		color.Red("❌ No plan returned from planner")
		return
	}

	color.Cyan("🤖 Agent Intent: %s", string(plan.Intent))
	color.Cyan("🔧 Steps: %d\n", len(plan.Steps))

	for i, step := range plan.Steps {
		color.Cyan("\n--- Step %d ---", i+1)
		if err := a.executeStep(step); err != nil {
			color.Red("❌ Step failed: %v", err)
			break
		}
	}

	color.Green("\n🎉 Done.")
}

// executeStep dispatches a single planner step to the appropriate tool.
func (a *Agent) executeStep(step PlannerStep) error {
	switch step.Tool {
	case "response":
		return a.handleResponseStep(step)

	case "shell":
		return a.handleShellStep(step)

	case "git":
		return a.handleGitStep(step)

	case "package":
		return a.handlePackageStep(step)

	case "rag":
		return a.handleRAGStep(step)

	default:
		// Unknown tool → fallback to a simple message so the user is not left hanging
		msg := strings.TrimSpace(step.Message)
		if msg == "" {
			msg = "I don't know how to handle this step."
		}
		fmt.Println(msg)
		return nil
	}
}

// ----------------------
// Tool Handlers
// ----------------------

// handleResponseStep simply outputs the message to the user.
func (a *Agent) handleResponseStep(step PlannerStep) error {
	msg := strings.TrimSpace(step.Message)
	if msg == "" {
		return nil
	}

	if a.typingEffect {
		a.ux.Typewriter(msg)
	} else {
		fmt.Println(msg)
	}

	return nil
}

// handleShellStep validates and runs a shell command step.
func (a *Agent) handleShellStep(step PlannerStep) error {
	cmd := strings.TrimSpace(step.Command)
	if cmd == "" {
		return fmt.Errorf("empty shell command in plan")
	}

	color.Cyan("🖥️ Shell: %s", cmd)

	// Use the existing validation + safety pipeline
	valid, err := commands.ValidateAndCleanCommand(cmd)
	if err != nil {
		return fmt.Errorf("invalid shell command: %w", err)
	}

	// Respect sandbox & execConfig
	if err := a.sandbox.WrapCommand(valid, a.execConfig, a.env); err != nil {
		return fmt.Errorf("command execution failed: %w", err)
	}

	return nil
}

// handleGitStep processes a git action step.
func (a *Agent) handleGitStep(step PlannerStep) error {
	action := strings.TrimSpace(step.Action)
	if action == "" {
		return fmt.Errorf("missing git action in plan")
	}

	color.Cyan("🌿 Git: action=%s", action)

	// For now we convert structured git intent back into a natural language
	// description that GitManager already knows how to handle.
	request := action

	// If we have structured args (like a commit message), inject that into the request
	if len(step.Args) > 0 {
		if msg, ok := step.Args["message"].(string); ok && msg != "" {
			request = fmt.Sprintf("%s with message %q", action, msg)
		}
		if target, ok := step.Args["target"].(string); ok && target != "" {
			request = fmt.Sprintf("%s %s", request, target)
		}
	}

	return a.gitManager.HandleGitRequest(request)
}

// handlePackageStep processes a package management step.
func (a *Agent) handlePackageStep(step PlannerStep) error {
	action := strings.TrimSpace(step.Action)
	name := strings.TrimSpace(step.Name)
	if action == "" || name == "" {
		return fmt.Errorf("package step requires both action and name")
	}

	color.Cyan("📦 Package: %s %s", action, name)

	// Reuse existing package manager handler:
	// HandlePackageCommand(args []string, env shell.Env, mockMode bool, execConfig ExecuteConfig)
	args := []string{action, name}
	commands.HandlePackageCommand(args, a.env, false, a.execConfig)

	// HandlePackageCommand itself prints and optionally executes the command;
	// we don't need to return error unless we want deep plumbing. For now, assume success.
	return nil
}

// handleRAGStep processes a RAG (Retrieval-Augmented Generation) step.
func (a *Agent) handleRAGStep(step PlannerStep) error {
	if a.rag == nil || !a.rag.IsInitialized() {
		a.ux.PrintWarning("RAG system is not initialized yet — falling back to normal answer.")
		// Optionally: turn this into a plain chat answer instead.
		return nil
	}

	query := step.Query
	if query == "" {
		// Graceful fallback: use message or command if planner messed up
		if step.Message != "" {
			query = step.Message
		} else if step.Command != "" {
			query = step.Command
		}
	}
	if query == "" {
		return fmt.Errorf("rag step has no query")
	}

	// Use RAG retrieval
	result, err := a.rag.Retrieve(query)
	if err != nil {
		a.ux.PrintError(fmt.Sprintf("RAG lookup failed: %v", err))
		return nil
	}

	a.ux.PrintRAGRetrievalInfo(query, len(result.Commands), result.RetrievalTime)

	if len(result.Commands) == 0 {
		a.ux.PrintInfo("No command documentation found for this query.")
		return nil
	}

	// For each relevant command, show a detailed explanation
	for _, cmd := range result.Commands {
		expl, err := a.rag.ExplainCommand(cmd.Name)
		if err != nil {
			// Fallback: show just name + description
			a.ux.ShowCommandExplanation(cmd.Name, cmd.Description)
			continue
		}
		a.ux.ShowCommandExplanation(cmd.Name, expl)
	}

	return nil
}
