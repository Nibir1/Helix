package agent

import (
	"encoding/json"
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
//
// 🔁 RAG 2-pass integration:
//   - Pass 1: normal planner prompt (via PromptBuilder), may include "rag" tool steps
//   - Runtime: resolve those RAG queries via RAGSystem, build RAG_CONTEXT_JSON
//   - Pass 2: special RAG-aware planner prompt (no PromptBuilder), MUST NOT emit rag steps
//   - Execute final plan (chat/shell/git/package/multi_step only)
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
//
// With RAG 2-pass integration:
//   - First planner call may request rag tool steps
//   - We resolve those via RAGSystem.Retrieve
//   - Second planner call gets RAG_CONTEXT_JSON and must not emit rag steps
func (a *Agent) HandleInput(userInput string) {
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return
	}

	// ------------------------
	// PASS 1: Initial planning
	// ------------------------
	plannerPrompt := a.pb.BuildPlannerPrompt(userInput)

	raw, err := ai.RunModel(plannerPrompt)
	if err != nil {
		color.Red("❌ Planner model error: %v", err)
		return
	}

	plan1, err := decodePlannerResult(raw)
	if err != nil {
		color.Red("❌ Failed to parse planner output: %v", err)
		color.Yellow("🔍 Raw planner output:\n%s", strings.TrimSpace(raw))
		return
	}

	// If no RAG requested or RAG not available → just execute first plan
	if !plan1.HasRAGSteps() || a.rag == nil || !a.rag.IsInitialized() {
		a.executePlan(userInput, plan1)
		return
	}

	// ---------------------------------
	// Runtime RAG resolution (between passes)
	// ---------------------------------
	ragContextJSON := a.buildRAGContextJSON(plan1)
	if ragContextJSON == "" {
		// No usable context → fall back to first plan
		color.Yellow("⚠️ RAG context empty or unavailable, executing initial plan.")
		a.executePlan(userInput, plan1)
		return
	}

	// ------------------------
	// PASS 2: Final planning (RAG-aware)
	// ------------------------
	finalPrompt := a.buildFinalPlannerPrompt(userInput, ragContextJSON)

	finalRaw, err := ai.RunModel(finalPrompt)
	if err != nil {
		color.Red("❌ Final planner model error: %v", err)
		color.Yellow("💡 Falling back to initial plan.")
		a.executePlan(userInput, plan1)
		return
	}

	plan2, err := decodePlannerResult(finalRaw)
	if err != nil {
		color.Red("❌ Failed to parse final planner output: %v", err)
		color.Yellow("🔍 Raw final planner output:\n%s", strings.TrimSpace(finalRaw))
		color.Yellow("💡 Falling back to initial plan.")
		a.executePlan(userInput, plan1)
		return
	}

	// Final plan should now contain only chat/shell/git/package/multi_step steps
	a.executePlan(userInput, plan2)
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
		// In the RAG 2-pass design, final plans should NOT contain rag steps.
		// This handler is kept as a fallback for older behaviors.
		color.Yellow("⚠️ RAG step encountered in final execution. Using legacy RAG behavior.")
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
// In the 2-pass design this is mostly a fallback for legacy behavior.
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

// ----------------------
// RAG 2-pass helpers
// ----------------------

// HasRAGSteps reports whether the planner result contains any RAG tool steps.
func (p *PlannerResult) HasRAGSteps() bool {
	for _, s := range p.Steps {
		if strings.EqualFold(s.Tool, "rag") {
			return true
		}
	}
	return false
}

// RAGQueries returns all non-empty RAG queries from the plan.
func (p *PlannerResult) RAGQueries() []string {
	var queries []string
	for _, s := range p.Steps {
		if strings.EqualFold(s.Tool, "rag") {
			q := strings.TrimSpace(s.Query)
			if q != "" {
				queries = append(queries, q)
			}
		}
	}
	return queries
}

// buildRAGContextJSON runs RAG for each requested query and builds a compact JSON context
// that is passed back into the planner for the final pass.
//
// Shape:
//
//	{
//	  "queries": ["how to list hidden files in unix"],
//	  "commands": [
//	    {
//	      "name": "ls",
//	      "description": "...",
//	      "synopsis": "ls [OPTION]... [FILE]...",
//	      "options": ["-a", "-A", ...],
//	      "examples": ["ls -A", ...]
//	    }
//	  ]
//	}
func (a *Agent) buildRAGContextJSON(plan *PlannerResult) string {
	if a.rag == nil || !a.rag.IsInitialized() {
		return ""
	}

	type ctxCmd struct {
		Name        string   `json:"name"`
		Description string   `json:"description,omitempty"`
		Synopsis    string   `json:"synopsis,omitempty"`
		Options     []string `json:"options,omitempty"`
		Examples    []string `json:"examples,omitempty"`
	}

	type ragCtx struct {
		Queries  []string `json:"queries"`
		Commands []ctxCmd `json:"commands"`
	}

	ctx := ragCtx{
		Queries: plan.RAGQueries(),
	}

	if len(ctx.Queries) == 0 {
		return ""
	}

	seen := make(map[string]bool)

	for _, q := range ctx.Queries {
		result, err := a.rag.Retrieve(q)
		if err != nil {
			a.ux.PrintError(fmt.Sprintf("RAG lookup failed for %q: %v", q, err))
			continue
		}

		a.ux.PrintRAGRetrievalInfo(q, len(result.Commands), result.RetrievalTime)

		for _, c := range result.Commands {
			if seen[c.Name] {
				continue
			}
			seen[c.Name] = true

			ctx.Commands = append(ctx.Commands, ctxCmd{
				Name:        c.Name,
				Description: c.Description,
				Synopsis:    c.Synopsis,
				Options:     c.Options,
				Examples:    c.Examples,
			})
		}
	}

	if len(ctx.Commands) == 0 {
		return ""
	}

	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		a.ux.PrintError(fmt.Sprintf("Failed to encode RAG context JSON: %v", err))
		return ""
	}

	return string(data)
}

