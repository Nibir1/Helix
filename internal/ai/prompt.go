package ai

import (
	"fmt"
	"regexp"
	"strings"

	"helix/internal/rag"
	"helix/internal/shell"

	"github.com/fatih/color"
)

// PromptBuilder integrates RAG capabilities with prompt generation
type PromptBuilder struct {
	env    shell.Env
	online bool
	rag    *rag.RAGSystem
}

// NewPromptBuilder creates a new prompt builder with RAG capabilities (no RAG system wired)
func NewPromptBuilder(env shell.Env, online bool) *PromptBuilder {
	return &PromptBuilder{
		env:    env,
		online: online,
	}
}

// NewEnhancedPromptBuilder creates a prompt builder with RAG integration
func NewEnhancedPromptBuilder(env shell.Env, online bool, ragSystem *rag.RAGSystem) *PromptBuilder {
	return &PromptBuilder{
		env:    env,
		online: online,
		rag:    ragSystem,
	}
}

// IsRAGAvailable dynamically checks if RAG system is available and initialized
func (pb *PromptBuilder) IsRAGAvailable() bool {
	return pb.rag != nil && pb.rag.IsInitialized()
}

// =========================
// COMMAND GENERATION
// =========================

// BuildCommandPrompt creates a command prompt - With optional RAG context
func (pb *PromptBuilder) BuildCommandPrompt(userInput string) string {
	originalPrompt := pb.buildOriginalCommandPrompt(userInput)

	// ADD DEBUG OUTPUT
	color.Yellow("🔍 DEBUG: RAG available: %v, RAG initialized: %v", pb.rag != nil, pb.rag != nil && pb.rag.IsInitialized())

	if !pb.IsRAGAvailable() {
		color.Yellow("🔍 DEBUG: Using standard prompt (RAG not enabled)")
		return originalPrompt
	}

	enhancedPrompt := pb.rag.EnhancePrompt(userInput, originalPrompt)

	// Check if RAG actually provided useful context
	if enhancedPrompt != originalPrompt {
		// Count how many commands were actually added
		ragSection := strings.Split(enhancedPrompt, "ORIGINAL PROMPT:")[0]
		commandCount := strings.Count(ragSection, "COMMAND ")

		if commandCount > 0 {
			color.Cyan("🎯 RAG-enhanced prompt generated with %d relevant commands", commandCount)
			color.Yellow("🔍 DEBUG: Enhanced prompt length: %d chars", len(enhancedPrompt))
			return enhancedPrompt
		}

		color.Yellow("💡 RAG found no relevant commands, using standard prompt")
		return originalPrompt
	}

	color.Yellow("💡 No relevant command context found, using standard prompt")
	return originalPrompt
}

// =========================
// ASK / Q&A
// =========================

// BuildAskPrompt creates an ask prompt with optional RAG context
func (pb *PromptBuilder) BuildAskPrompt(userInput string) string {
	originalPrompt := pb.buildOriginalAskPrompt(userInput)

	if !pb.IsRAGAvailable() {
		return originalPrompt
	}

	// Only enhance if the question is about commands
	if pb.isCommandRelatedQuestion(userInput) {
		enhancedPrompt := pb.rag.EnhancePrompt(userInput, originalPrompt)

		if enhancedPrompt != originalPrompt {
			color.Cyan("🎯 RAG-enhanced Q&A with command documentation")
			return enhancedPrompt
		}
	}

	return originalPrompt
}

// BuildEnhancedAskPrompt creates an enhanced ask prompt (compatibility)
func (pb *PromptBuilder) BuildEnhancedAskPrompt(userInput string) string {
	status := "offline"
	if pb.online {
		status = "online"
	}

	return fmt.Sprintf(`You are Helix, an AI assistant in a command-line interface. Answer the user's question directly and helpfully.

Context:
- You are running in a CLI environment
- Status: %s
- User's shell: %s on %s

User question: %s

Provide a clear, direct answer. If you don't know something or are offline, be honest about limitations.`,
		status, pb.env.Shell, pb.env.OSName, userInput)
}

// =========================
// EXPLAIN
// =========================

// BuildExplainPrompt creates an explain prompt with RAG context when available
func (pb *PromptBuilder) BuildExplainPrompt(command string) string {
	originalPrompt := pb.buildOriginalExplainPrompt(command)

	if !pb.IsRAGAvailable() {
		return originalPrompt
	}

	// Try to get RAG-based explanation first
	if ragExplanation, err := pb.rag.ExplainCommand(command); err == nil {
		color.Cyan("🎯 Using RAG-powered command explanation")
		return ragExplanation
	}

	// Fall back to AI explanation
	color.Yellow("💡 No RAG data for command, using AI explanation")
	return originalPrompt
}

// =========================
// PACKAGE MANAGEMENT
// =========================

