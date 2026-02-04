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
	outputHandler  func(text string) // Callback for TUI/Headless mode
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

// SetOutputHandler sets a custom output handler for UX (enables Headless/TUI mode)
func (ux *UX) SetOutputHandler(handler func(string)) {
	ux.outputHandler = handler
}

// Helper to route output either to stdout or the handler
func (ux *UX) print(text string) {
	if ux.outputHandler != nil {
		ux.outputHandler(text)
		return
	}
	fmt.Println(text)
}

// Typewriter prints text with enhanced sci-fi typing animation
// In TUI mode, it skips the delay and sends the text immediately.
func (ux *UX) Typewriter(text string) {
	// TUI Mode: Skip blocking sleeps, let the Viewport handle rendering
	if ux.outputHandler != nil {
		ux.outputHandler(text)
		return
	}

	// CLI Mode: Run animation
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

	if ux.outputHandler != nil {
		// In TUI mode, combine prefix and text and send
		ux.outputHandler(prefix + text)
		return
	}

	// CLI Mode
	fmt.Print(prefix)
	if useTypingEffect {
		ux.Typewriter(text)
	} else {
		fmt.Println(text)
	}
}

// PrintRAGResponse displays RAG-enhanced responses with knowledge theme
func (ux *UX) PrintRAGResponse(text string, useTypingEffect bool) {
	prefix := ux.scifiPrefix("📚", "KNOWLEDGE_BASE", ux.colors.Accent)

	if ux.outputHandler != nil {
		ux.outputHandler(prefix + text)
		return
	}

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
	if ux.outputHandler != nil {
		ux.outputHandler(color.CyanString(banner))
	} else {
		color.Cyan(banner)
		fmt.Println()
	}
}

// ShowHelp displays sci-fi styled help information
func (ux *UX) ShowHelp() {
	var b strings.Builder

	b.WriteString(color.CyanString("📖 Helix — Command Overview\n\n"))

	b.WriteString(color.GreenString("🤖 Natural Language Mode (Default)\n"))
	b.WriteString("  Just type your request and Helix will:\n")
	b.WriteString("   • Understand your intent\n")
	b.WriteString("   • Plan steps\n")
	b.WriteString("   • Call tools automatically (shell / git / file ops)\n")
	b.WriteString("   • Execute actions when appropriate\n\n")

	b.WriteString("  Examples:\n")
	b.WriteString("   • \"why is the sky blue?\"\n")
	b.WriteString("   • \"list all files in this folder\"\n")
	b.WriteString("   • \"update the version in the readme and commit it\"\n\n")

	b.WriteString(color.CyanString("⚙️  System Commands (Program Control)\n"))
	b.WriteString("  /help                 - Show this help screen\n")
	b.WriteString("  /exit                 - Quit Helix\n")
	b.WriteString("  /debug                - Show debug information\n")
	b.WriteString("  /sandbox <mode>       - Control directory restrictions\n\n")

	b.WriteString(color.MagentaString("🧠 RAG System Controls\n"))
	b.WriteString("  /rag-status           - Show RAG initialization status\n\n")

	b.WriteString(color.GreenString("💡 Tips:\n"))
	b.WriteString("  • NoSlash = full natural-language automation\n")
	b.WriteString("  • SlashCommands = system control only\n\n")

	b.WriteString(color.MagentaString("🎉 Helix Agent Mode is now your default interface."))

	ux.print(b.String())
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
			if ux.outputHandler == nil {
				fmt.Println()
			}
		}
	}
}

// ProgressBar shows a sci-fi styled progress bar
// Note: In TUI mode, complex dynamic progress bars are difficult to stream via single messages.
// This simplifies to text output for TUI, or retains standard behavior for CLI.
func (ux *UX) ProgressBar(total int, description string) func() {
	if ux.outputHandler != nil {
		// TUI Mode: Simple notification
		ux.outputHandler(fmt.Sprintf("%s [Start]", description))
		return func() {
			ux.outputHandler(fmt.Sprintf("%s [%s]", description, ux.colors.Success("COMPLETE")))
		}
	}

	// CLI Mode
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
// Skipped in TUI mode to avoid breaking layout/event loop
func (ux *UX) ShowLoadingAnimation(message string, done chan bool) {
	if ux.outputHandler != nil {
		// TUI Mode: Just print the start message. The TUI has its own spinner.
		ux.outputHandler(fmt.Sprintf("%s %s...", "⣻", message))
		return
	}

	// CLI Mode
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

	var b strings.Builder

	// Top Border
	b.WriteString("┌")
	for i, width := range widths {
		b.WriteString("─" + strings.Repeat("─", width) + "─")
		if i < len(widths)-1 {
			b.WriteString("┬")
		}
	}
	b.WriteString("┐\n")

	// Headers
	b.WriteString("│")
	for i, header := range headers {
		b.WriteString(fmt.Sprintf(" %-*s │", widths[i], ux.colors.Primary(header)))
	}
	b.WriteString("\n")

	// Separator
	b.WriteString("├")
	for i, width := range widths {
		b.WriteString("─" + strings.Repeat("─", width) + "─")
		if i < len(widths)-1 {
			b.WriteString("┼")
		}
	}
	b.WriteString("┤\n")

	// Rows
	for _, row := range rows {
		b.WriteString("│")
		for i, cell := range row {
			b.WriteString(fmt.Sprintf(" %-*s │", widths[i], cell))
		}
		b.WriteString("\n")
	}

	// Footer
	b.WriteString("└")
	for i, width := range widths {
		b.WriteString("─" + strings.Repeat("─", width) + "─")
		if i < len(widths)-1 {
			b.WriteString("┴")
		}
	}
	b.WriteString("┘")

	ux.print(b.String())
}

// Helper methods

func (ux *UX) scifiPrint(emoji, label, text string, colorFunc func(...interface{}) string) {
	msg := fmt.Sprintf("%s %s %s", emoji, ux.scifiLabel(label), colorFunc(text))
	ux.print(msg)
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
	msg := fmt.Sprintf("%s %s %s",
		ux.colors.Primary("┌"+line+"┐"),
		ux.colors.Highlight(title),
		ux.colors.Primary("└"+line+"┘"))
	ux.print(msg)
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
