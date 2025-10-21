// prompt.go
package main

import (
	"fmt"
	"regexp"
	"strings"
)

// PromptBuilder constructs optimized prompts for different modes
type PromptBuilder struct {
	env    Env
	online bool
}

// NewPromptBuilder creates a new prompt builder
func NewPromptBuilder(env Env, online bool) *PromptBuilder {
	return &PromptBuilder{
		env:    env,
		online: online,
	}
}

// Enhanced BuildCommandPrompt to get cleaner output
func (pb *PromptBuilder) BuildCommandPrompt(userInput string) string {
	return fmt.Sprintf(`You are Helix, an intelligent CLI assistant. Convert the user's natural language request into a single, safe shell command for %s (%s).

CRITICAL RULES:
1. Output ONLY the raw command without any explanations, backticks, or formatting
2. Do NOT use markdown code blocks
3. Do NOT include backticks
4. Do NOT add any text before or after the command
5. Make it safe and avoid destructive operations
6. Use appropriate package managers for the OS
7. Keep it concise and efficient

User: %s

Command:`, pb.env.OSName, pb.env.Shell, userInput)
}

// BuildAskPrompt creates a prompt for general questions
func (pb *PromptBuilder) BuildAskPrompt(userInput string) string {
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

// BuildEnhancedAskPrompt for better responses
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

// BuildExplainPrompt creates a prompt to explain commands
func (pb *PromptBuilder) BuildExplainPrompt(command string) string {
	return fmt.Sprintf(`Explain what this shell command does in simple terms: "%s"

Keep the explanation under 3 sentences and focus on the main purpose and potential risks.`, command)
}

// BuildPackagePrompt creates a prompt for package management
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