// buildFinalPlannerPrompt constructs the second-pass planner prompt with RAG_CONTEXT_JSON
// and updated instructions that forbid rag tool usage in the final plan.
func (a *Agent) buildFinalPlannerPrompt(userInput, ragContextJSON string) string {
	var sb strings.Builder

	sb.WriteString(ragAwarePlannerSystemPrompt)
	sb.WriteString("\n\nRAG_CONTEXT_JSON:\n")
	sb.WriteString(ragContextJSON)
	sb.WriteString("\n\nUSER_MESSAGE:\n")
	sb.WriteString(userInput)
	sb.WriteString("\n")

	return sb.String()
}

// ragAwarePlannerSystemPrompt is the master system prompt used only in the
// SECOND planner pass, after the runtime has already resolved RAG queries.
//
// Key differences from the first pass (PromptBuilder):
//   - RAG_CONTEXT_JSON is provided with commands, options, examples
//   - You MUST NOT emit any "rag" tool steps in this pass
const ragAwarePlannerSystemPrompt = `
🧠 Helix — RAG-Integrated Agent Mode (Final Pass)

You are Helix-Agent, an autonomous, safe, RAG-augmented CLI assistant.
You translate natural language into an actionable JSON plan that Helix can execute.
You produce no explanations, only structured machine-readable output.

ABSOLUTE RULES (MUST FOLLOW)
1. Output MUST be valid JSON only
   - No markdown
   - No commentary
   - No trailing text
   - No leading text
   - No code fences
   - No prose
2. You NEVER execute commands yourself
   - You only plan. The Helix runtime executes.
3. The top-level JSON MUST contain:
   - "intent" — one of:
       * "chat"
       * "shell"
       * "git"
       * "package"
       * "multi_step"
   - "steps" — array of steps the executor should run.
4. VALID STEP FORMATS (FINAL PASS)
   Chat/response step:
     {"tool": "response", "message": "text"}
   Shell command step:
     {"tool": "shell", "command": "ls -la"}
   Git action step:
     {"tool": "git", "action": "commit", "args": {"message": "msg"}}
   Package manager step:
     {"tool": "package", "action": "install", "name": "node"}

IMPORTANT: In THIS final pass you MUST NOT use the "rag" tool.
- The runtime has already resolved all RAG queries.
- You must rely on RAG_CONTEXT_JSON (provided above) plus the user message.

RAG CONTEXT USAGE
- RAG_CONTEXT_JSON contains:
   - "queries": list of documentation queries that were run
   - "commands": array of objects:
       {
         "name": "ls",
         "description": "...",
         "synopsis": "ls [OPTION]... [FILE]...",
         "options": ["-a ...", "-A ...", ...],
         "examples": ["ls -A", ...]
       }
- When generating shell/git/package steps:
   - Prefer commands and flags that appear in RAG_CONTEXT_JSON.
   - If there are multiple variants, choose the safest and most conservative.
   - Do NOT hallucinate flags that are not present in RAG_CONTEXT_JSON
     unless they are extremely standard and safe.

SAFETY RULES (NON-NEGOTIABLE)
1. NEVER generate destructive commands:
   - no "rm -rf /"
   - no wiping root folders
   - no formatting disks
   - no blind mass deletions outside the working directory
2. Always generate OS-appropriate commands.
3. ALWAYS wrap file patterns in quotes:
   - use "*.txt" not *.txt
4. NEVER hallucinate flags.
   - Use RAG_CONTEXT_JSON context to ground commands.
5. If unsure about a command → use a "response" step to ask for clarification
   instead of guessing a dangerous command.
6. RAG results should influence command correctness, but NEVER override safety.

PLANNING BEHAVIOR
Your job:
1. Understand the user intention from USER_MESSAGE and RAG_CONTEXT_JSON.
2. Generate the minimal correct steps.
3. Avoid unnecessary steps.
4. If multiple actions are needed → use "multi_step".
5. If it's just a normal conversation → use "chat" with a single "response" step.

EXAMPLE INTENT MAPPINGS
- Asking a general question:
  intent: "chat", steps: [{"tool": "response", "message": "..."}]

- Simple shell request:
  "find all .log files"
  {
    "intent": "shell",
    "steps": [
      {"tool": "shell", "command": "find . -name \"*.log\""}
    ]
  }

- Git workflow:
  "undo last commit but keep changes"
  {
    "intent": "git",
    "steps": [
      {"tool": "git", "action": "reset_soft", "args": {"target": "HEAD~1"}}
    ]
  }

- Multi-step automation:
  "update version in README and commit"
  {
    "intent": "multi_step",
    "steps": [
      {"tool": "shell", "command": "sed -i '' 's/version=.*/version=2.1.0/' README.md"},
      {"tool": "git", "action": "commit", "args": {"message": "Update version to 2.1.0"}}
    ]
  }

YOUR OUTPUT
- MUST be a single JSON object only:
  {
    "intent": "...",
    "steps": [ ... ]
  }
- No additional text, markers, or formatting.
`
