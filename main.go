// main.go
package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/fatih/color"
)

// Package-level variables
var (
	cfg               *Config
	env               Env
	pb                *PromptBuilder
	online            bool
	execConfig        ExecuteConfig
	gitManager        *GitManager
	syntaxHighlighter *SyntaxHighlighter
	sandbox           *DirectorySandbox
)

func main() {
	// Initialize color output
	color.Cyan("🚀 Helix v%s — AI-Powered CLI Assistant", HelixVersion)
	color.Yellow("Repository: https://github.com/Nibir1/Helix")

	// Load configuration
	var err error
	cfg, err = DefaultConfig()
	if err != nil {
		color.Red("Error loading config: %v", err)
		return
	}

	// Detect environment
	env = DetectEnvironment()
	color.Blue("🌍 Detected: %s (%s shell)", strings.Title(env.OSName), env.Shell)

	// Check internet connectivity
	online = IsOnline(5 * time.Second)
	if online {
		color.Green("✅ Online mode - real-time capabilities available")
	} else {
		color.Yellow("⚠️  Offline mode - using local AI only")
	}

	// Initialize Git manager
	gitManager = NewGitManager(env, execConfig, sandbox)

	// Initialize prompt builder
	pb = NewPromptBuilder(env, online)

	// Initialize syntax highlighter
	syntaxHighlighter = NewSyntaxHighlighter()

	// Initialize directory sandbox
	sandbox = NewDirectorySandbox()

	// Set execution config
	execConfig = DefaultExecuteConfig()

	// Ensure model directory exists
	if err := cfg.EnsureModelDir(); err != nil {
		color.Red("Error creating model directory: %v", err)
		return
	}

	// Download model if not present
	if err := DownloadModel(cfg.ModelFile, ModelURL, ModelChecksum); err != nil {
		color.Yellow("⚠️  Model download error: %v", err)
		color.Yellow("Running in enhanced mock mode.")
		runEnhancedMockMode()
		return
	}

	// Verify model file
	fileInfo, err := os.Stat(cfg.ModelFile)
	if err != nil {
		color.Red("⚠️  Model file not found: %v", err)
		runEnhancedMockMode()
		return
	}

	color.Green("✅ Model file exists: %s (Size: %.2f MB)",
		cfg.ModelFile,
		float64(fileInfo.Size())/(1024*1024))

	// Load LLaMA model
	color.Blue("🔧 Loading AI model...")
	if err := LoadModel(cfg.ModelFile); err != nil {
		color.Red("⚠️  Failed to load model: %v", err)
		color.Yellow("This could indicate:")
		color.Yellow("  - Corrupted model file")
		color.Yellow("  - Incompatible model format")
		color.Yellow("  - Insufficient RAM/VRAM")

		runEnhancedMockMode()
		return
	}

	defer CloseModel()
	color.Green("✅ AI model loaded successfully!")

	// Create UX manager for nice output
	ux := NewUX()
	ux.ShowWelcomeBanner("0.2.0")

	// Test the model with a simple prompt
	color.Blue("🧪 Testing AI with simple prompt...")
	testResponse, err := RunModel("Say 'Hello from Helix!' in one sentence:")
	if err != nil {
		color.Red("⚠️  Model test failed: %v", err)
		runEnhancedMockMode()
		return
	}

	color.Cyan("🤖 AI Test: %s", testResponse)
	color.Green("🎉 Helix is ready! Type '/help' for available commands.")

	// Start enhanced CLI loop
	runEnhancedCLI()
}

func runEnhancedMockMode() {
	color.Yellow("\n🔧 ENHANCED MOCK MODE ACTIVATED")
	color.Yellow("AI commands will be simulated with intelligent responses")

	execConfig.DryRun = true
	env = DetectEnvironment()
	pb = NewPromptBuilder(env, online)

	reader := bufio.NewReader(os.Stdin)
	for {
		color.Cyan("[helix-mock]> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch {
		case input == "/exit":
			color.Green("Exiting Helix. Goodbye! 👋")
			return
		case input == "/debug":
			showDebugInfo()
		case input == "/help":
			showHelp()
		case strings.HasPrefix(input, "/cmd"):
			handleCmdCommand(input, true)
		case strings.HasPrefix(input, "/ask"):
			handleAskCommand(input, true)
		case strings.HasPrefix(input, "/explain"):
			handleExplainCommand(input, true)
		case strings.HasPrefix(input, "/install"):
			handleInstallCommand(input, true)
		case strings.HasPrefix(input, "/update"):
			handleUpdateCommand(input, true)
		case strings.HasPrefix(input, "/remove"):
			handleRemoveCommand(input, true)
		case strings.HasPrefix(input, "/dry-run"):
			toggleDryRun()
		case input == "/online":
			checkOnlineStatus()
		default:
			color.Yellow("❓ Unknown command. Type '/help' for available commands.")
		}
	}
}

