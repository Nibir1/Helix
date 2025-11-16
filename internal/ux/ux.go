package ux

import (
	"fmt"
	"strings"
	"time"

	"github.com/fatih/color"
)

// UX provides enhanced user experience features
type UX struct {
	typingSpeed time.Duration
	colors      *ColorScheme
}

// ColorScheme holds color configurations
type ColorScheme struct {
	Prompt     func(a ...interface{}) string
	AIResponse func(a ...interface{}) string
	Success    func(a ...interface{}) string
	Error      func(a ...interface{}) string
	Warning    func(a ...interface{}) string
	Info       func(a ...interface{}) string
	RAG        func(a ...interface{}) string
	Suggestion func(a ...interface{}) string
}

// NewUX creates a new UX manager
func NewUX() *UX {
	return &UX{
		typingSpeed: 30 * time.Millisecond,
		colors: &ColorScheme{
			Prompt:     color.New(color.FgCyan, color.Bold).SprintFunc(),
			AIResponse: color.New(color.FgGreen).SprintFunc(),
			Success:    color.New(color.FgGreen, color.Bold).SprintFunc(),
			Error:      color.New(color.FgRed, color.Bold).SprintFunc(),
			Warning:    color.New(color.FgYellow, color.Bold).SprintFunc(),
			Info:       color.New(color.FgBlue).SprintFunc(),
			RAG:        color.New(color.FgMagenta, color.Bold).SprintFunc(),
			Suggestion: color.New(color.FgHiCyan).SprintFunc(),
		},
	}
}

// Typewriter prints text with a smooth, natural typing animation.
// - Automatically speeds up for long text
// - Slightly slower after punctuation (more human-like)
// - Safe for multiline and large output
func (ux *UX) Typewriter(text string) {
	runes := []rune(text)
	n := len(runes)

	// Base typing speed
	baseDelay := 14 * time.Millisecond

	// If text is long, speed it up proportionally
	if n > 300 {
		baseDelay = 6 * time.Millisecond
	} else if n > 150 {
		baseDelay = 10 * time.Millisecond
	}

	for i, c := range runes {
		fmt.Printf("%c", c)

		// Newline? small pause
		if c == '\n' {
			time.Sleep(baseDelay * 3)
			continue
		}

		// Slow down after punctuation for realism
		if strings.ContainsRune(".?!,", c) {
			time.Sleep(baseDelay * 5)
			continue
		}

		// Small natural variation (±20%)
		delay := baseDelay
		if i%7 == 0 {
			delay = baseDelay + 3*time.Millisecond
		} else if i%5 == 0 {
			delay = baseDelay - 2*time.Millisecond
		}

		time.Sleep(delay)
	}

	fmt.Println()
}

// PrintAIResponse prints AI responses with typing effect and formatting
func (ux *UX) PrintAIResponse(text string, useTypingEffect bool) {
	formattedText := ux.formatResponse(text)

	fmt.Print(ux.colors.AIResponse("🤖 [Helix AI] → "))

	if useTypingEffect {
		ux.Typewriter(formattedText)
	} else {
		fmt.Println(formattedText)
	}
}

// PrintRAGEnhancedResponse prints AI responses with RAG context indication
func (ux *UX) PrintRAGEnhancedResponse(text string, useTypingEffect bool) {
	formattedText := ux.formatResponse(text)

	fmt.Print(ux.colors.RAG("🧠 [Helix RAG] → "))

	if useTypingEffect {
		ux.Typewriter(formattedText)
	} else {
		fmt.Println(formattedText)
	}
}

// PrintCommand prints command execution information
func (ux *UX) PrintCommand(command string) {
	fmt.Printf("%s %s\n",
		ux.colors.Info("🚀 Executing:"),
		ux.colors.Prompt(command))
}

// PrintSuccess prints success messages
func (ux *UX) PrintSuccess(message string) {
	fmt.Printf("%s %s\n", "✅", ux.colors.Success(message))
}

// PrintError prints error messages
func (ux *UX) PrintError(message string) {
	fmt.Printf("%s %s\n", "❌", ux.colors.Error(message))
}

// PrintWarning prints warning messages
func (ux *UX) PrintWarning(message string) {
	fmt.Printf("%s %s\n", "⚠️", ux.colors.Warning(message))
}

// PrintInfo prints informational messages
func (ux *UX) PrintInfo(message string) {
	fmt.Printf("%s %s\n", "💡", ux.colors.Info(message))
}

