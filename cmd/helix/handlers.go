package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/utils"
	"helix/internal/ux"

	"github.com/fatih/color"
)

// Handle /cmd command
func handleCmdCommand(input string, mockMode bool) {
	commandText := strings.TrimSpace(strings.TrimPrefix(input, "/cmd"))
	if commandText == "" {
		color.Red("❌ Usage: /cmd <natural language command>")
		color.Yellow("💡 Example: /cmd 'list all files in current directory'")
		return
	}

	// Build the prompt for command generation
	prompt := pb.BuildCommandPrompt(commandText)

	// ADD THIS DEBUG
	color.Yellow("🔍 DEBUG: Final prompt being sent to AI (%d chars):", len(prompt))
	color.Yellow("--- PROMPT START ---")
	color.Yellow("%s", prompt)
	color.Yellow("--- PROMPT END ---")

	color.Blue("🤖 Processing: %s", commandText)

	var aiResponse string
	var err error

	if mockMode {
		// Mock AI response
		aiResponse = generateMockCommand(commandText, env)
		color.Green("🤖 [Mock AI] → %s", aiResponse)
	} else {
		// Real AI processing
		start := time.Now()
		aiResponse, err = ai.RunModel(prompt)
		if err != nil {
			color.Red("❌ AI error: %v", err)
			return
		}
		color.Green("✅ AI processed in %s", utils.FormatDuration(time.Since(start)))
	}

	// NEW: Check for empty response and provide fallback
	if strings.TrimSpace(aiResponse) == "" {
		color.Red("❌ AI returned empty response")
		color.Yellow("💡 This might be due to:")
		color.Yellow("  - Model not understanding the prompt")
		color.Yellow("  - RAG context being confusing")
		color.Yellow("  - Model needing different parameters")

		// Try fallback with simpler prompt
		color.Blue("🔄 Trying fallback with simpler prompt...")
		simplePrompt := fmt.Sprintf("Command to %s:", commandText)
		fallbackResponse, fallbackErr := ai.RunModel(simplePrompt)

		if fallbackErr != nil {
			color.Red("❌ Fallback also failed: %v", fallbackErr)
			return
		}

		if strings.TrimSpace(fallbackResponse) != "" {
			color.Green("✅ Fallback successful!")
			aiResponse = fallbackResponse
		} else {
			color.Red("❌ Fallback also returned empty")
			// Final fallback to mock command
			color.Blue("🔄 Using mock command as final fallback...")
			aiResponse = generateMockCommand(commandText, env)
			color.Green("🤖 [Fallback] → %s", aiResponse)
		}
	}

	// Extract the actual command from AI response
	command := ai.ExtractCommand(aiResponse)

	if command == "" {
		color.Red("❌ AI didn't generate a valid command")
		color.Yellow("Raw AI response: %s", aiResponse)
		return
	}

	// Show the raw command before cleaning for debugging
	color.Yellow("🔍 Raw AI command: %s", command)

	// NEW: Enhanced detailed analysis
	color.Blue("🔬 Analyzing command structure:")
	color.Blue("  - Single quotes: %d", strings.Count(command, "'"))
	color.Blue("  - Double quotes: %d", strings.Count(command, `"`))
	color.Blue("  - Has '*.go': %v", strings.Contains(command, "*.go"))
	color.Blue("  - Has '.go': %v", strings.Contains(command, ".go") && !strings.Contains(command, "*.go"))
	color.Blue("  - Has trailing ): %v", strings.HasSuffix(command, ")"))

	// NEW: Show specific issues detected
	if strings.Contains(command, ")") && !strings.Contains(command, "(") {
		color.Red("⚠️  Detected invalid trailing parenthesis")
	}
	if strings.Contains(command, ".go") && !strings.Contains(command, "*.go") {
		color.Red("⚠️  Detected malformed file pattern: '.go' should be '*.go'")
	}
	if strings.Count(command, "'")%2 != 0 || strings.Count(command, `"`)%2 != 0 {
		color.Red("⚠️  Detected unbalanced quotes")
	}

	// NEW: Enhanced command fixing with detailed feedback
	color.Blue("🛠️  Applying automatic fixes...")
	fixedCommand := attemptCommandFix(command)

	// NEW: Show what changed in detail
	if fixedCommand != command {
		color.Green("🔧 Fixed command:")
		color.Green("   BEFORE: %s", command)
		color.Green("   AFTER:  %s", fixedCommand)

		// Show specific changes
		if strings.Contains(command, ".go") && !strings.Contains(command, "*.go") &&
			strings.Contains(fixedCommand, "*.go") {
			color.Green("   ✓ Fixed file pattern: '.go' → '*.go'")
		}
		if strings.HasSuffix(command, ")") && !strings.HasSuffix(fixedCommand, ")") {
			color.Green("   ✓ Removed trailing parenthesis")
		}
		if (strings.Count(command, "'")%2 != 0 && strings.Count(fixedCommand, "'")%2 == 0) ||
			(strings.Count(command, `"`)%2 != 0 && strings.Count(fixedCommand, `"`)%2 == 0) {
			color.Green("   ✓ Fixed unbalanced quotes")
		}

		command = fixedCommand
	} else {
		color.Yellow("🔧 No fixes applied")
	}

	// Clean and validate the command
	cleanedCommand, err := commands.ValidateAndCleanCommand(command)
	if err != nil {
		color.Red("❌ Command validation failed: %v", err)
		color.Yellow("Attempted command: %s", command)

		// NEW: Enhanced validation error handling
		if strings.Contains(err.Error(), "unmatched quotes") {
			color.Yellow("💡 Quote balancing issue detected")
			// Try one more fix attempt with enhanced repair
			repairedCommand := utils.FixUnmatchedQuotes(command)
			if repairedCommand != command {
				color.Blue("🔄 Retrying with enhanced quote repair...")
				cleanedCommand, err = commands.ValidateAndCleanCommand(repairedCommand)
				if err == nil {
					command = cleanedCommand
					color.Green("✅ Quote repair successful: %s", command)
				} else {
					color.Red("❌ Quote repair failed: %v", err)
				}
			}
		}

		if err != nil {
			color.Red("❌ Command cannot be fixed: %v", err)

			// NEW: Offer manual editing option
			if commands.AskForConfirmation("Would you like to manually edit the command?") {
				manuallyEdited := manualCommandEdit(command)
				if manuallyEdited != "" {
					cleanedCommand, err = commands.ValidateAndCleanCommand(manuallyEdited)
					if err == nil {
						command = cleanedCommand
						color.Green("✅ Manual edit successful: %s", command)
					} else {
						color.Red("❌ Manual edit still invalid: %v", err)
						return
					}
				} else {
					color.Yellow("❌ Manual edit cancelled")
					return
				}
			} else {
				return
			}
		}
	} else {
		command = cleanedCommand
	}

	color.Cyan("💡 Final command: %s", command)

	// NEW: Manual fix option for stubborn patterns
	if strings.Contains(command, ".go") && !strings.Contains(command, "*.go") {
		color.Yellow("⚠️  AI generated malformed file pattern")
		if commands.AskForConfirmation("Apply manual fix for file pattern?") {
			// Apply targeted fix - PRESERVE WILDCARD!
			oldCommand := command
			command = strings.ReplaceAll(command, ".go", "*.go")
			command = strings.ReplaceAll(command, "'.go", "'*.go")
			command = strings.ReplaceAll(command, "\".go", "\"*.go")
			color.Green("✅ Manually fixed: %s → %s", oldCommand, command)
		}
	}

	// Show syntax-highlighted version
	syntaxHighlighter.PrintHighlightedCommand("Generated command", command)

	// NEW: Enhanced validation with detailed feedback
	color.Blue("🔍 Validating command syntax...")
	if hasSyntaxErrors(command) {
		color.Red("❌ Command has syntax errors")

		// Show specific issues
		if strings.Contains(command, ".go") && !strings.Contains(command, "*.go") {
			color.Red("   ✗ Malformed file pattern: '.go' should be '*.go'")
		}
		if strings.HasSuffix(command, ")") {
			color.Red("   ✗ Trailing parenthesis")
		}
		if !utils.HasBalancedQuotes(command) {
			color.Red("   ✗ Unbalanced quotes")
		}

		color.Yellow("💡 The generated command may not execute properly")
	} else {
		color.Green("✅ Command syntax validation passed")
		// Optional command breakdown
		if commands.AskForConfirmation("Show command breakdown?") {
			syntaxHighlighter.ExplainCommandComponents(command)
			fmt.Println()
		}
	}

	// Show the cleaned command for transparency
	if command != strings.TrimSpace(aiResponse) {
		color.Yellow("🔧 Note: Command was cleaned for safety")
	}

	// Ask for explanation if the command looks complex
	if shouldExplainCommand(command) && commands.AskForConfirmation("Would you like an explanation of this command?") {
		explainCommand(command, mockMode)
	}

	// NEW: Enhanced final validation before execution
	color.Cyan("🎯 Ready to execute:")
	syntaxHighlighter.PrintHighlightedCommand("", command)

	// NEW: Comprehensive pre-execution check
	if hasSyntaxErrors(command) {
		color.Red("🚨 WARNING: Command has syntax errors that may cause failure")
		color.Yellow("💡 Recommended: Cancel and try a different phrasing")
		if !commands.AskForConfirmation("Execute anyway? (likely to fail)") {
			color.Yellow("❌ Execution cancelled due to syntax errors")
			return
		}
	} else {
		color.Green("✅ Command looks good to execute")
	}

	// Final confirmation before execution
	color.Yellow("🔍 Final command to execute: '%s'", command)

	// Execute the command
	if commands.AskForConfirmation("Execute this command?") {
		err := sandbox.WrapCommand(command, execConfig, env)
		if err != nil {
			color.Red("❌ Command failed: %v", err)

			// Enhanced error suggestions
			if strings.Contains(err.Error(), "command not found") {
				color.Yellow("💡 The command or program may not be installed")
			} else if strings.Contains(err.Error(), "No such file or directory") {
				color.Yellow("💡 Check if the file or directory exists")
			} else if strings.Contains(err.Error(), "Permission denied") {
				color.Yellow("💡 You may need elevated privileges for this command")
			} else if strings.Contains(err.Error(), "syntax error") {
				color.Yellow("💡 The command has shell syntax errors")
				color.Yellow("💡 Try rephrasing your request differently")
			} else if strings.Contains(err.Error(), "unmatched") {
				color.Yellow("💡 There are unmatched quotes or parentheses")
			}
		} else {
			color.Green("✅ Command executed successfully!")
		}
	} else {
		color.Yellow("💡 Command ready to use: %s", command)
	}
}

