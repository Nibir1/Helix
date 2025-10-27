// ux.go
package main

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
		},
	}
}

// Typewriter prints text with a typing effect
func (ux *UX) Typewriter(text string) {
	for _, char := range text {
		fmt.Print(string(char))
		time.Sleep(ux.typingSpeed)
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
	color.Cyan("📖 Helix Commands:")
	fmt.Println()

	color.Yellow("🤖 AI Commands:")
	fmt.Println("  /ask <question>     - Ask the AI a question")
	fmt.Println("  /cmd <request>      - Generate and execute commands from natural language")
	fmt.Println("  /git <request>      - Process natural language git requests")
	fmt.Println("  /explain <command>  - Explain what a command does")
	fmt.Println()

	color.Yellow("📦 Package Management:")
	fmt.Println("  /install <package>  - Install a package")
	fmt.Println("  /update <package>   - Update a package")
	fmt.Println("  /remove <package>   - Remove a package")
	fmt.Println()

	color.Yellow("⚙️  System Commands:")
	fmt.Println("  /debug              - Show debug information")
	fmt.Println("  /test-ai            - Test /ask AI feature")
	fmt.Println("  /online             - Check internet connectivity")
	fmt.Println("  /dry-run            - Toggle dry-run mode")
	fmt.Println("  /help               - Show this help message")
	fmt.Println("  /exit               - Exit Helix")
	fmt.Println()

	color.Green("💡 Examples:")
	fmt.Println("  /ask 'how do I list files in a directory?'")
	fmt.Println("  /cmd 'show me what's in the current folder'")
	fmt.Println("  /install git")
	fmt.Println("  /explain 'rm -rf node_modules'")
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

// ProgressBar shows a simple progress bar
func (ux *UX) ProgressBar(total int, description string) func() {
	fmt.Printf("%s [", description)

	return func() {
		fmt.Print("█")
	}
}

// SetTypingSpeed adjusts the typing animation speed
func (ux *UX) SetTypingSpeed(speed time.Duration) {
	ux.typingSpeed = speed
}
