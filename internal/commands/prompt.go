// internal/commands/prompt.go
// Purpose: Central prompt abstraction for all command confirmation and text input.
package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Prompter is implemented by UX layers capable of asking the user questions.
type Prompter interface {
	AskYesNo(question string) bool
	AskLine(prompt string) string
	AskTypedConfirmation(label, requiredPhrase string) bool
}

// cliPrompter is the fallback terminal prompter used before the TUI starts.
type cliPrompter struct{}

// AskYesNo asks a yes/no question using stdin.
func (cliPrompter) AskYesNo(question string) bool {
	fmt.Printf("%s [y/N]: ", question)

	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}

	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}

// AskLine reads one line of text using stdin.
func (cliPrompter) AskLine(prompt string) string {
	fmt.Printf("%s: ", prompt)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return ""
	}

	return strings.TrimSpace(line)
}

// AskTypedConfirmation requires an exact typed phrase.
func (c cliPrompter) AskTypedConfirmation(label, requiredPhrase string) bool {
	fmt.Printf("HIGH-RISK operation: %s\n", label)
	fmt.Printf("Type %q to confirm: ", requiredPhrase)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}

	return strings.TrimSpace(line) == requiredPhrase
}

// activePrompter defaults to CLI prompts.
// main.go replaces this with the TUI-aware UX implementation.
var activePrompter Prompter = cliPrompter{}

// SetPrompter installs the active prompter.
func SetPrompter(p Prompter) {
	if p != nil {
		activePrompter = p
	}
}

// AskForConfirmation routes yes/no prompts through the active prompter.
func AskForConfirmation(prompt string) bool {
	return activePrompter.AskYesNo(prompt)
}

// AskLine routes line-input prompts through the active prompter.
func AskLine(prompt string) string {
	return activePrompter.AskLine(prompt)
}

// AskTypedConfirmation routes typed confirmations through the active prompter.
func AskTypedConfirmation(label, requiredPhrase string) bool {
	return activePrompter.AskTypedConfirmation(label, requiredPhrase)
}