// Handle /ask command
func handleAskCommand(input string, mockMode bool) {
	promptText := strings.TrimSpace(strings.TrimPrefix(input, "/ask"))
	if promptText == "" {
		color.Red("❌ Usage: /ask <question>")
		color.Yellow("💡 Example: /ask 'how do I check disk space?'")
		return
	}

	color.Blue("🤖 Thinking about: %s", promptText)

	var response string
	var err error

	if mockMode {
		// Mock AI response
		response = generateMockResponse(promptText)
	} else {
		// Use a prompt that enforces English and concise responses
		prompt := fmt.Sprintf(`Instruction: Answer the following question in English. Be concise and direct.

Question: %s

Answer:`, promptText)

		// Use more restrictive parameters
		config := ai.ModelConfig{
			Temperature: 0.3, // Lower for more deterministic responses
			TopP:        0.7,
			TopK:        20,
			MaxTokens:   150, // Limit response length
		}

		start := time.Now()
		response, err = ai.RunModelWithConfig(prompt, config)
		if err != nil {
			color.Red("❌ AI error: %v", err)
			return
		}
		color.Green("✅ AI processed in %s", utils.FormatDuration(time.Since(start)))

		// Debug: Show raw response
		color.Yellow("🔍 Raw AI response: '%s'", response)
	}

	// Basic cleaning
	response = strings.TrimSpace(response)

	if response == "" {
		color.Red("❌ AI generated an empty response")
		return
	}

	// Create UX manager for nice output
	ux := ux.NewUX()
	ux.PrintAIResponse(response, !mockMode)
}

