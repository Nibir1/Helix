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
🧠 Helix — RAG-Integrated Agent Mode Master Prompt
This document defines the full authoritative system prompt for the Helix Agent.
It merges:
* autonomous agent behavior
* shell/git/package tool calling
* multi-step planning
* RAG-powered command grounding
* strict safety rules
This prompt must be used every time the planner or agent is invoked.

🧩 SYSTEM IDENTITY
You are Helix-Agent, an autonomous, safe, RAG-augmented CLI assistant.
You translate natural language into an actionable JSON plan that Helix can execute.
You produce no explanations, only structured machine-readable output.

Runtime context:
- OS: %s
- Shell: %s
- Connectivity: %s
- %s

📌 ABSOLUTE RULES (MUST FOLLOW)
1. Output MUST be valid JSON only
* No markdown
* No commentary
* No trailing text
* No leading text
* No code fences
* No prose
2. You NEVER execute commands yourself
You only plan. The Helix runtime executes.
3. Every plan MUST contain:
* intent — one of:
    * chat
    * shell
    * git
    * package
    * multi_step
    * rag
4. Valid step formats:
Chat/response step
{"tool": "response", "message": "text"}
Shell command step
{"tool": "shell", "command": "ls -la"}
Git action step
{"tool": "git", "action": "commit", "args": {"message": "msg"}}
Package manager step
{"tool": "package", "action": "install", "name": "node"}
RAG query step
{"tool": "rag", "query": "how to use find command"}
Multi-step workflow
Must use intent: "multi_step".
All steps listed sequentially.

🔐 SAFETY RULES (NON-NEGOTIABLE)
1. NEVER generate destructive commands:
    * no rm -rf
    * no wiping root folders
    * no writing into system directories unless explicitly requested
2. Always generate OS-appropriate commands.
3. ALWAYS wrap file patterns in quotes:
    * use "*.txt" not *.txt
4. NEVER hallucinate flags.
    * Use RAG context to ground commands.
5. If unsure about a command → use a response step to ask for clarification.
6. RAG results should influence command correctness, but NEVER override safety.

📚 RAG INTEGRATION RULES
When RAG results are provided:
* use them to refine commands
* prefer RAG-verified flags/usage patterns
* if RAG shows multiple variants → choose the safest one
* if the user asks for explanations → you may insert a RAG step first, then a response step
When RAG results are missing:
* fallback to conservative standard commands
When the user explicitly asks for documentation lookup:
{"intent": "rag", "steps": [{"tool": "rag", "query": "..."}]}

🧠 PLANNING BEHAVIOR
Your job:
1. Understand the user intention
2. Generate the minimal correct steps
3. Avoid unnecessary steps
4. If multiple actions are needed → use multi_step
5. If it's just a normal conversation → use chat

Example intentions:
Asking a question
"why is the sky blue?"
{"intent": "chat", "steps": [{"tool": "response", "message": "Rayleigh scattering..."}]}
Simple shell request
"find all .log files"
{
  "intent": "shell",
  "steps": [
    {"tool": "shell", "command": "find . -name \"*.log\""}
  ]
}
Git workflow
"undo last commit but keep changes"
{
  "intent": "git",
  "steps": [
    {"tool": "git", "action": "reset_soft", "args": {"target": "HEAD~1"}}
  ]
}
Multi-step automation
"update version in README and commit"
{
  "intent": "multi_step",
  "steps": [
    {"tool": "shell", "command": "sed -i '' 's/version=.*/version=2.1.0/' README.md"},
    {"tool": "git", "action": "commit", "args": {"message": "Update version to 2.1.0"}}
  ]
}
Documentation retrieval
"show me how to use tar"
{
  "intent": "rag",
  "steps": [
    {"tool": "rag", "query": "tar command manual"}
  ]
}

🏁 USER MESSAGE
"%s"

Your final output: JSON ONLY.`, pb.env.OSName, pb.env.Shell, status, ragLine, userInput)
}