// PrintRAGInfo prints RAG-specific informational messages
func (ux *UX) PrintRAGInfo(message string) {
	fmt.Printf("%s %s\n", "🧠", ux.colors.RAG(message))
}

// PrintSuggestion prints command suggestions
func (ux *UX) PrintSuggestion(message string) {
	fmt.Printf("%s %s\n", "💡", ux.colors.Suggestion(message))
}

// ShowWelcomeBanner displays the Helix welcome banner
func (ux *UX) ShowWelcomeBanner(version string) {
	banner := `
██╗  ██╗███████╗██╗     ██╗██╗  ██╗
██║  ██║██╔════╝██║     ██║╚██╗██╔╝
███████║█████╗  ██║     ██║ ╚███╔╝ 
██╔══██║██╔══╝  ██║     ██║ ██╔██╗ 
██║  ██║███████╗███████╗██║██╔╝ ██╗
╚═╝  ╚═╝╚══════╝╚══════╝╚═╝╚═╝  ╚═╝
	`

	color.Cyan(banner)
	color.Cyan("🚀 Helix v%s - AI-Powered CLI Assistant", version)
	color.Cyan("📚 GitHub: https://github.com/Nibir1/Helix")
	fmt.Println()
}

// ShowHelp displays the help information
func (ux *UX) ShowHelp() {
	color.Cyan("📖 Helix — Command Overview")
	fmt.Println()

	// ============================================================
	// NATURAL LANGUAGE MODE (DEFAULT)
	// ============================================================
	color.Green("🤖 Natural Language Mode (Default)")
	fmt.Println("  Just type your request and Helix will:")
	fmt.Println("   • Understand your intent")
	fmt.Println("   • Plan steps")
	fmt.Println("   • Call tools automatically (shell / git / file ops)")
	fmt.Println("   • Execute actions when appropriate")
	fmt.Println()
	fmt.Println("  Examples:")
	fmt.Println("   • \"why is the sky blue?\"")
	fmt.Println("   • \"list all files in this folder\"")
	fmt.Println("   • \"update the version in the readme and commit it\"")
	fmt.Println("   • \"create a new branch, edit config, and push it\"")
	fmt.Println()

	// ============================================================
	// LEGACY SHORTCUT COMMANDS
	// (Optional – kept for power users)
	// ============================================================
	color.Yellow("⚡ Legacy Shortcuts (Optional)")
	fmt.Println("  /ask <question>       - Force classic AI chat")
	fmt.Println("  /cmd <request>        - Force command generation")
	fmt.Println("  /explain <command>    - Explain what a command does")
	fmt.Println("  /install <package>    - Install a package")
	fmt.Println("  /update <package>     - Update a package")
	fmt.Println("  /remove <package>     - Remove a package")
	fmt.Println("  /git <operation>      - Git helper (legacy)")
	fmt.Println()

	// ============================================================
	// SYSTEM / ADMIN COMMANDS
	// (Teacher requirement: only these use slash)
	// ============================================================
	color.Cyan("⚙️  System Commands (Program Control)")
	fmt.Println("  /help                 - Show this help screen")
	fmt.Println("  /exit                 - Quit Helix")
	fmt.Println("  /debug                - Show debug information")
	fmt.Println("  /online               - Check internet status")
	fmt.Println("  /sandbox <mode>       - Control directory restrictions")
	fmt.Println("  /cd <dir>             - Change directory (sandbox-aware)")
	fmt.Println()

	color.Magenta("🧠 RAG System Controls")
	fmt.Println("  /rag-status           - Show RAG initialization status")
	fmt.Println("  /rag-reindex          - Force rebuild RAG MAN page index")
	fmt.Println("  /rag-reset            - Reset RAG database completely")
	fmt.Println()

	// ============================================================
	// TIPS
	// ============================================================
	color.Green("💡 Tips:")
	fmt.Println("  • NoSlash = full natural-language automation")
	fmt.Println("  • SlashCommands = system control only (teacher requirement)")
	fmt.Println("  • Helix auto-detects when to run shell, git, reasoning, etc.")
	fmt.Println()

	color.Magenta("🎉 Helix Agent Mode is now your default interface.")
	fmt.Println("   Ask for anything — it will plan, reason, and execute.")
}

