package ux

import (
	"fmt"
	"strings"
	"time"

	"github.com/fatih/color"
)

// UX provides enhanced sci-fi themed user experience features
type UX struct {
	typingSpeed    time.Duration
	colors         *ColorScheme
	scifiMode      bool
	animationSpeed time.Duration
}

// ColorScheme holds sci-fi themed color configurations
type ColorScheme struct {
	Primary   func(a ...interface{}) string
	Secondary func(a ...interface{}) string
	Accent    func(a ...interface{}) string
	Success   func(a ...interface{}) string
	Error     func(a ...interface{}) string
	Warning   func(a ...interface{}) string
	Info      func(a ...interface{}) string
	System    func(a ...interface{}) string
	Neutral   func(a ...interface{}) string
	Highlight func(a ...interface{}) string
}

// NewUX creates a new sci-fi themed UX manager
func NewUX() *UX {
	return &UX{
		typingSpeed:    25 * time.Millisecond,
		animationSpeed: 120 * time.Millisecond,
		scifiMode:      true,
		colors: &ColorScheme{
			Primary:   color.New(color.FgHiCyan, color.Bold).SprintFunc(),
			Secondary: color.New(color.FgHiBlue).SprintFunc(),
			Accent:    color.New(color.FgHiMagenta, color.Bold).SprintFunc(),
			Success:   color.New(color.FgHiGreen, color.Bold).SprintFunc(),
			Error:     color.New(color.FgHiRed, color.Bold).SprintFunc(),
			Warning:   color.New(color.FgHiYellow, color.Bold).SprintFunc(),
			Info:      color.New(color.FgHiWhite).SprintFunc(),
			System:    color.New(color.FgHiGreen).SprintFunc(),
			Neutral:   color.New(color.FgHiBlack).SprintFunc(),
			Highlight: color.New(color.FgHiWhite, color.BgBlue, color.Bold).SprintFunc(),
		},
	}
}

// Typewriter prints text with enhanced sci-fi typing animation
func (ux *UX) Typewriter(text string) {
	runes := []rune(text)
	n := len(runes)

	// Adaptive speed based on content length
	baseDelay := ux.typingSpeed
	if n > 400 {
		baseDelay = 8 * time.Millisecond
	} else if n > 200 {
		baseDelay = 15 * time.Millisecond
	}

	for i, c := range runes {
		fmt.Printf("%c", c)

		// Enhanced pauses for dramatic effect
		switch {
		case c == '\n':
			time.Sleep(baseDelay * 4)
		case strings.ContainsRune(".!?", c):
			time.Sleep(baseDelay * 8)
		case strings.ContainsRune(",;:", c):
			time.Sleep(baseDelay * 3)
		default:
			// Add slight randomness for natural feel
			variation := time.Duration(i%13) * time.Millisecond
			time.Sleep(baseDelay + variation - (2 * time.Millisecond))
		}
	}
	fmt.Println()
}

// PrintSystemMessage displays system-level messages with sci-fi styling
func (ux *UX) PrintSystemMessage(text string) {
	ux.scifiPrint("🛸", "SYSTEM", text, ux.colors.System)
}

// PrintAIMessage displays AI responses with neural network theme
func (ux *UX) PrintAIMessage(text string, useTypingEffect bool) {
	prefix := ux.scifiPrefix("🧠", "NEURAL_NET", ux.colors.Primary)
	fmt.Print(prefix)

	if useTypingEffect {
		ux.Typewriter(text)
	} else {
		fmt.Println(text)
	}
}

// PrintCommand displays command execution with cyberpunk style
func (ux *UX) PrintCommand(command string) {
	ux.scifiPrint("⚡", "EXEC", command, ux.colors.Secondary)
}

// PrintData displays data output with database theme
func (ux *UX) PrintData(data string) {
	ux.scifiPrint("💾", "DATA", data, ux.colors.Info)
}

// PrintSuccess displays success messages with positive sci-fi theme
func (ux *UX) PrintSuccess(message string) {
	ux.scifiPrint("✅", "SUCCESS", message, ux.colors.Success)
}

