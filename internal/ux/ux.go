// internal/ux/ux.go

package ux

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
)

// ConfirmationRequest is sent to the TUI when the Agent needs user input.
// This allows the Agent to pause and wait for a UI response (Y/N).
type ConfirmationRequest struct {
	Question  string
	ReplyChan chan bool // The TUI sends true/false back here
}

// UX provides enhanced sci-fi themed user experience features.
type UX struct {
	typingSpeed    time.Duration
	colors         *ColorScheme
	scifiMode      bool
	animationSpeed time.Duration

	// eventHandler replaces the simple outputHandler.
	// It accepts any event type (string logs or struct requests).
	eventHandler func(interface{})
}

// ColorScheme holds sci-fi themed color configurations.
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

// NewUX creates a new sci-fi themed UX manager.
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

// SetEventHandler sets a custom handler for all UX events (Text logs or Interactive Requests).
// This enables the TUI to react to the Agent's needs.
func (ux *UX) SetEventHandler(handler func(interface{})) {
	ux.eventHandler = handler
}

// AskYesNo prompts the user for confirmation.
// In TUI mode, it sends a request and BLOCKS until the UI replies.
// In CLI mode, it uses standard stdin.
func (ux *UX) AskYesNo(question string) bool {
	if ux.eventHandler != nil {
		// TUI MODE:
		// 1. Create a channel for the answer
		replyCh := make(chan bool)

		// 2. Send the request to the UI
		ux.eventHandler(ConfirmationRequest{
			Question:  question,
			ReplyChan: replyCh,
		})

		// 3. BLOCK here until the UI (user) responds via the channel
		return <-replyCh
	}

	// CLI MODE (Legacy/Fallback):
	color.Yellow("%s [y/N]: ", question)
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}

// Helper to route output either to stdout or the event handler
func (ux *UX) print(text string) {
	if ux.eventHandler != nil {
		// In TUI mode, plain text is just one type of event
		ux.eventHandler(text)
		return
	}
	fmt.Println(text)
}

// Typewriter prints text with enhanced sci-fi typing animation.
// In TUI mode, it skips the delay and sends the text immediately.
func (ux *UX) Typewriter(text string) {
	if ux.eventHandler != nil {
		ux.eventHandler(text)
		return
	}

	// CLI Mode: Run animation
	runes := []rune(text)
	n := len(runes)

	baseDelay := ux.typingSpeed
	if n > 400 {
		baseDelay = 8 * time.Millisecond
	} else if n > 200 {
		baseDelay = 15 * time.Millisecond
	}

	for i, c := range runes {
		fmt.Printf("%c", c)

		switch {
		case c == '\n':
			time.Sleep(baseDelay * 4)
		case strings.ContainsRune(".!?", c):
			time.Sleep(baseDelay * 8)
		case strings.ContainsRune(",;:", c):
			time.Sleep(baseDelay * 3)
		default:
			variation := time.Duration(i%13) * time.Millisecond
			time.Sleep(baseDelay + variation - (2 * time.Millisecond))
		}
	}
	fmt.Println()
}

// PrintSystemMessage displays system-level messages.
func (ux *UX) PrintSystemMessage(text string) {
	ux.scifiPrint("SYSTEM", text, ux.colors.System)
}

// PrintAIMessage displays AI responses.
func (ux *UX) PrintAIMessage(text string, useTypingEffect bool) {
	prefix := ux.scifiPrefix("[NEURAL_NET]", ux.colors.Primary)

	if ux.eventHandler != nil {
		ux.eventHandler(prefix + text)
		return
	}

	fmt.Print(prefix)
	if useTypingEffect {
		ux.Typewriter(text)
	} else {
		fmt.Println(text)
	}
}

// PrintRAGResponse displays RAG-enhanced responses.
func (ux *UX) PrintRAGResponse(text string, useTypingEffect bool) {
	prefix := ux.scifiPrefix("KNOWLEDGE_BASE", ux.colors.Accent)

	if ux.eventHandler != nil {
		ux.eventHandler(prefix + text)
		return
	}

	fmt.Print(prefix)
	if useTypingEffect {
		ux.Typewriter(text)
	} else {
		fmt.Println(text)
	}
}

// PrintCommand displays command execution.
func (ux *UX) PrintCommand(command string) {
	ux.scifiPrint("EXEC", command, ux.colors.Secondary)
}

// PrintData displays data output.
func (ux *UX) PrintData(data string) {
	ux.scifiPrint("DATA", data, ux.colors.Info)
}

// PrintSuccess displays success messages.
func (ux *UX) PrintSuccess(message string) {
	ux.scifiPrint("SUCCESS", message, ux.colors.Success)
}

// PrintError displays error messages.
func (ux *UX) PrintError(message string) {
	ux.scifiPrint("ERROR", message, ux.colors.Error)
}