// BuildPackagePrompt creates package management prompts
func (pb *PromptBuilder) BuildPackagePrompt(packageName, action string) string {
	actions := map[string]string{
		"install": "install",
		"update":  "update to the latest version",
		"remove":  "remove",
	}

	verb := actions[action]
	if verb == "" {
		verb = action
	}

	return fmt.Sprintf(`Provide the shell command to %s package "%s" on %s using the appropriate package manager.

Rules:
- Output ONLY the command
- Use the most common package manager for %s
- Include sudo if typically required

Command:`, verb, packageName, pb.env.OSName, pb.env.OSName)
}

// =========================
// RAG UTILITIES
// =========================

// GetCommandSuggestions gets RAG-based command suggestions
func (pb *PromptBuilder) GetCommandSuggestions(userInput string) ([]rag.CommandSuggestion, error) {
	if !pb.IsRAGAvailable() {
		return []rag.CommandSuggestion{}, nil
	}

	return pb.rag.GetCommandSuggestions(userInput)
}

// EnableRAG enables RAG functionality (kept for compatibility)
func (pb *PromptBuilder) EnableRAG(ragSystem *rag.RAGSystem) {
	pb.rag = ragSystem
}

// =========================
// ORIGINAL PROMPT BUILDERS
// =========================

func (pb *PromptBuilder) buildOriginalCommandPrompt(userInput string) string {
	return fmt.Sprintf(`You are Helix, an advanced CLI assistant. Convert the user's natural language request into a single, safe, fully executable shell command for %s (%s).

STRICT RULES – FOLLOW EXACTLY:
1. Output ONLY the raw shell command with no explanations, notes, or formatting
2. Never include backticks, code blocks, or extra punctuation
3. Do NOT prepend or append any text
4. Always produce a safe command; avoid destructive operations like rm -rf or anything that modifies critical system files
5. Use the correct package manager or system tool for the OS
6. Keep the command concise, efficient, and fully executable
7. Ensure all quotes are properly matched and escaped, including within wildcards
8. Use quotes for all file patterns and paths (e.g., '*.go' or '/path/to/file')
9. Do NOT use unquoted wildcards that could expand unexpectedly
10. Never add trailing semicolons, parentheses, or invalid characters
11. If multiple commands are needed, combine them safely with && only
12. Ensure the command works correctly in a real shell before outputting

User request: %s

Command:`, pb.env.OSName, pb.env.Shell, userInput)
}

func (pb *PromptBuilder) buildOriginalAskPrompt(userInput string) string {
	status := "offline"
	if pb.online {
		status = "online"
	}

	return fmt.Sprintf(`You are Helix, a helpful CLI assistant. The user is asking a question.

IMPORTANT: Provide a direct, helpful response to the user's question. Do not ask questions back. Do not be meta. Just answer helpfully.

Current status: %s
User question: %s

Provide a concise, helpful answer:`, status, userInput)
}

func (pb *PromptBuilder) buildOriginalExplainPrompt(command string) string {
	return fmt.Sprintf(`Explain what this shell command does in simple, clear terms: "%s"

IMPORTANT RULES:
1. Provide a clear explanation of what the command does
2. Keep it under 3 sentences
3. Focus on the main purpose and potential risks
4. Do not ask questions back
5. Do not be meta - just explain the command
6. If you don't know, say you're not sure

Explanation:`, command)
}

// =========================
// HELPER METHODS
// =========================

// isCommandRelatedQuestion checks if a question is about commands
func (pb *PromptBuilder) isCommandRelatedQuestion(question string) bool {
	question = strings.ToLower(question)

	commandKeywords := []string{
		"command", "how to", "what is", "what does", "explain", "meaning of",
		"usage of", "how do i", "how can i", "what's the", "what are",
		"difference between", "vs ", " versus ", "alternative to", "replace",
		"equivalent of", "similar to",
	}

	for _, keyword := range commandKeywords {
		if strings.Contains(question, keyword) {
			return true
		}
	}

	return false
}

// ExtractCommand cleans AI output to get just the command
func ExtractCommand(aiOutput string) string {
	// Remove all code blocks and backticks
	aiOutput = strings.ReplaceAll(aiOutput, "```bash", "")
	aiOutput = strings.ReplaceAll(aiOutput, "```sh", "")
	aiOutput = strings.ReplaceAll(aiOutput, "```", "")

	// Remove backticks from the entire output
	aiOutput = strings.ReplaceAll(aiOutput, "`", "")

	// Remove any markdown formatting
	aiOutput = strings.ReplaceAll(aiOutput, "**", "")

	// Take only the first non-comment, non-empty line
	lines := strings.Split(aiOutput, "\n")
	var command string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "#") {
			command = line
			break
		}
	}

	// Remove leading/trailing quotes
	command = strings.Trim(command, `"'`)

	// Look for typical command patterns (best-effort)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^[a-zA-Z0-9_\-\./]+\s+`), // Starts with command
		regexp.MustCompile(`^[a-z]+\s+`),             // Starts with lowercase word
	}

	for _, pattern := range patterns {
		if match := pattern.FindString(command); match != "" {
			command = strings.TrimSpace(command)
			break
		}
	}

	return strings.TrimSpace(command)
}

