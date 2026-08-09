// internal/ux/ux.go
//
// Purpose: Terminal UX layer for Helix. Handles colored output, prompts,
// confirmation flow, and the typewriter effect.
package ux

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"helix/internal/audio"
	"helix/internal/utils"

	"github.com/fatih/color"
)

// UX owns terminal presentation and user interaction.
type UX struct {
	typingSpeed    time.Duration
	animationSpeed time.Duration
	colors         *ColorScheme
	typewriteAll   bool
}

// ColorScheme centralizes Helix terminal colors.
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

// NewUX creates a UX layer with Helix defaults.
//
// Args: none.
// Returns: *UX.
// Complexity: O(1).
func NewUX() *UX {
	return &UX{
		typingSpeed:    25 * time.Millisecond,
		animationSpeed: 120 * time.Millisecond,
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

// SetTypewriteAll toggles the global typewriter effect for all output.
//
// Args: on: true to typewrite everything, false for AI-only.
// Returns: none. Complexity: O(1).
func (ux *UX) SetTypewriteAll(on bool) {
	ux.typewriteAll = on
}

// AskYesNo asks a yes/no question.
//
// Args:
//   - question: prompt text.
//
// Returns: bool.
// Complexity: O(1), plus stdin read time.
func (ux *UX) AskYesNo(question string) bool {
	fmt.Printf("%s [y/N]: ", question)

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))

	return response == "y" || response == "yes"
}

// AskLine reads one line of user input.
//
// Args:
//   - prompt: prompt text.
//
// Returns: string.
// Complexity: O(1), plus stdin read time.
func (ux *UX) AskLine(prompt string) string {
	fmt.Printf("%s: ", prompt)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return ""
	}

	return strings.TrimSpace(line)
}

// AskTypedConfirmation requires an exact typed phrase.
//
// Args:
//   - label: human-readable operation label.
//   - requiredPhrase: exact phrase the user must type.
//
// Returns: bool.
// Complexity: O(1), plus stdin read time.
func (ux *UX) AskTypedConfirmation(label, requiredPhrase string) bool {
	prompt := fmt.Sprintf("HIGH-RISK operation: %s. Type %q to confirm", label, requiredPhrase)
	response := ux.AskLine(prompt)

	return response == requiredPhrase
}

// Typewriter renders AI text with a typing effect and synchronized audio.
//
// Args:
//   - text: text to render.
//
// Returns: none.
// Complexity: O(len(text)), plus sleep-based animation time.
func (ux *UX) Typewriter(text string) {
	runes := []rune(text)
	n := len(runes)

	baseDelay := ux.typingSpeed

	// Long responses type faster so the UX remains responsive.
	if n > 400 {
		baseDelay = 8 * time.Millisecond
	} else if n > 200 {
		baseDelay = 15 * time.Millisecond
	}

	for i, c := range runes {
		// Audio is synchronized with visible characters only.
		// Spaces and newlines should not produce ticks.
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			audio.PlayType()
		}

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

// PrintSystemMessage prints a system-level message.
//
// Args:
//   - text: message text.
//
// Returns: none.
// Complexity: O(1).
func (ux *UX) PrintSystemMessage(text string) {
	ux.scifiPrint("SYSTEM", text, ux.colors.System)
}

// PrintAIMessage prints an AI response.
func (ux *UX) PrintAIMessage(text string, useTypingEffect bool) {
	prefix := ux.scifiPrefix("[NEURAL_NET]", ux.colors.Primary)
	if ux.typewriteAll {
		// Phase 15: Typewrite the prefix and text together
		ux.Typewriter(prefix + text)
	} else {
		fmt.Print(prefix)
		if useTypingEffect {
			ux.Typewriter(text)
		} else {
			fmt.Println(text)
		}
	}
}

// PrintCommand prints a command execution header.
//
// Args:
//   - command: command text.
//
// Returns: none.
// Complexity: O(1).
func (ux *UX) PrintCommand(command string) {
	ux.scifiPrint("EXEC", command, ux.colors.Secondary)
}

// PrintData prints structured data output.
//
// Args:
//   - data: data text.
//
// Returns: none.
// Complexity: O(1).
func (ux *UX) PrintData(data string) {
	ux.scifiPrint("DATA", data, ux.colors.Info)
}

// PrintSuccess prints a success message.
//
// Args:
//   - message: message text.
//
// Returns: none.
// Complexity: O(1).
func (ux *UX) PrintSuccess(message string) {
	ux.scifiPrint("SUCCESS", message, ux.colors.Success)
}

// PrintError prints an error message.
//
// Args:
//   - message: message text.
//
// Returns: none.
// Complexity: O(1).
func (ux *UX) PrintError(message string) {
	ux.scifiPrint("ERROR", message, ux.colors.Error)
}

// PrintWarning prints a warning message.
//
// Args:
//   - message: message text.
//
// Returns: none.
// Complexity: O(1).
func (ux *UX) PrintWarning(message string) {
	ux.scifiPrint("WARNING", message, ux.colors.Warning)
}

// PrintInfo prints an informational message.
//
// Args:
//   - message: message text.
//
// Returns: none.
// Complexity: O(1).
func (ux *UX) PrintInfo(message string) {
	ux.scifiPrint("INFO", message, ux.colors.Info)
}

// PrintDebug prints debug output only when debug mode is active
// (/debug on or HELIX_DEBUG=1).
//
// Args:
//   - message: debug text.
//
// Returns: none.
// Complexity: O(1).
func (ux *UX) PrintDebug(message string) {
	if !utils.IsDebugMode() {
		return
	}
	ux.scifiPrint("DEBUG", message, ux.colors.Neutral)
}

// RunShellCommand runs a shell command with inherited stdio.
//
// Args:
//   - command: command text.
//   - dir: working directory.
//   - shellName: shell name detected by Helix.
//
// Returns: error only when the command cannot be launched.
// Complexity: O(command execution time).
func (ux *UX) RunShellCommand(command string, dir string, shellName string) error {
	cmd := BuildShellCommand(command, shellName)

	cmd.Dir = dir
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	err := cmd.Run()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return nil
		}

		return err
	}

	return nil
}

