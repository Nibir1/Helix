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
	// REMOVED: useRAG bool - now we check dynamically
}

// NewPromptBuilder creates a new prompt builder with RAG capabilities
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
		// REMOVED: useRAG field - we check dynamically via IsRAGAvailable()
	}
}

// IsRAGAvailable dynamically checks if RAG system is available and initialized
func (pb *PromptBuilder) IsRAGAvailable() bool {
	return pb.rag != nil && pb.rag.IsInitialized()
}

// BuildCommandPrompt creates a command prompt - With optional RAG context
func (pb *PromptBuilder) BuildCommandPrompt(userInput string) string {
	originalPrompt := pb.buildOriginalCommandPrompt(userInput)

	// ADD DEBUG OUTPUT
	color.Yellow("🔍 DEBUG: RAG available: %v, RAG initialized: %v", pb.rag != nil, pb.rag != nil && pb.rag.IsInitialized())

	// Use dynamic checking instead of static flag
	if !pb.IsRAGAvailable() {
		color.Yellow("🔍 DEBUG: Using standard prompt (RAG not enabled)")
		return originalPrompt
	}

	enhancedPrompt := pb.rag.EnhancePrompt(userInput, originalPrompt)

	// NEW: Check if RAG actually provided useful context
	if enhancedPrompt != originalPrompt {
		// Count how many commands were actually added
		ragSection := strings.Split(enhancedPrompt, "ORIGINAL PROMPT:")[0]
		commandCount := strings.Count(ragSection, "COMMAND ")

		if commandCount > 0 {
			color.Cyan("🎯 RAG-enhanced prompt generated with %d relevant commands", commandCount)
			color.Yellow("🔍 DEBUG: Enhanced prompt length: %d chars", len(enhancedPrompt))
			return enhancedPrompt
		} else {
			color.Yellow("💡 RAG found no relevant commands, using standard prompt")
			return originalPrompt
		}
	} else {
		color.Yellow("💡 No relevant command context found, using standard prompt")
		return originalPrompt
	}
}

// BuildAskPrompt creates an ask prompt with optional RAG context
func (pb *PromptBuilder) BuildAskPrompt(userInput string) string {
	originalPrompt := pb.buildOriginalAskPrompt(userInput)

	// Use dynamic checking
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

// BuildExplainPrompt creates an explain prompt with RAG context when available
func (pb *PromptBuilder) BuildExplainPrompt(command string) string {
	originalPrompt := pb.buildOriginalExplainPrompt(command)

	// Use dynamic checking
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

// BuildPackagePrompt creates package management prompts (unchanged)
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

// GetCommandSuggestions gets RAG-based command suggestions (new method)
func (pb *PromptBuilder) GetCommandSuggestions(userInput string) ([]rag.CommandSuggestion, error) {
	// Use dynamic checking
	if !pb.IsRAGAvailable() {
		return []rag.CommandSuggestion{}, nil
	}

	return pb.rag.GetCommandSuggestions(userInput)
}

// EnableRAG enables RAG functionality (kept for compatibility, but now does nothing special)
func (pb *PromptBuilder) EnableRAG(ragSystem *rag.RAGSystem) {
	pb.rag = ragSystem
	// No need to set useRAG flag anymore since we check dynamically
}

// ========== ORIGINAL PROMPT BUILDERS (PRIVATE) ==========

// buildOriginalCommandPrompt is the original command prompt builder
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

// buildOriginalAskPrompt is the original ask prompt builder
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

// buildOriginalExplainPrompt is the original explain prompt builder
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

// ========== HELPER METHODS ==========

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

	// Take only the first line (in case AI adds explanations)
	lines := strings.Split(aiOutput, "\n")
	var command string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "#") {
			command = line
			break
		}
	}

	// Remove any leading/trailing quotes
	command = strings.Trim(command, `"'`)

	// Final cleanup - remove any non-command text
	// Look for the first occurrence of common command patterns
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