// Handle /explain command
func handleExplainCommand(input string, mockMode bool) {
	commandText := strings.TrimSpace(strings.TrimPrefix(input, "/explain"))
	if commandText == "" {
		color.Red("❌ Usage: /explain <command>")
		color.Yellow("💡 Example: /explain 'git push origin main'")
		return
	}

	color.Blue("📚 Explaining command: %s", commandText)

	var explanation string
	var err error

	if mockMode {
		explanation = generateMockExplanation(commandText)
	} else {
		// Uses RAG-enhanced explanation automatically
		explanation, err = ai.RunModel(pb.BuildExplainPrompt(commandText))
		if err != nil {
			color.Red("❌ AI error: %v", err)
			return
		}
	}

	ux := ux.NewUX()
	ux.PrintAIResponse(explanation, !mockMode)
}

// Handle /install command
func handleInstallCommand(input string, mockMode bool) {
	args := strings.Fields(input)
	if len(args) < 2 {
		color.Red("❌ Usage: /install <package-name>")
		color.Yellow("💡 Example: /install git")
		return
	}

	action := "install"
	packageName := args[1]

	commands.HandlePackageCommand([]string{action, packageName}, env, mockMode, execConfig)
}

// Handle /update command
func handleUpdateCommand(input string, mockMode bool) {
	args := strings.Fields(input)
	if len(args) < 2 {
		color.Red("❌ Usage: /update <package-name>")
		color.Yellow("💡 Example: /update git")
		return
	}

	action := "update"
	packageName := args[1]

	commands.HandlePackageCommand([]string{action, packageName}, env, mockMode, execConfig)
}

