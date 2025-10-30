package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/shell"
	"helix/internal/utils"
	"helix/internal/ux"

	"github.com/fatih/color"
)

// Helper functions for mock mode
func generateMockCommand(request string, env shell.Env) string {
	request = strings.ToLower(request)

	switch {
	case strings.Contains(request, "list") && strings.Contains(request, "file"):
		if env.IsUnixLike() {
			return "ls -la"
		} else {
			return "dir"
		}
	case strings.Contains(request, "current directory"):
		if env.IsUnixLike() {
			return "pwd"
		} else {
			return "cd"
		}
	case strings.Contains(request, "disk space"):
		if env.IsUnixLike() {
			return "df -h"
		} else {
			return "wmic logicaldisk get size,freespace,caption"
		}
	case strings.Contains(request, "process"):
		if env.IsUnixLike() {
			return "ps aux"
		} else {
			return "tasklist"
		}
	default:
		return "echo 'Mock command for: " + request + "'"
	}
}

// Generate a mock response for a question
func generateMockResponse(question string) string {
	question = strings.ToLower(question)

	switch {
	case strings.Contains(question, "what can you do") || strings.Contains(question, "help"):
		return "I can help you with:\n• Converting natural language to commands (/cmd)\n• Answering questions (/ask)\n• Explaining commands (/explain)\n• Managing packages (/install, /update, /remove)\n• And much more! Try /help for all commands."
	case strings.Contains(question, "president") && strings.Contains(question, "usa"):
		return "As an AI assistant running offline, I don't have real-time information about current political positions. You might want to check a reliable news source for the most up-to-date information."
	case strings.Contains(question, "weather"):
		return "I'm currently running in offline mode, so I can't access real-time weather data. You could try using online services or check your local weather app."
	case strings.Contains(question, "time"):
		return fmt.Sprintf("The current system time is: %s", time.Now().Format("Monday, January 2, 2006 at 3:04 PM"))
	case strings.Contains(question, "hello") || strings.Contains(question, "hi"):
		return "Hello! I'm Helix, your AI CLI assistant. How can I help you today?"
	default:
		return fmt.Sprintf("I understand you're asking about: '%s'. This is a simulated response since we're in mock mode. In real mode, I would provide a helpful answer based on my training data.", question)
	}
}

// Generate a mock explanation for a command
func generateMockExplanation(command string) string {
	return fmt.Sprintf("The command '%s' appears to be a system command. In mock mode, I can't provide detailed explanations, but in real mode I would explain what this command does, its common options, and any potential risks.", command)
}

// attemptCommandFix tries to fix common AI command generation issues
func attemptCommandFix(command string) string {
	originalCommand := command

	// Fix 1: Fix file patterns with missing wildcards - be more intelligent
	filePatterns := []struct {
		wrong   string
		correct string
	}{
		{"-name '.go'", "-name '*.go'"},
		{"-name \".go\"", "-name \"*.go\""},
		{"-name .go", "-name '*.go'"},
		{"-name '.go", "-name '*.go'"},
		{"-name \"*.go", "-name \"*.go\""},
		{"-name '*.go", "-name '*.go'"},
		{"-name '.py'", "-name '*.py'"},
		{"-name '.js'", "-name '*.js'"},
		{"-name '.md'", "-name '*.md'"},
		{"-name '.txt'", "-name '*.txt'"},
		{"-name '.java'", "-name '*.java'"},
		{"-name '.cpp'", "-name '*.cpp'"},
		{"-name '.c'", "-name '*.c'"},
		{"-name '.html'", "-name '*.html'"},
		{"-name '.css'", "-name '*.css'"},
	}

	for _, pattern := range filePatterns {
		if strings.Contains(command, pattern.wrong) {
			command = strings.Replace(command, pattern.wrong, pattern.correct, 1)
		}
	}

	// Fix 2: Use regex for more robust pattern matching
	// This catches patterns like: -name '.go (missing quote and wildcard)
	patternRegex := regexp.MustCompile(`-name\s+['"]?(\.[a-zA-Z0-9]+)['"]?`)
	if matches := patternRegex.FindStringSubmatch(command); len(matches) > 1 {
		// Found a pattern like '.go' - replace it with '*.go'
		wrongPattern := matches[0]
		extension := matches[1]
		correctPattern := strings.Replace(wrongPattern, extension, "*"+extension, 1)
		command = strings.Replace(command, wrongPattern, correctPattern, 1)
	}

	// Fix 3: Remove trailing invalid characters (but be careful)
	command = strings.TrimSpace(command)
	if strings.HasSuffix(command, ");") {
		command = strings.TrimSuffix(command, ");")
	}
	if strings.HasSuffix(command, ")") && !strings.Contains(command, "(") {
		command = strings.TrimSuffix(command, ")")
	}

	// Fix 4: Fix unmatched quotes ONLY if it's a clear pattern
	command = utils.FixUnmatchedQuotes(command)

	// Fix 5: Remove duplicate "git" prefixes for non-git commands
	if strings.HasPrefix(command, "git find") {
		command = strings.TrimPrefix(command, "git ")
	}
	command = strings.ReplaceAll(command, "git find", "find")

	// Only return the fixed command if we actually made meaningful changes
	if command != originalCommand {
		return command
	}
	return originalCommand
}