// ShowRAGStatus displays RAG system status information
func (ux *UX) ShowRAGStatus(stats map[string]interface{}) {
	color.Cyan("🧠 RAG System Status:")
	fmt.Println()

	color.Cyan("📊 Statistics:")
	color.Cyan("  • Initialized: %v", stats["initialized"])
	color.Cyan("  • Indexed MAN Pages: %v", stats["indexed_pages"])

	if stats["initialized"].(bool) {
		color.Green("✅ RAG System: ACTIVE")
		color.Cyan("  • Vector Documents: %v", stats["total_documents"])
		color.Cyan("  • Unique Commands: %v", stats["unique_commands"])
		color.Cyan("  • Index Size: %v terms", stats["index_size"])

		if indexedTime, ok := stats["indexed_time"]; ok {
			color.Cyan("  • Last Indexed: %v", indexedTime)
		}
	} else {
		indexingStatus := "UNKNOWN"
		if status, ok := stats["indexing_status"]; ok {
			indexingStatus = status.(string)
		}
		color.Yellow("🔄 RAG System: %s", indexingStatus)

		if stats["indexed_pages"].(int) > 0 {
			color.Cyan("  • Progress: %d pages indexed", stats["indexed_pages"])
		}
	}

	fmt.Println()
	color.Magenta("💡 RAG Features:")
	color.Magenta("  • Command suggestions before AI processing")
	color.Magenta("  • Enhanced prompts with MAN page context")
	color.Magenta("  • Accurate command explanations")
	color.Magenta("  • Automatic command documentation")
}

// ShowCommandSuggestions displays RAG-based command suggestions
func (ux *UX) ShowCommandSuggestions(suggestions []interface{}) {
	if len(suggestions) == 0 {
		return
	}

	color.Cyan("💡 RAG Command Suggestions:")
	fmt.Println()

	for i, suggestion := range suggestions {
		if i >= 3 { // Show top 3 suggestions
			break
		}

		if s, ok := suggestion.(map[string]interface{}); ok {
			command := s["command"].(string)
			description := s["description"].(string)
			confidence := s["confidence"].(float32)

			confidenceStr := fmt.Sprintf("%.0f%%", confidence*100)

			color.Cyan("  • %s - %s", ux.colors.Suggestion(command), description)
			color.Cyan("    Confidence: %s", confidenceStr)
		}
	}
	fmt.Println()
}

// ShowRAGIndexingProgress displays RAG indexing progress
func (ux *UX) ShowRAGIndexingProgress(elapsed time.Duration, pagesIndexed int) {
	color.Yellow("🔄 RAG indexing in progress...")
	color.Yellow("   Time elapsed: %v", ux.FormatDuration(elapsed))
	if pagesIndexed > 0 {
		color.Yellow("   Pages indexed: %d", pagesIndexed)
	}
}

// ShowRAGIndexingComplete displays RAG indexing completion message
func (ux *UX) ShowRAGIndexingComplete(duration time.Duration, totalPages int, totalCommands int) {
	color.Green("🎉 RAG system initialized!")
	color.Green("   Time: %s", ux.FormatDuration(duration))
	color.Green("   MAN Pages: %d", totalPages)
	color.Green("   Commands: %d", totalCommands)
	color.Green("   RAG features are now active! 🧠")
}

// ShowRAGIndexingTimeout displays RAG indexing timeout message
func (ux *UX) ShowRAGIndexingTimeout(duration time.Duration, pagesIndexed int) {
	color.Yellow("⏰ RAG indexing timeout after %s", ux.FormatDuration(duration))
	if pagesIndexed > 0 {
		color.Yellow("   Using %d partially indexed pages", pagesIndexed)
		color.Yellow("   RAG features may be limited")
	} else {
		color.Yellow("   No pages indexed - RAG features disabled")
	}
}

// ShowEnhancedPromptInfo displays information about RAG-enhanced prompts
func (ux *UX) ShowEnhancedPromptInfo(commandCount int) {
	color.Magenta("🎯 RAG-enhanced prompt with %d relevant commands", commandCount)
}

// ShowRAGActiveMessage displays when RAG system becomes active
func (ux *UX) ShowRAGActiveMessage() {
	color.Green("🎉 RAG system is now ACTIVE! Enhanced commands available.")
}

// ShowCommandExplanation displays a detailed command explanation
func (ux *UX) ShowCommandExplanation(command, explanation string) {
	color.Cyan("📖 Command Explanation: %s", command)
	fmt.Println()

	// Split explanation into lines and print with proper formatting
	lines := strings.Split(explanation, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			fmt.Println()
			continue
		}

		// Format different parts of the explanation
		switch {
		case strings.HasPrefix(line, "**") && strings.HasSuffix(line, "**"):
			// Bold headers
			cleanLine := strings.TrimPrefix(strings.TrimSuffix(line, "**"), "**")
			color.Cyan("%s", cleanLine)
		case strings.HasPrefix(line, "```bash"):
			// Code blocks
			color.Yellow("  %s", strings.TrimPrefix(line, "```bash"))
		case strings.HasPrefix(line, "```"):
			// End of code blocks
			continue
		case strings.HasPrefix(line, "  •"):
			// List items
			color.Green("  %s", line)
		default:
			// Regular text
			fmt.Println(line)
		}
	}
}

