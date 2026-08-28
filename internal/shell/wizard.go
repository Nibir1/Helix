// internal/shell/wizard.go
//
// Purpose: the panel language, extended to the screens that ASK rather than
// report.
//
// panel.go converted every status screen. The wizards were left behind, and it
// showed: /blackbox setup opened with a properly framed chain menu and then, the
// moment the user answered, fell out of the frame into a flat stack of
// color.Yellow/Green/Cyan lines starting at column zero — a port reassignment, a
// pid, a log path and a verification result all in different colours, none of
// them attached to the provider they were about. A wizard is the one screen a
// new user cannot skip, so it was the worst place in Helix to lose the thread.
//
// A wizard step is not a report row. It has a SUBJECT (the thing being set up),
// an OUTCOME (did it work), and usually an explanation or a command underneath.
// These three primitives render exactly that, behind the same gutter, so a
// sequence of steps reads as one continuous operation instead of as unrelated
// lines that happen to be adjacent.
package shell

import "strings"

// Step renders one outcome inside a wizard's gutter: a state glyph, the subject
// it happened to, and optional muted detail.
//
// The subject is the provider or resource, never a sentence — "whisper-local",
// not "whisper-local is answering on port 28862". Detail carries the rest, in
// the muted tone, so a reader skimming the glyph column can see what succeeded
// and what did not without reading a word.
//
// A row too wide for the panel wraps with its continuations hanging under the
// subject, for the reason KV wraps: a tail that restarts at column zero has
// visually escaped the block it belongs to.
func Step(s State, subject, detail string) string {
	head := Badge(s, subject)
	if detail == "" {
		return PanelLine(head)
	}

	full := head + "  " + Muted(detail)
	limit := panelWidth() - 2
	if visibleWidth(full) <= limit {
		return PanelLine(full)
	}

	lines := wrapANSI(full, limit)
	out := PanelLine(lines[0])
	for _, l := range lines[1:] {
		// Two cells: the width of the badge glyph and its trailing space, so
		// continuations align under the subject rather than under the glyph.
		out += "\n" + PanelLine("  "+l)
	}
	return out
}

// StepDetail renders the explanation belonging to the Step above it, indented
// past the glyph column.
//
// The indent is what makes it subordinate. Printed flush with the gutter, an
// error's own text reads as a second step — which is how "whisper-local at
// http://127.0.0.1:8080: nothing is listening." came to look like an
// independent event rather than the reason the step above it failed.
func StepDetail(text string, colour func(string) string) []string {
	return panelWrapIndent(text, colour, "  ")
}

// panelWrapIndent is the shared body of PanelWrap and StepDetail: word-wrap to
// the panel width, gutter every line, optionally indent continuations of a
// subordinate block. Extracted rather than copied because two wrappers with
// slightly different measuring is exactly how a frame starts leaking.
func panelWrapIndent(text string, colour func(string) string, indent string) []string {
	if colour == nil {
		colour = func(s string) string { return s }
	}
	limit := panelWidth() - 2 - visibleWidth(indent)
	if limit < 20 {
		limit = 20
	}

	var out []string
	var line string
	flush := func() {
		if line != "" {
			out = append(out, PanelLine(indent+colour(line)))
			line = ""
		}
	}
	for _, word := range strings.Fields(text) {
		// A word too long for any line is split rather than allowed to run past
		// the frame. A URL, an absolute path or an endpoint is the realistic
		// case, and letting one escape defeats the point of wrapping at all.
		if visibleWidth(word) > limit {
			flush()
			for _, part := range wrapANSI(word, limit) {
				out = append(out, PanelLine(indent+colour(part)))
			}
			continue
		}
		switch {
		case line == "":
			line = word
		case visibleWidth(line)+1+visibleWidth(word) <= limit:
			line += " " + word
		default:
			flush()
			line = word
		}
	}
	flush()
	return out
}

// StepCommand renders a command the user is expected to run, under a step.
//
// Distinct from Hint on purpose: Hint is what to type NEXT at the Helix prompt
// and sits flush with the gutter, while this is a shell command belonging to
// the step above it. Never wrapped — a launch command with a line break in it
// cannot be copied — so it is the one thing in a panel allowed to run wide.
func StepCommand(cmd string) string {
	return PanelLine("  " + Fg(HexSubtle, glyphArrow+" ") + Value(cmd))
}

// PromptDanger is Prompt for a question whose YES destroys something.
//
// The colour is the point. Prompt renders in cyan — Helix speaking — which is
// right for "which provider should hear you" and wrong for "permanently delete
// everything". A confirmation that looks identical whether it wipes your API
// keys or picks a voice is asking the reader to supply the stakes from memory,
// and /purge's yes is not reversible.
func PromptDanger(question string) string {
	return "  " + Fg(HexRectifier, glyphSection) + " " + Fg(HexRectifier, question)
}

// Plain strips ANSI colour.
//
// For text that leaves the terminal: spoken aloud, written to a log, compared
// in a test. The voice prompter is the case that made this necessary — it
// passes the question it was given straight to the TTS engine, so a
// panel-styled prompt would have been READ ALOUD as its escape sequences.
func Plain(s string) string { return ansiRegex.ReplaceAllString(s, "") }
