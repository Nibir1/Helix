// cmd/helix/voice_ui.go
//
// Purpose: the voice wizard's shared rendering, so /blackbox setup speaks in
// the same visual language as every screen it sits next to.
//
// The wizard was the last thing in Helix still printing flat colour. It opened
// with a framed chain menu and then, the moment the user answered, dropped to
// column zero for a port reassignment, a pid, a log path, two verification
// results and a recorder name — five different facts about the same operation,
// in four colours, with nothing tying them together. The primitives live in
// internal/shell; what belongs here is the small amount of wizard-specific glue
// that all three voice files need.
package main

import (
	"fmt"
	"strings"

	"helix/internal/commands"
	"helix/internal/shell"
)

// wizConfirmDanger asks a yes/no question whose YES destroys something.
//
// Same mechanics as wizConfirm, different colour, because a confirmation that
// looks the same whether it picks a voice or wipes the keystore is asking the
// reader to remember which one they are in.
func wizConfirmDanger(question string) bool {
	return commands.AskForConfirmation(shell.PromptDanger(question))
}

// wizConfirm asks a yes/no question in the panel's voice.
//
// The question is rendered with shell.Prompt and handed to the shared prompter,
// which appends its own " [y/N]: " — exactly the arrangement the line-input
// prompts already use, so a confirmation and a menu answer look like the same
// act rather than two unrelated interfaces.
//
// Phrased lowercase and without a question mark, matching the other prompts:
// "start whisper-local now", not "Start whisper-local now?".
func wizConfirm(question string) bool {
	return commands.AskForConfirmation(shell.Prompt(question, ""))
}

// wizStep prints one outcome line, the wizard's unit of progress.
func wizStep(state shell.State, subject, detail string) {
	fmt.Println(shell.Step(state, subject, detail))
}

// wizDetail prints the explanation belonging to the step above it.
//
// Indentation in the input is MEANINGFUL and must survive. A sidecar diagnosis
// is already formatted — a statement at column 0, its reasoning indented two,
// and the command to run indented four — and word-wrapping the whole thing
// collapses that into a paragraph with a shell command buried mid-sentence.
// That defect is already recorded against the status screen (see
// providerDetailLines), and rendering this screen reproduced it immediately:
// a piper launch command came out broken across two lines, which is a command
// nobody can copy.
//
// So the two levels are honoured. Prose wraps. A command-depth line is handed
// to StepCommand and never wrapped, for the same reason: a launch command with
// a line break in it is not a launch command.
func wizDetail(text string) {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if leadingSpaces(line) >= commandIndent {
			fmt.Println(shell.StepCommand(strings.TrimSpace(line)))
			continue
		}
		for _, l := range shell.StepDetail(strings.TrimSpace(line), shell.Muted) {
			fmt.Println(l)
		}
	}
}

// commandIndent is the depth at which a diagnosis line stops being prose.
//
// Four, because that is what internal/speech's diagnosis builder emits: two
// spaces for a continuation ("Start it:") and four for the command beneath it.
// Reading the convention rather than re-deciding it is what keeps the two
// renderers from disagreeing about the same string.
const commandIndent = 4

// leadingSpaces counts the indent of a line, tabs as one.
func leadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' && r != '\t' {
			break
		}
		n++
	}
	return n
}