func runEnhancedCLI() {
	reader := bufio.NewReader(os.Stdin)

	// Load command history
	history, _ := LoadHistory(cfg.HistoryPath)
	if len(history) > 0 {
		color.Blue("📚 Loaded %d commands from history", len(history))
	}

	for {
		color.Cyan("[helix]> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		// Save to history
		if input != "" && input != "/exit" {
			AppendHistory(cfg.HistoryPath, input)
		}

		switch {
		case input == "/exit":
			color.Green("Exiting Helix. Goodbye! 👋")
			return
		case input == "/debug":
			showDebugInfo()
		case input == "/help":
			showHelp()
		case input == "/online":
			checkOnlineStatus()
		case input == "/test-ai":
			testAIModel()
		case strings.HasPrefix(input, "/cmd"):
			handleCmdCommand(input, false)
		case strings.HasPrefix(input, "/ask"):
			handleAskCommand(input, false)
		case strings.HasPrefix(input, "/explain"):
			handleExplainCommand(input, false)
		case strings.HasPrefix(input, "/install"):
			handleInstallCommand(input, false)
		case strings.HasPrefix(input, "/update"):
			handleUpdateCommand(input, false)
		case strings.HasPrefix(input, "/git"):
			handleGitCommand(input)
		case strings.HasPrefix(input, "/sandbox"):
			handleSandboxCommand(input)
		case strings.HasPrefix(input, "/cd"):
			handleChangeDirectory(input)
		case strings.HasPrefix(input, "/remove"):
			handleRemoveCommand(input, false)
		case strings.HasPrefix(input, "/dry-run"):
			toggleDryRun()
		default:
			color.Yellow("💡 Tip: Start with '/ask' for questions or '/cmd' for command generation")
		}
	}
}

// --------------------------------------------------
// *** Command handlers for the Enhanced CLI ***
// --------------------------------------------------

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
		aiResponse, err = RunModel(prompt)
		if err != nil {
			color.Red("❌ AI error: %v", err)
			return
		}
		color.Green("✅ AI processed in %s", FormatDuration(time.Since(start)))
	}

	// Extract the actual command from AI response
	command := ExtractCommand(aiResponse)

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
	cleanedCommand, err := ValidateAndCleanCommand(command)
	if err != nil {
		color.Red("❌ Command validation failed: %v", err)
		color.Yellow("Attempted command: %s", command)

		// NEW: Enhanced validation error handling
		if strings.Contains(err.Error(), "unmatched quotes") {
			color.Yellow("💡 Quote balancing issue detected")
			// Try one more fix attempt with enhanced repair
			repairedCommand := fixUnmatchedQuotes(command)
			if repairedCommand != command {
				color.Blue("🔄 Retrying with enhanced quote repair...")
				cleanedCommand, err = ValidateAndCleanCommand(repairedCommand)
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
			if AskForConfirmation("Would you like to manually edit the command?") {
				manuallyEdited := manualCommandEdit(command)
				if manuallyEdited != "" {
					cleanedCommand, err = ValidateAndCleanCommand(manuallyEdited)
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
		if AskForConfirmation("Apply manual fix for file pattern?") {
			// Apply targeted fix
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
		if !hasBalancedQuotes(command) {
			color.Red("   ✗ Unbalanced quotes")
		}

		color.Yellow("💡 The generated command may not execute properly")
	} else {
		color.Green("✅ Command syntax validation passed")
		// Optional command breakdown
		if AskForConfirmation("Show command breakdown?") {
			syntaxHighlighter.ExplainCommandComponents(command)
			fmt.Println()
		}
	}

	// Show the cleaned command for transparency
	if command != strings.TrimSpace(aiResponse) {
		color.Yellow("🔧 Note: Command was cleaned for safety")
	}

	// Ask for explanation if the command looks complex
	if shouldExplainCommand(command) && AskForConfirmation("Would you like an explanation of this command?") {
		explainCommand(command, mockMode)
	}

	// NEW: Enhanced final validation before execution
	color.Cyan("🎯 Ready to execute:")
	syntaxHighlighter.PrintHighlightedCommand("", command)

	// NEW: Comprehensive pre-execution check
	if hasSyntaxErrors(command) {
		color.Red("🚨 WARNING: Command has syntax errors that may cause failure")
		color.Yellow("💡 Recommended: Cancel and try a different phrasing")
		if !AskForConfirmation("Execute anyway? (likely to fail)") {
			color.Yellow("❌ Execution cancelled due to syntax errors")
			return
		}
	} else {
		color.Green("✅ Command looks good to execute")
	}

	// Final confirmation before execution
	color.Yellow("🔍 Final command to execute: '%s'", command)

	// Execute the command
	if AskForConfirmation("Execute this command?") {
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

// NEW: manualCommandEdit allows user to manually fix the command
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
	command = fixUnmatchedQuotes(command)

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
		config := ModelConfig{
			Temperature: 0.3, // Lower for more deterministic responses
			TopP:        0.7,
			TopK:        20,
			MaxTokens:   150, // Limit response length
		}

		start := time.Now()
		response, err = RunModelWithConfig(prompt, config)
		if err != nil {
			color.Red("❌ AI error: %v", err)
			return
		}
		color.Green("✅ AI processed in %s", FormatDuration(time.Since(start)))

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
	ux := NewUX()
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
		prompt := pb.BuildExplainPrompt(commandText)
		explanation, err = RunModel(prompt)
		if err != nil {
			color.Red("❌ AI error: %v", err)
			return
		}
	}

	ux := NewUX()
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

	HandlePackageCommand([]string{action, packageName}, env, mockMode, execConfig)
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

	HandlePackageCommand([]string{action, packageName}, env, mockMode, execConfig)
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

	HandlePackageCommand([]string{action, packageName}, env, mockMode, execConfig)
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
		sandbox.SetMode(SandboxDisabled)
	case "current", "dir", "normal":
		sandbox.SetMode(SandboxCurrentDir)
	case "strict", "tight", "restricted":
		sandbox.SetMode(SandboxStrict)
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

// Show debug information
func showDebugInfo() {
	color.Cyan("=== 🔧 HELIX DEBUG INFORMATION ===")
	color.Cyan("Version: %s", HelixVersion)
	color.Cyan("Model: %s", cfg.ModelFile)
	color.Cyan("OS: %s", env.OSName)
	color.Cyan("Shell: %s", env.Shell)
	color.Cyan("User: %s", env.User)
	color.Cyan("Home: %s", env.HomeDir)
	color.Cyan("Online: %v", online)
	color.Cyan("Dry Run: %v", execConfig.DryRun)
	color.Cyan("Safe Mode: %v", execConfig.SafeMode)

	// Check model status
	if ModelIsLoaded() {
		color.Green("Model Status: ✅ Loaded")

		// Better model test - more specific and in English
		color.Blue("🧪 Running model test...")
		testResponse, err := RunModel("Answer with one word only: Hello")
		if err != nil {
			color.Red("Model Test: ❌ Failed - %v", err)
		} else {
			cleanResponse := strings.TrimSpace(testResponse)
			color.Green("Model Test: ✅ Working - '%s'", cleanResponse)

			// Check if response is reasonable
			if len(cleanResponse) > 100 {
				color.Yellow("⚠️  Model is generating verbose responses")
			}
			if !isMostlyEnglish(cleanResponse) {
				color.Yellow("⚠️  Model is responding in non-English")
			}
		}
	} else {
		color.Red("Model Status: ❌ Not loaded")
	}

	// Check package manager
	pkgMgr := DetectPackageManager(env)
	if pkgMgr.Exists {
		color.Green("Package Manager: %s", pkgMgr.Name)
	} else {
		color.Yellow("Package Manager: None detected")
	}

	// Check history
	history, _ := LoadHistory(cfg.HistoryPath)
	color.Cyan("Command History: %d entries", len(history))

	color.Cyan("=================================")
}

// Show help information
func showHelp() {
	ux := NewUX()
	ux.ShowHelp()
}

// Check and display oinline status
func checkOnlineStatus() {
	color.Blue("🌐 Checking internet connectivity...")

	if IsOnline(3 * time.Second) {
		color.Green("✅ Online - Real-time capabilities available")
	} else {
		color.Yellow("⚠️  Offline - Using local AI only")
	}
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

// Helper functions for mock mode
func generateMockCommand(request string, env Env) string {
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

// Function to determine if a command should be explained
func shouldExplainCommand(command string) bool {
	// Commands that might need explanation
	complexCommands := []string{
		"rm -", "chmod", "chown", "dd", "find", "grep", "sed", "awk",
		"curl", "wget", "ssh", "scp", "rsync", "tar", "gzip",
	}

	return ContainsAny(strings.ToLower(command), complexCommands)
}

// Function to explain a command
func explainCommand(command string, mockMode bool) {
	color.Blue("📖 Getting explanation...")

	var explanation string
	var err error

	if mockMode {
		explanation = generateMockExplanation(command)
	} else {
		explanation, err = ExplainCommand(command)
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

	ux := NewUX()
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
		response, err := RunModel(test.prompt)
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
