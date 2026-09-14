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
	"helix/internal/shell"
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

// AIStreamWriter renders an AI response incrementally as tokens arrive
// (BlackBox P8.8).
//
// It deliberately does NOT reuse Typewriter. The typewriter *simulates* live
// generation with fixed per-character sleeps; once real tokens are streaming,
// that simulation is strictly worse — it would add artificial delay on top of
// genuine provider latency and make responses slower than they are today. Real
// arrival timing replaces the simulation, while the audible tick and the
// [NEURAL_NET] prefix keep the established character.
type AIStreamWriter struct {
	ux      *UX
	started bool
	held    bool // a SuspendLine is outstanding and Close must release it
	band    *shell.BandWriter
}

// ReplyMeta names the model that produced the reply, for the band header.
//
// A HOOK rather than a parameter, because the alternative was widening
// agent.Renderer's StreamAIMessage/PrintAIMessage signatures and every
// implementation of them — including the headless one, which has no band and no
// use for the value. It is read at RENDER time, not turn start, so a mid-turn
// failover to the local model is named correctly rather than reporting whatever
// was selected when the turn began.
//
// nil is the honest default: a caller that has not wired it gets a band with no
// model in the rule, which is what a caller that does not know the model should
// produce.
var ReplyMeta func() string

// replyMeta reads the hook safely.
func replyMeta() string {
	if ReplyMeta == nil {
		return ""
	}
	return ReplyMeta()
}

// StreamAIMessage begins an incrementally rendered AI response. The caller
// feeds it with Chunk and MUST finish with Close.
func (ux *UX) StreamAIMessage() *AIStreamWriter {
	return &AIStreamWriter{ux: ux}
}

// Chunk renders one streamed fragment, emitting the prefix on first content.
//
// The prefix is deferred rather than printed up front so a response that never
// produces text leaves no orphaned "[NEURAL_NET] →" on screen.
func (w *AIStreamWriter) Chunk(text string) {
	if text == "" {
		return
	}
	if !w.started {
		// Models commonly open with a newline or spaces; leading whitespace
		// would push the answer off the first rail line.
		text = strings.TrimLeft(text, " \t\r\n")
		if text == "" {
			return
		}
		// Taken on FIRST CONTENT and released in Close, so the hold spans the
		// whole stream rather than each chunk. Per-chunk would let the HUD
		// repaint in the gaps between tokens, which is the same bug arriving
		// one token at a time.
		SuspendLine()
		w.held = true
		fmt.Println(shell.BandHeader("HELIX", replyMeta(), shell.HexPrimary))
		w.band = shell.NewBandWriter()
		w.started = true
	}

	// One tick per chunk, not per character: a chunk is roughly a token, which
	// gives a steadier rhythm than per-rune ticking. PlayType is internally
	// throttled to 10ms, so fast streams cannot stack audio streams.
	if strings.TrimSpace(text) != "" {
		audio.PlayType()
	}
	w.band.WriteString(text)
}

// Started reports whether any content was rendered, so callers can fall back
// to a buffered print for an empty response.
func (w *AIStreamWriter) Started() bool { return w.started }