// PrintError displays error messages with alert theme
func (ux *UX) PrintError(message string) {
	ux.scifiPrint("🚨", "ERROR", message, ux.colors.Error)
}

// PrintWarning displays warning messages with caution theme
func (ux *UX) PrintWarning(message string) {
	ux.scifiPrint("⚠️", "WARNING", message, ux.colors.Warning)
}

// PrintInfo displays informational messages
func (ux *UX) PrintInfo(message string) {
	ux.scifiPrint("ℹ️", "INFO", message, ux.colors.Info)
}

// PrintDebug displays debug information
func (ux *UX) PrintDebug(message string) {
	ux.scifiPrint("🔧", "DEBUG", message, ux.colors.Neutral)
}

// PrintRAGResponse displays RAG-enhanced responses with knowledge theme
func (ux *UX) PrintRAGResponse(text string, useTypingEffect bool) {
	prefix := ux.scifiPrefix("📚", "KNOWLEDGE_BASE", ux.colors.Accent)
	fmt.Print(prefix)

	if useTypingEffect {
		ux.Typewriter(text)
	} else {
		fmt.Println(text)
	}
}

// ShowWelcomeBanner displays sci-fi welcome banner
func (ux *UX) ShowWelcomeBanner(version string) {
	banner := `
╔════════════════════════════════════════════════════════════════╗
║                                                                ║
║            ██╗  ██╗███████╗██╗     ██╗██╗  ██╗                 ║
║            ██║  ██║██╔════╝██║     ██║╚██╗██╔╝                 ║
║            ███████║█████╗  ██║     ██║ ╚███╔╝                  ║
║            ██╔══██║██╔══╝  ██║     ██║ ██╔██╗                  ║
║            ██║  ██║███████╗███████╗██║██╔╝ ██╗                 ║
║            ╚═╝  ╚═╝╚══════╝╚══════╝╚═╝╚═╝  ╚═╝                 ║
║                                                                ║
║       🚀 Helix v` + version + ` - AI-Powered CLI Assistant            ║
║                  Author - Nahasat Nibir                       ║
║       📚 GitHub: https://github.com/Nibir1/Helix            ║
║                                                                ║
╚════════════════════════════════════════════════════════════════╝
`

	color.Cyan(banner)
	fmt.Println()
}

// ShowHelp displays sci-fi styled help information
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
	fmt.Println("  /rag-rebuild          - Rebuild RAG index from scratch")
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

// ShowRAGStatus displays RAG system status with sci-fi theme
func (ux *UX) ShowRAGStatus(stats map[string]interface{}) {
	ux.sectionHeader("KNOWLEDGE BASE STATUS")

	status := ux.colors.Error("OFFLINE")
	if stats["initialized"].(bool) {
		status = ux.colors.Success("ONLINE")
	}

	ux.PrintSystemMessage(fmt.Sprintf("Knowledge Base: %s", status))
	ux.PrintData(fmt.Sprintf("Indexed Documents: %v", stats["indexed_pages"]))
	ux.PrintData(fmt.Sprintf("Neural Connections: %v", stats["unique_commands"]))
	ux.PrintData(fmt.Sprintf("Memory Allocation: %v terms", stats["index_size"]))

	if indexedTime, ok := stats["indexed_time"]; ok {
		ux.PrintDebug(fmt.Sprintf("Last Sync: %v", indexedTime))
	}
}

// ShowCommandSuggestions displays RAG-based command suggestions
func (ux *UX) ShowCommandSuggestions(suggestions []interface{}) {
	if len(suggestions) == 0 {
		return
	}

	ux.sectionHeader("NEURAL SUGGESTIONS")
	for i, suggestion := range suggestions {
		if i >= 3 {
			break
		}

		if s, ok := suggestion.(map[string]interface{}); ok {
			command := s["command"].(string)
			description := s["description"].(string)
			confidence := s["confidence"].(float32)

			ux.PrintInfo(fmt.Sprintf("Command: %s", ux.colors.Highlight(command)))
			ux.PrintData(fmt.Sprintf("Description: %s", description))
			ux.PrintDebug(fmt.Sprintf("Neural Confidence: %.0f%%", confidence*100))
			fmt.Println()
		}
	}
}

