// prompt.go
package main

import (
	"fmt"
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

// BuildCommandPrompt creates a prompt for command generation
func (pb *PromptBuilder) BuildCommandPrompt(userInput string) string {
	return fmt.Sprintf(`You are Helix, an intelligent CLI assistant. Convert the user's natural language request into a single, safe shell command for %s (%s).

Follow these rules:
1. Output ONLY the command without explanations
2. Make it safe and avoid destructive operations
3. Use appropriate package managers for the OS
4. Keep it concise and efficient

User: %s

Command:`, pb.env.OSName, pb.env.Shell, userInput)
}

// BuildAskPrompt creates a prompt for general questions
func (pb *PromptBuilder) BuildAskPrompt(userInput string) string {
	status := "offline"
	if pb.online {
		status = "online"
	}

	return fmt.Sprintf(`You are Helix, a helpful CLI assistant. You are currently %s.

User question: %s

Provide a concise, helpful response. If you're offline, mention that your knowledge is limited and you cannot access real-time information.`, status, userInput)
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
	// Remove code blocks if present
	aiOutput = strings.ReplaceAll(aiOutput, "```bash", "")
	aiOutput = strings.ReplaceAll(aiOutput, "```sh", "")
	aiOutput = strings.ReplaceAll(aiOutput, "```", "")

	// Take only the first line (in case AI adds explanations)
	lines := strings.Split(aiOutput, "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return strings.TrimSpace(aiOutput)
}