// PrintWarning displays warning messages.
func (ux *UX) PrintWarning(message string) {
	ux.scifiPrint("WARNING", message, ux.colors.Warning)
}

// PrintInfo displays informational messages.
func (ux *UX) PrintInfo(message string) {
	ux.scifiPrint("INFO", message, ux.colors.Info)
}

// PrintDebug displays debug information.
func (ux *UX) PrintDebug(message string) {
	ux.scifiPrint("DEBUG", message, ux.colors.Neutral)
}

// ShowWelcomeBanner displays sci-fi welcome banner.
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
║       Helix v` + version + ` - AI-Powered CLI Assistant            ║
║                  Creator - Nahasat Nibir                       ║
║       GitHub: https://github.com/Nibir1/Helix            ║
║                                                                ║
╚════════════════════════════════════════════════════════════════╝
`
	if ux.eventHandler != nil {
		ux.eventHandler(color.CyanString(banner))
	} else {
		color.Cyan(banner)
		fmt.Println()
	}
}

// ShowHelp displays sci-fi styled help information.
func (ux *UX) ShowHelp() {
	var b strings.Builder

	b.WriteString(color.CyanString("Helix — Command Overview\n\n"))
	b.WriteString(color.GreenString("Natural Language Mode (Default)\n"))
	b.WriteString("  Just type your request and Helix will:\n")
	b.WriteString("   • Understand your intent\n")
	b.WriteString("   • Plan steps\n")
	b.WriteString("   • Call tools automatically (shell / git / file ops)\n")
	b.WriteString("   • Execute actions when appropriate\n\n")

	b.WriteString(color.CyanString("System Commands (Program Control)\n"))
	b.WriteString("  /help                 - Show this help screen\n")
	b.WriteString("  /exit                 - Quit Helix\n")
	b.WriteString("  /debug                - Show debug information\n")
	b.WriteString("  /sandbox <mode>       - Control directory restrictions\n\n")

	b.WriteString(color.MagentaString("RAG System Controls\n"))
	b.WriteString("  /rag-status           - Show RAG initialization status\n\n")

	b.WriteString(color.MagentaString("Helix Agent Mode is now your default interface."))

	ux.print(b.String())
}

// ShowRAGStatus displays RAG system status.
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

// ShowCommandSuggestions displays RAG-based command suggestions.
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
			if ux.eventHandler == nil {
				fmt.Println()
			}
		}
	}
}

// ProgressBar shows a sci-fi styled progress bar.
func (ux *UX) ProgressBar(total int, description string) func() {
	if ux.eventHandler != nil {
		ux.eventHandler(fmt.Sprintf("%s [Start]", description))
		return func() {
			ux.eventHandler(fmt.Sprintf("%s [%s]", description, ux.colors.Success("COMPLETE")))
		}
	}

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

// ShowLoadingAnimation shows a sci-fi loading animation.
func (ux *UX) ShowLoadingAnimation(message string, done chan bool) {
	if ux.eventHandler != nil {
		ux.eventHandler(fmt.Sprintf("%s %s...", "⣻", message))
		return
	}

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

// PrintTable prints a sci-fi styled table.
func (ux *UX) PrintTable(headers []string, rows [][]string) {
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

func (ux *UX) scifiPrint(label, text string, colorFunc func(...interface{}) string) {
	msg := fmt.Sprintf("%s %s", ux.scifiLabel(label), colorFunc(text))
	ux.print(msg)
}

func (ux *UX) scifiPrefix(label string, colorFunc func(...interface{}) string) string {
	return fmt.Sprintf("%s → ", colorFunc(label))
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

func (ux *UX) SetAnimationSpeed(speed time.Duration) {
	ux.animationSpeed = speed
}

func (ux *UX) SetTypingSpeed(speed time.Duration) {
	ux.typingSpeed = speed
}

func (ux *UX) FormatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm %ds", int(d.Minutes())%60, int(d.Seconds())%60)
}

// TextRequest is sent to the TUI when the Agent needs free-form text input.
// Phase 0: used to route typed confirmations and branch prompts through the TUI.
type TextRequest struct {
	Prompt    string
	ReplyChan chan string
}

// AskLine prompts the user for a single line of text.
// In TUI mode, it blocks until the TUI returns a response.
func (ux *UX) AskLine(prompt string) string {
	if ux.eventHandler != nil {
		reply := make(chan string)

		ux.eventHandler(TextRequest{
			Prompt:    prompt,
			ReplyChan: reply,
		})

		return <-reply
	}

	fmt.Printf("%s: ", prompt)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return ""
	}

	return strings.TrimSpace(line)
}

// AskTypedConfirmation requires the user to type an exact phrase.
func (ux *UX) AskTypedConfirmation(label, requiredPhrase string) bool {
	prompt := fmt.Sprintf(
		"HIGH-RISK operation: %s. Type %q to confirm",
		label,
		requiredPhrase,
	)

	response := ux.AskLine(prompt)
	return response == requiredPhrase
}