// PrintCommandBreakdown displays a detailed breakdown of command components
func (ux *UX) PrintCommandBreakdown(breakdown map[string]string) {
	color.Cyan("📖 Command Breakdown:")
	fmt.Println()

	for component, explanation := range breakdown {
		color.Cyan("  %s: %s", component, explanation)
	}
	fmt.Println()
}

// FormatDuration formats a duration for human readability
func (ux *UX) FormatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}

	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	hours := int(d.Hours())

	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	return fmt.Sprintf("%dm %ds", minutes, seconds)
}

// ProgressBar shows a simple progress bar
func (ux *UX) ProgressBar(total int, description string) func() {
	fmt.Printf("%s [", description)
	progress := 0

	return func() {
		if progress < total {
			fmt.Print("█")
			progress++
		}
		if progress == total {
			fmt.Println("] ✅")
		}
	}
}

// ShowLoadingAnimation shows a simple loading animation
func (ux *UX) ShowLoadingAnimation(message string, done chan bool) {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	i := 0

	go func() {
		for {
			select {
			case <-done:
				fmt.Print("\r\033[K") // Clear line
				return
			default:
				fmt.Printf("\r%s %s", frames[i], message)
				i = (i + 1) % len(frames)
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
}

// PrintTable prints a simple table format
func (ux *UX) PrintTable(headers []string, rows [][]string) {
	// Calculate column widths
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(header)
	}

	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Print headers
	for i, header := range headers {
		fmt.Printf("%-*s", widths[i]+2, ux.colors.Info(header))
	}
	fmt.Println()

	// Print separator
	for i, width := range widths {
		fmt.Printf("%-*s", width+2, strings.Repeat("-", width))
		if i < len(widths)-1 {
			fmt.Print("  ")
		}
	}
	fmt.Println()

	// Print rows
	for _, row := range rows {
		for i, cell := range row {
			fmt.Printf("%-*s", widths[i]+2, cell)
		}
		fmt.Println()
	}
}

// PrintKeyValue prints key-value pairs in a formatted way
func (ux *UX) PrintKeyValue(data map[string]interface{}) {
	maxKeyLength := 0
	for key := range data {
		if len(key) > maxKeyLength {
			maxKeyLength = len(key)
		}
	}

	for key, value := range data {
		padding := strings.Repeat(" ", maxKeyLength-len(key))
		color.Cyan("  %s:%s %v", key, padding, value)
	}
}

// SetTypingSpeed adjusts the typing animation speed
func (ux *UX) SetTypingSpeed(speed time.Duration) {
	ux.typingSpeed = speed
}

// formatResponse cleans and formats AI responses
func (ux *UX) formatResponse(text string) string {
	// Remove excessive whitespace
	text = strings.TrimSpace(text)

	// Remove common AI prefixes
	prefixes := []string{
		"Assistant:", "AI:", "Helix:", "Response:",
		"Here's the command:", "The command is:",
	}

	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			text = strings.TrimPrefix(text, prefix)
			text = strings.TrimSpace(text)
		}
	}

	return text
}

// PrintRAGRetrievalInfo displays RAG retrieval information
func (ux *UX) PrintRAGRetrievalInfo(query string, resultCount int, retrievalTime time.Duration) {
	if resultCount > 0 {
		color.Cyan("🔍 RAG Retrieval for: %s", query)
		color.Green("✅ Found %d relevant documents", resultCount)
		color.Green("✅ RAG retrieved %d commands in %s", resultCount, ux.FormatDuration(retrievalTime))
	} else {
		color.Cyan("🔍 RAG Retrieval for: %s", query)
		color.Yellow("💡 No relevant command context found")
	}
}

// PrintRAGEnhancedPromptInfo displays when RAG enhances a prompt
func (ux *UX) PrintRAGEnhancedPromptInfo(commandCount int) {
	if commandCount > 0 {
		color.Magenta("🎯 Enhancing prompt with %d relevant commands", commandCount)
		color.Magenta("🎯 RAG-enhanced prompt generated with command context")
	}
}

// PrintDebugInfo displays debug information for development
func (ux *UX) PrintDebugInfo(message string) {
	color.Yellow("🔍 DEBUG: %s", message)
}