// BuildShellCommand creates the correct exec.Cmd for the detected shell.
//
// Args:
//   - command: command text.
//   - shellName: shell name detected by Helix.
//
// Returns: *exec.Cmd.
// Complexity: O(1).
func BuildShellCommand(command string, shellName string) *exec.Cmd {
	shellName = strings.TrimSpace(shellName)
	lower := strings.ToLower(shellName)

	// Fallback for "unknown" shell detection to prevent exec failures
	if lower == "unknown" {
		lower = ""
	}

	if runtime.GOOS == "windows" {
		switch lower {
		case "powershell", "powershell.exe":
			return exec.Command("powershell", "-NoProfile", "-Command", command)
		case "pwsh", "pwsh.exe":
			return exec.Command("pwsh", "-NoProfile", "-Command", command)
		case "cmd", "cmd.exe", "":
			return exec.Command("cmd", "/C", command)
		default:
			return exec.Command(shellName, "-c", command)
		}
	}

	switch lower {
	case "powershell", "pwsh":
		bin := lower
		if shellName != "" {
			bin = shellName
		}
		return exec.Command(bin, "-NoProfile", "-Command", command)
	case "", "sh":
		return exec.Command("/bin/sh", "-c", command)
	default:
		return exec.Command(shellName, "-c", command)
	}
}

// scifiPrint prints a labeled message using the Helix UX style.
func (ux *UX) scifiPrint(label, text string, colorFunc func(...interface{}) string) {
	msg := fmt.Sprintf("%s %s", ux.scifiLabel(label), colorFunc(text))
	if ux.typewriteAll {
		// Route all system messages through the typewriter engine
		ux.Typewriter(msg)
	} else {
		fmt.Println(msg)
	}
}

// scifiPrefix creates a colored prefix for inline messages.
//
// Args:
//   - label: log label.
//   - colorFunc: colorizer.
//
// Returns: string.
// Complexity: O(1).
func (ux *UX) scifiPrefix(label string, colorFunc func(...interface{}) string) string {
	return fmt.Sprintf("%s → ", colorFunc(label))
}

// scifiLabel creates a neutral bracketed label.
//
// Args:
//   - label: log label.
//
// Returns: string.
// Complexity: O(1).
func (ux *UX) scifiLabel(label string) string {
	return ux.colors.Neutral("[" + label + "]")
}