// hasSyntaxErrors checks for obvious shell syntax errors
func hasSyntaxErrors(command string) bool {
	// Check for completely unbalanced quotes (be more tolerant)
	singleQuotes := strings.Count(command, "'")
	doubleQuotes := strings.Count(command, `"`)

	// Only flag as error if we have clear, multiple unbalanced quotes
	if (singleQuotes%2 != 0 && singleQuotes > 1) || (doubleQuotes%2 != 0 && doubleQuotes > 1) {
		return true
	}

	// Check for trailing invalid characters that break execution
	trimmed := strings.TrimSpace(command)
	if strings.HasSuffix(trimmed, ")") && !strings.Contains(trimmed, "(") {
		return true
	}

	// Check for obvious shell syntax errors
	invalidPatterns := []string{
		"&&)", "||)", "|)", ">)", ">>)", "<)",
		"find .)", "grep )", "ls )",
	}

	for _, pattern := range invalidPatterns {
		if strings.Contains(command, pattern) {
			return true
		}
	}

	return false
}

// Function to determine if a command should be explained
func shouldExplainCommand(command string) bool {
	// Commands that might need explanation
	complexCommands := []string{
		"rm -", "chmod", "chown", "dd", "find", "grep", "sed", "awk",
		"curl", "wget", "ssh", "scp", "rsync", "tar", "gzip",
	}

	return utils.ContainsAny(strings.ToLower(command), complexCommands)
}

// Function to explain a command
func explainCommand(command string, mockMode bool) {
	color.Blue("📖 Getting explanation...")

	var explanation string
	var err error

	if mockMode {
		explanation = generateMockExplanation(command)
	} else {
		explanation, err = commands.ExplainCommand(command)
		if err != nil {
			color.Red("❌ Explanation failed: %v", err)
			return
		}

		// FALLBACK MECHANISM: If AI returns empty, use fallback
		if strings.TrimSpace(explanation) == "" {
			color.Yellow("⚠️  AI returned empty explanation, using fallback")
			explanation = generateFallbackExplanation(command)
		}
	}

	ux := ux.NewUX()
	ux.PrintAIResponse(explanation, !mockMode)
}

// generateFallbackExplanation provides a basic explanation if AI fails
func generateFallbackExplanation(command string) string {
	command = strings.ToLower(command)

	// Simple rule-based fallback explanations for common commands
	switch {
	case strings.Contains(command, "find") && strings.Contains(command, "-exec"):
		return "This find command searches for files and executes another command on each result. Powerful but can be slow on large directories."

	case strings.Contains(command, "grep"):
		return "Searches for text patterns in files. Essential for code analysis and log inspection."

	case strings.Contains(command, "curl") || strings.Contains(command, "wget"):
		return "Downloads or transfers data from networks. Commonly used for API testing and file downloads."

	case strings.Contains(command, "git merge"):
		return "Combines changes from different branches. Can modify commit history - use carefully."

	case strings.Contains(command, "docker") || strings.Contains(command, "podman"):
		return "Container management command. Handles isolated application environments."

	case strings.Contains(command, "chmod"):
		return "Changes file permissions. Affects security and access controls."

	case strings.Contains(command, "chown"):
		return "Changes file ownership. Requires appropriate privileges."

	case strings.Contains(command, "rm "):
		return "Removes files or directories. Can cause data loss - double-check paths."

	case strings.Contains(command, "mv "):
		return "Moves or renames files. Overwrites existing files without warning."

	case strings.Contains(command, "cp "):
		return "Copies files or directories. Preserves originals but can overwrite destinations."

	case strings.Contains(command, "ssh "):
		return "Secure shell connection to remote servers. Provides encrypted terminal access."

	case strings.Contains(command, "scp "):
		return "Securely copies files between systems over SSH."

	case strings.Contains(command, "rsync"):
		return "Efficient file synchronization between locations. Great for backups."

	case strings.Contains(command, "tar "):
		return "Archives files into a single package. Commonly used for compression and distribution."

	case strings.Contains(command, "sed "):
		return "Stream editor for text transformation. Powerful for batch file editing."

	case strings.Contains(command, "awk "):
		return "Pattern scanning and processing language. Excellent for data extraction and reporting."

	case strings.Contains(command, "xargs"):
		return "Converts input into command arguments. Useful for processing large file lists."

	case strings.Contains(command, "|"):
		return "Uses pipes to chain multiple commands together. Output of one becomes input to the next."

	case strings.Contains(command, ">"):
		return "Redirects output to a file, overwriting existing content."

	case strings.Contains(command, ">>"):
		return "Redirects output to a file, appending to existing content."

	default:
		// Generic fallback based on the first word
		parts := strings.Fields(command)
		if len(parts) > 0 {
			mainCommand := parts[0]
			return fmt.Sprintf("This appears to be a '%s' command. For detailed information, try 'man %s' or '%s --help'.",
				mainCommand, mainCommand, mainCommand)
		}
		return "This command performs a system operation. Use manual pages (man) for detailed information."
	}
}

