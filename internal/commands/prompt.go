// internal/commands/prompt.go
// Purpose: Central prompt abstraction for all command confirmation and text input.
package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
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
//
// Guarded, because it is now written ONCE PER TURN rather than once per mode.
// The prompter used to follow the conversation mode, which was the same thing
// as the mode; it now follows the provenance of the line being handled, so a
// typed line asks for confirmation at the keyboard even mid-conversation. That
// makes the REPL a writer on every turn while agent goroutines are readers, and
// an unguarded interface value written under a race can be torn.
var (
	promptMu       sync.RWMutex
	activePrompter Prompter = cliPrompter{}
)

// SetPrompter installs the active prompter.
func SetPrompter(p Prompter) {
	if p == nil {
		return
	}
	promptMu.Lock()
	activePrompter = p
	promptMu.Unlock()
}

// ActivePrompter returns the currently installed prompter (used by mode
// switching to save/restore the TTY prompter around voice mode).
func ActivePrompter() Prompter {
	promptMu.RLock()
	defer promptMu.RUnlock()
	return activePrompter
}

// prompter reads the active prompter for one call.
func prompter() Prompter {
	promptMu.RLock()
	defer promptMu.RUnlock()
	return activePrompter
}

// AskForConfirmation routes yes/no prompts through the active prompter.
func AskForConfirmation(prompt string) bool {
	return prompter().AskYesNo(prompt)
}

// AskLine routes line-input prompts through the active prompter.
func AskLine(prompt string) string {
	return prompter().AskLine(prompt)
}

// AskTypedConfirmation routes typed confirmations through the active prompter.
func AskTypedConfirmation(label, requiredPhrase string) bool {
	return prompter().AskTypedConfirmation(label, requiredPhrase)
}