// Handle /remove command
func handleRemoveCommand(input string, mockMode bool) {
	args := strings.Fields(input)
	if len(args) < 2 {
		color.Red("❌ Usage: /remove <package-name>")
		color.Yellow("💡 Example: /remove git")
		return
	}

	action := "remove"
	packageName := args[1]

	commands.HandlePackageCommand([]string{action, packageName}, env, mockMode, execConfig)
}

// Handle /sandbox command
func handleSandboxCommand(input string) {
	args := strings.Fields(input)
	if len(args) < 2 {
		// Show current status
		sandbox.PrintStatus()
		color.Yellow("💡 Usage: /sandbox <mode>")
		color.Yellow("Modes: off, current, strict")
		color.Yellow("Examples:")
		color.Yellow("  /sandbox current  - Restrict to current directory")
		color.Yellow("  /sandbox off      - Disable restrictions")
		color.Yellow("  /sandbox strict   - Strict mode (current + subdirs only)")
		return
	}

	mode := strings.ToLower(args[1])
	switch mode {
	case "off", "disable", "none":
		sandbox.SetMode(commands.SandboxDisabled)
	case "current", "dir", "normal":
		sandbox.SetMode(commands.SandboxCurrentDir)
	case "strict", "tight", "restricted":
		sandbox.SetMode(commands.SandboxStrict)
	default:
		color.Red("❌ Unknown sandbox mode: %s", mode)
		color.Yellow("💡 Available modes: off, current, strict")
	}
}

// Handle /cd command
func handleChangeDirectory(input string) {
	targetDir := strings.TrimSpace(strings.TrimPrefix(input, "/cd"))
	if targetDir == "" {
		// Show current directory
		currentDir, _ := os.Getwd()
		color.Cyan("📁 Current directory: %s", currentDir)
		return
	}

	if err := sandbox.ChangeDirectory(targetDir); err != nil {
		color.Red("❌ Failed to change directory: %v", err)
	}
}

// Handle /git command
func handleGitCommand(input string) {
	commandText := strings.TrimSpace(strings.TrimPrefix(input, "/git"))
	if commandText == "" {
		color.Red("❌ Usage: /git <git operation>")
		color.Yellow("💡 Examples:")
		color.Yellow("  /git merge feature-branch with squash and accept all changes")
		color.Yellow("  /git undo last commit")
		color.Yellow("  /git clean untracked files")
		color.Yellow("  /git status")
		return
	}

	if err := gitManager.HandleGitRequest(commandText); err != nil {
		color.Red("❌ Git operation failed: %v", err)
	}
}

