// internal/ux/ux.go
package ux

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/fatih/color"
)

type UX struct {
	typingSpeed    time.Duration
	animationSpeed time.Duration
	colors         *ColorScheme
}

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

func (ux *UX) AskYesNo(question string) bool {
	fmt.Printf("%s [y/N]: ", question)
	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}

func (ux *UX) AskLine(prompt string) string {
	fmt.Printf("%s: ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(line)
}

func (ux *UX) AskTypedConfirmation(label, requiredPhrase string) bool {
	prompt := fmt.Sprintf("HIGH-RISK operation: %s. Type %q to confirm", label, requiredPhrase)
	response := ux.AskLine(prompt)
	return response == requiredPhrase
}

func (ux *UX) Typewriter(text string) {
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

func (ux *UX) PrintSystemMessage(text string) { ux.scifiPrint("SYSTEM", text, ux.colors.System) }

func (ux *UX) PrintAIMessage(text string, useTypingEffect bool) {
	prefix := ux.scifiPrefix("[NEURAL_NET]", ux.colors.Primary)
	fmt.Print(prefix)
	if useTypingEffect {
		ux.Typewriter(text)
	} else {
		fmt.Println(text)
	}
}

func (ux *UX) PrintCommand(command string) { ux.scifiPrint("EXEC", command, ux.colors.Secondary) }
func (ux *UX) PrintData(data string)       { ux.scifiPrint("DATA", data, ux.colors.Info) }
func (ux *UX) PrintSuccess(message string) { ux.scifiPrint("SUCCESS", message, ux.colors.Success) }
func (ux *UX) PrintError(message string)   { ux.scifiPrint("ERROR", message, ux.colors.Error) }
func (ux *UX) PrintWarning(message string) { ux.scifiPrint("WARNING", message, ux.colors.Warning) }
func (ux *UX) PrintInfo(message string)    { ux.scifiPrint("INFO", message, ux.colors.Info) }

func (ux *UX) PrintDebug(message string) {
	if os.Getenv("HELIX_DEBUG") != "1" {
		return
	}
	ux.scifiPrint("DEBUG", message, ux.colors.Neutral)
}

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

func BuildShellCommand(command string, shellName string) *exec.Cmd {
	shellName = strings.TrimSpace(shellName)
	lower := strings.ToLower(shellName)
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

func (ux *UX) scifiPrint(label, text string, colorFunc func(...interface{}) string) {
	msg := fmt.Sprintf("%s %s", ux.scifiLabel(label), colorFunc(text))
	fmt.Println(msg)
}

func (ux *UX) scifiPrefix(label string, colorFunc func(...interface{}) string) string {
	return fmt.Sprintf("%s → ", colorFunc(label))
}

func (ux *UX) scifiLabel(label string) string {
	return ux.colors.Neutral("[" + label + "]")
}