// =========================
// AGENT PLANNER PROMPT
// =========================

// BuildPlannerPrompt generates the planning prompt for the agent.
// This is used when the user enters a message without a slash command.
// The LLM MUST output a JSON plan with intent + steps matching PlannerResult / PlannerStep.
func (pb *PromptBuilder) BuildPlannerPrompt(userInput string) string {
	status := "offline"
	if pb.online {
		status = "online"
	}

	ragLine := "You also have background knowledge of system commands and manual pages (RAG)."
	if !pb.IsRAGAvailable() {
		ragLine = "RAG command docs may not be available yet; still plan as best you can."
	}

	return fmt.Sprintf(`
You are Helix-Agent, an intelligent CLI automation planner inside a terminal.

Your ONLY job is to create a machine-readable plan describing what to do.
You DO NOT execute commands yourself — another component will do that.

===========================================
SYSTEM CONTEXT
- OS: %s
- Shell: %s
- Connectivity: %s
- %s
===========================================

IMPORTANT RULES
1. Output MUST be VALID JSON ONLY — no markdown, no prose, no comments.
2. The top-level JSON object MUST match this schema exactly:

{
  "intent": "chat" | "shell" | "git" | "package" | "multi_step",
  "steps": [
    {
      "tool": "response" | "shell" | "git" | "package",
      "command": string,            // required for tool="shell"
      "action": string,             // required for tool="git" or "package"
      "name": string,               // package name, tag name, etc. (for tool="package" or git tags)
      "args": { ... },              // optional structured arguments, e.g. {"message": "...", "target": "HEAD~1"}
      "message": string             // required for tool="response"
    }
  ]
}

3. TOOL semantics:
   - tool="response": executor will print "message" as plain text to the user.
   - tool="shell": executor will run "command" in the user's shell (with safety checks).
   - tool="git": executor will call a git helper with "action" and optional "args".
   - tool="package": executor will call a package manager helper with "action" and "name".
4. Choose "intent":
   - "chat": purely conversational, no tools, just a response step.
   - "shell": one or more shell commands (e.g. list files, move files, search logs).
   - "git": git-specific operations (commit, add, reset, stash, etc.).
   - "package": install/update/remove packages.
   - "multi_step": any workflow that needs multiple actions or mixed tools.
5. For "multi_step", decompose the user request into a small sequence of safe steps.
6. NEVER invent obviously destructive commands (like "rm -rf /").
7. Prefer simple, robust commands over clever one-liners.
8. When unsure if you should respond with text or commands, prefer using tools if they can achieve what the user asked.

EXAMPLES

Example 1 (Chat)
User: "why is the sky blue?"
{
  "intent": "chat",
  "steps": [
    {
      "tool": "response",
      "message": "The sky appears blue because of Rayleigh scattering: shorter blue wavelengths are scattered more strongly by air molecules than other colors."
    }
  ]
}

Example 2 (Shell, single-step)
User: "list all .txt files"
{
  "intent": "shell",
  "steps": [
    {
      "tool": "shell",
      "command": "ls -1 *.txt"
    }
  ]
}

Example 3 (Git, single-step)
User: "undo last commit but keep changes"
{
  "intent": "git",
  "steps": [
    {
      "tool": "git",
      "action": "reset_soft",
      "args": { "target": "HEAD~1" }
    }
  ]
}

Example 4 (Multi-step, mixed tools)
User: "bump version to 2.1.0 in README and commit it with a tag"
{
  "intent": "multi_step",
  "steps": [
    {
      "tool": "shell",
      "command": "sed -i '' 's/version = .*/version = \"2.1.0\"/' README.md"
    },
    {
      "tool": "git",
      "action": "commit",
      "args": { "message": "Bump README version to 2.1.0" }
    },
    {
      "tool": "git",
      "action": "tag",
      "args": { "name": "v2.1.0" }
    }
  ]
}

Example 5 (Package)
User: "install git"
{
  "intent": "package",
  "steps": [
    {
      "tool": "package",
      "action": "install",
      "name": "git"
    }
  ]
}

===========================================
USER MESSAGE:
"%s"

Now plan the best possible intent and steps.
Return ONLY a single JSON object that matches the schema above. No markdown, no backticks, no extra text.`,
		pb.env.OSName, pb.env.Shell, status, ragLine, userInput)
}