// Handle /rag-status command
func handleRAGStatus() {
	color.Cyan("🧠 RAG System Status:")

	if ragSystem == nil {
		color.Red("  ❌ RAG system not initialized")
		return
	}

	stats := ragSystem.GetSystemStats()
	indexingStatus := "UNKNOWN"

	// Use reflection to get the indexing status if available
	if rs, ok := interface{}(ragSystem).(interface{ GetIndexingStatus() string }); ok {
		indexingStatus = rs.GetIndexingStatus()
	}

	color.Cyan("  📊 Statistics:")
	color.Cyan("    • Initialized: %v", stats["initialized"])
	color.Cyan("    • Indexed MAN Pages: %v", stats["indexed_pages"])
	color.Cyan("    • Indexing Status: %s", indexingStatus)

	if stats["initialized"].(bool) {
		color.Green("  ✅ RAG system is ACTIVE")
		color.Cyan("    • Vector Documents: %v", stats["total_documents"])
		color.Cyan("    • Unique Commands: %v", stats["unique_commands"])
	} else {
		color.Yellow("  🔄 RAG system is %s...", indexingStatus)

		// Show estimated time based on typical indexing
		if stats["indexed_pages"].(int) > 0 {
			color.Cyan("    • Progress: %d pages indexed", stats["indexed_pages"])
		}
	}
}

// Handle /rag-reindex command
func handleRAGReindex() {
	color.Blue("🔄 Manual RAG reindexing...")

	if ragSystem == nil {
		color.Red("❌ RAG system not initialized")
		return
	}

	// Force reindex by removing state
	homeDir, _ := os.UserHomeDir()
	stateFile := filepath.Join(homeDir, ".helix", "rag_index", "rag_state.json")
	os.Remove(stateFile)

	go ragSystem.IndexAvailableManPages()
	color.Green("✅ RAG reindexing started in background")
}

// Toggle dry-run mode
func toggleDryRun() {
	execConfig.DryRun = !execConfig.DryRun
	if execConfig.DryRun {
		color.Yellow("🔒 Dry-run mode ENABLED - commands will be shown but not executed")
	} else {
		color.Green("🚀 Dry-run mode DISABLED - commands will be executed")
	}
}

// Check and display online status
func checkOnlineStatus() {
	color.Blue("🌐 Checking internet connectivity...")

	if utils.IsOnline(3 * time.Second) {
		color.Green("✅ Online - Real-time capabilities available")
	} else {
		color.Yellow("⚠️  Offline - Using local AI only")
	}
}

// Add to handlers.go
func handleRAGReset() {
	color.Blue("🔄 Resetting RAG system...")

	if ragSystem == nil {
		color.Red("❌ RAG system not initialized")
		return
	}

	homeDir, _ := os.UserHomeDir()
	ragDir := filepath.Join(homeDir, ".helix", "rag_index")

	if err := os.RemoveAll(ragDir); err != nil {
		color.Red("❌ Failed to reset RAG: %v", err)
		return
	}

	color.Green("✅ RAG system reset. Will reindex on next startup.")
}

// Add this function to handlers.go
func testBasicAI() {
	color.Cyan("🧪 Testing basic AI functionality...")

	// Test 1: Very simple prompt
	simplePrompt := "Say 'hello world'"
	response, err := ai.RunModel(simplePrompt)
	if err != nil {
		color.Red("❌ Basic AI test failed: %v", err)
		return
	}
	color.Green("✅ Basic AI response: '%s'", strings.TrimSpace(response))

	// Test 2: Simple command prompt
	commandPrompt := "Command to list files:"
	response2, err := ai.RunModel(commandPrompt)
	if err != nil {
		color.Red("❌ Command AI test failed: %v", err)
		return
	}
	color.Green("✅ Command AI response: '%s'", strings.TrimSpace(response2))

	// Test 3: Current command prompt style
	currentPrompt := pb.BuildCommandPrompt("list files")
	response3, err := ai.RunModel(currentPrompt)
	if err != nil {
		color.Red("❌ Current prompt test failed: %v", err)
		return
	}
	color.Green("✅ Current prompt response: '%s'", strings.TrimSpace(response3))
}