// ProgressBar shows a sci-fi styled progress bar
func (ux *UX) ProgressBar(total int, description string) func() {
	fmt.Printf("%s [", ux.colors.System(description))
	progress := 0
	chars := []string{"▱", "▰"}

	return func() {
		if progress < total {
			fmt.Print(ux.colors.Primary(chars[1]))
			progress++
		}
		if progress == total {
			fmt.Println("] " + ux.colors.Success("COMPLETE"))
		}
	}
}

// ShowLoadingAnimation shows a sci-fi loading animation
func (ux *UX) ShowLoadingAnimation(message string, done chan bool) {
	frames := []string{"⡿", "⣟", "⣯", "⣷", "⣾", "⣽", "⣻", "⢿"}
	i := 0

	go func() {
		for {
			select {
			case <-done:
				fmt.Print("\r\033[K")
				return
			default:
				fmt.Printf("\r%s %s %s",
					ux.colors.Primary(frames[i]),
					ux.colors.System(message),
					ux.scifiDots(i))
				i = (i + 1) % len(frames)
				time.Sleep(ux.animationSpeed)
			}
		}
	}()
}

// PrintTable prints a sci-fi styled table
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

	// Print headers with sci-fi style
	fmt.Print("┌")
	for i, width := range widths {
		fmt.Print("─" + strings.Repeat("─", width) + "─")
		if i < len(widths)-1 {
			fmt.Print("┬")
		}
	}
	fmt.Println("┐")

	// Headers
	fmt.Print("│")
	for i, header := range headers {
		fmt.Printf(" %-*s │", widths[i], ux.colors.Primary(header))
	}
	fmt.Println()

	// Separator
	fmt.Print("├")
	for i, width := range widths {
		fmt.Print("─" + strings.Repeat("─", width) + "─")
		if i < len(widths)-1 {
			fmt.Print("┼")
		}
	}
	fmt.Println("┤")

	// Rows
	for _, row := range rows {
		fmt.Print("│")
		for i, cell := range row {
			fmt.Printf(" %-*s │", widths[i], cell)
		}
		fmt.Println()
	}

	// Footer
	fmt.Print("└")
	for i, width := range widths {
		fmt.Print("─" + strings.Repeat("─", width) + "─")
		if i < len(widths)-1 {
			fmt.Print("┴")
		}
	}
	fmt.Println("┘")
}

// Helper methods
func (ux *UX) scifiPrint(emoji, label, text string, colorFunc func(...interface{}) string) {
	fmt.Printf("%s %s %s\n",
		emoji,
		ux.scifiLabel(label),
		colorFunc(text))
}

func (ux *UX) scifiPrefix(emoji, label string, colorFunc func(...interface{}) string) string {
	return fmt.Sprintf("%s %s → ",
		emoji,
		colorFunc(label))
}

func (ux *UX) scifiLabel(label string) string {
	return ux.colors.Neutral("[" + label + "]")
}

func (ux *UX) sectionHeader(title string) {
	line := strings.Repeat("─", len(title)+4)
	fmt.Printf("%s %s %s\n",
		ux.colors.Primary("┌"+line+"┐"),
		ux.colors.Highlight(title),
		ux.colors.Primary("└"+line+"┘"))
}

func (ux *UX) scifiDots(frame int) string {
	dots := []string{"   ", ".  ", ".. ", "..."}
	return ux.colors.Neutral(dots[frame%len(dots)])
}

// SetAnimationSpeed adjusts animation speeds
func (ux *UX) SetAnimationSpeed(speed time.Duration) {
	ux.animationSpeed = speed
}

// SetTypingSpeed adjusts typing animation speed
func (ux *UX) SetTypingSpeed(speed time.Duration) {
	ux.typingSpeed = speed
}

// FormatDuration formats duration in sci-fi style
func (ux *UX) FormatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm %ds", int(d.Minutes())%60, int(d.Seconds())%60)
}