// Close terminates the streamed line.
func (w *AIStreamWriter) Close() {
	if w.started {
		w.band.Close()
	}
	// Released here and not in a defer on Chunk: the hold has to outlive every
	// chunk, and a stream that produced no content never took one.
	if w.held {
		w.held = false
		ResumeLine()
	}
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
// PrintAIMessage prints an AI response as a band.
//
// THIS IS THE NON-STREAMING PATH — a vision answer, a fast-path reply, anything
// the agent has in hand before it prints. The streaming path deliberately does
// NOT animate (see AIStreamWriter: real arrival timing replaces the
// simulation), but here there is no arrival timing to replace, so the typing
// effect still has a job and `/config typing-effect` still governs it.
//
// It feeds the BAND rather than printing the text, because the rail is emitted
// per line: animating the finished string would type over the frame. The first
// version of this dropped the parameter entirely, which quietly turned a
// documented setting — "Animate AI replies" — into one that did nothing.
func (ux *UX) PrintAIMessage(text string, useTypingEffect bool) {
	if strings.TrimSpace(text) == "" {
		return
	}
	// The reply owns the line while it writes. Without this an animated HUD —
	// the duplex SPEAKING waveform, say — repaints over the band ten times a
	// second and the answer is wiped as fast as it is drawn.
	SuspendLine()
	defer ResumeLine()

	fmt.Println(shell.BandHeader("HELIX", replyMeta(), shell.HexPrimary))

	if !useTypingEffect && !ux.typewriteAll {
		for _, line := range shell.BandLines(text) {
			fmt.Println(line)
		}
		return
	}
	ux.typeIntoBand(text)
}

// typeIntoBand animates a finished reply through the band writer.
//
// Fed rune by rune so the WRITER decides every line break — the alternative is
// animating pre-wrapped lines, which types the rail glyph as though it were
// content and puts the frame inside the animation.
func (ux *UX) typeIntoBand(text string) {
	band := shell.NewBandWriter()
	delay := ux.typingSpeed
	if n := len([]rune(text)); n > 400 {
		delay = 8 * time.Millisecond
	} else if n > 200 {
		delay = 15 * time.Millisecond
	}
	for _, r := range text {
		if r != ' ' && r != '\n' && r != '\r' && r != '\t' {
			audio.PlayType()
		}
		band.WriteString(string(r))
		time.Sleep(delay)
	}
	band.Close()
}

// PrintCommand prints a command execution header.
//
// Args:
//   - command: command text.
//
// Returns: none.
// Complexity: O(1).
func (ux *UX) PrintCommand(command string) {
	// `[EXEC] glob **/*.md` was the last of the bracketed labels left in a
	// live trace, and it sat at column ZERO while the step markers, the prompt
	// and the reply band all start at column two — so the left edge of a
	// running session broke in and out by two cells, line by line.
	//
	// A tool step is the same family as `┄ step 1 of 2`: a record of what
	// happened, not something Helix is saying. It reads as one now.
	ux.PrintChrome("  " + shell.Fg(shell.HexSubtle, "▸ ") + shell.Fg(shell.HexAmber, command))
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
// PrintChrome writes an already-formatted line with no label and no typewriter.
//
// Chrome is structure, not speech: a step marker animated character by
// character is the frame pretending to be content.
func (ux *UX) PrintChrome(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	SuspendLine()
	defer ResumeLine()
	fmt.Println(text)
}

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
	// EVERY print yields the animated line, not just the reply.
	//
	// SuspendLine started life around PrintAIMessage, because a reply being
	// wiped by the speaking HUD was the visible half of the problem. It is not
	// the whole of it: a live session prints step markers, EXEC lines, warnings
	// and info between HUD frames, and each one lands on the row the HUD is
	// repainting ten times a second. Reported as "the progress bar glitches
	// when it starts to speak OR ANYTHING ELSE PRINTS ON THE SCREEN" — the
	// second half of that sentence is the general case, and this is the one
	// place all of it funnels through.
	SuspendLine()
	defer ResumeLine()

	msg := fmt.Sprintf("%s %s", ux.scifiLabel(label), colorFunc(text))
	if ux.typewriteAll {
		// Route all system messages through the typewriter engine
		ux.Typewriter(msg)
	} else {
		fmt.Println(msg)
	}
}

// scifiPrefix is GONE. It built the `[NEURAL_NET] →` inline prefix, and the
// band layout replaced that with a labelled rule — the prefix could not say
// which model produced the turn and could not hold the prose to a measure, and
// both were the reported complaint. scifiPrint keeps its own inline form for
// SYSTEM and WARNING lines, which are single-line notices rather than turns.

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