// manualCommandEdit allows user to manually fix the command
func manualCommandEdit(currentCommand string) string {
	color.Cyan("✏️  Manual Command Editor")
	color.Cyan("Current command: %s", currentCommand)
	color.Cyan("Enter corrected command (or press Enter to cancel): ")

	reader := bufio.NewReader(os.Stdin)
	edited, _ := reader.ReadString('\n')
	edited = strings.TrimSpace(edited)

	if edited == "" {
		return ""
	}
	return edited
}

// For testing the AI model with various prompts - /test-ai command
func testAIModel() {
	color.Cyan("🧪 Testing AI model with different prompts...")

	tests := []struct {
		name   string
		prompt string
	}{
		{"Simple Q&A", "Q: What is the sun?\nA:"},
		{"Instruction", "Instruction: Answer in one sentence. What is the sun?\nAnswer:"},
		{"Strict", "Answer the question in one word: Hello\nResponse:"},
		{"Chat", "User: What is the sun?\nAssistant:"},
	}

	for _, test := range tests {
		color.Blue("Testing: %s", test.name)
		response, err := ai.RunModel(test.prompt)
		if err != nil {
			color.Red("  ❌ Failed: %v", err)
		} else {
			clean := strings.TrimSpace(response)
			color.Green("  ✅ Response: '%s'", clean)
			if len(clean) > 50 {
				color.Yellow("  ⚠️  Too verbose")
			}
		}
		time.Sleep(1 * time.Second) // Don't overwhelm the model
	}
}

// isCommandReasonable checks if the command matches the request intent
func isCommandReasonable(request, command string) bool {
	request = strings.ToLower(request)
	command = strings.ToLower(command)

	// Check if command type matches request type
	if strings.Contains(request, "file") || strings.Contains(request, "list") || strings.Contains(request, "show") {
		// File operations should use ls, find, etc. not git
		if strings.Contains(command, "git") && !strings.Contains(request, "git") {
			return false
		}
	}

	return true
}

// isNaturalLanguage checks if the input is natural language
func isNaturalLanguage(text string) bool {
	// If it starts with a question word or is too long, it's probably natural language
	questionWords := []string{"what", "how", "why", "when", "where", "which", "who"}
	text = strings.ToLower(text)

	if len(text) > 100 {
		return true
	}

	for _, word := range questionWords {
		if strings.HasPrefix(text, word) {
			return true
		}
	}

	// If it contains multiple spaces and no command-like structure
	if strings.Count(text, " ") > 5 && !strings.ContainsAny(text, "-./*$") {
		return true
	}

	return false
}

// buildEnhancedAskPrompt builds the enhanced ask prompt
func generateFallbackCommand(request string, env shell.Env) string {
	request = strings.ToLower(request)

	switch {
	case strings.Contains(request, "file") || strings.Contains(request, "list") || strings.Contains(request, "show"):
		if env.IsUnixLike() {
			return "ls -la"
		} else {
			return "dir"
		}
	case strings.Contains(request, "directory") || strings.Contains(request, "folder"):
		if env.IsUnixLike() {
			return "pwd"
		} else {
			return "cd"
		}
	case strings.Contains(request, "go file") || strings.Contains(request, ".go"):
		return "find . -name \"*.go\" -type f"
	default:
		return "" // No good fallback
	}
}

// checkRAGProgress checks and displays RAG indexing progress
func checkRAGProgress() {
	if ragSystem == nil || pb.IsRAGAvailable() {
		return
	}

	stats := ragSystem.GetSystemStats()
	if pages, ok := stats["indexed_pages"]; ok {
		if pageCount, ok := pages.(int); ok && pageCount > 0 {
			status := ragSystem.GetIndexingStatus()
			color.Magenta("🧠 RAG Progress: %d pages (%s)", pageCount, status)

			// Show when RAG becomes available
			if ragSystem.IsInitialized() {
				color.Green("🎉 RAG system is now ACTIVE! Enhanced commands available.")
			}
		}
	}
}
