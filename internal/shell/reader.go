// internal/shell/reader.go
// Purpose: Raw-mode line editor with a SMOOTH animated prompt and an
// IN-PLACE resize-healing redraw engine.
//
// PERFORMANCE + RELIABILITY ARCHITECTURE:
//  1. Every frame is ONE buffered string emitted with a single write call.
//  2. Only cheap per-line clears (\033[2K) are used — never \033[J for
//     routine redraws.
//  3. Syntax highlighting is cached and recomputed ONLY on buffer mutation.
//  4. Glitch is a short 150ms burst on a gentle 1Hz clock (legacy rhythm).
//  5. RESIZE HEALING IS IN-PLACE: on SIGWINCH / width change we clear our
//     own drawn block and repaint on the SAME lines. We never emit "\r\n"
//     during a resize, so prompt lines can never duplicate.
package shell

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"helix/internal/utils"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

// editor holds the mutable line state shared between the keystroke loop and
// the background animation ticker. All redraws are mutex-atomic.
type editor struct {
	ctx PromptContext
	hl  *utils.SyntaxHighlighter

	mu    sync.Mutex
	width int

	left     string
	right    string
	leftLen  int
	rightLen int

	buf        []rune
	cursor     int
	suggestion string

	hlCache string
	hlDirty bool

	glitch float64

	// Geometry of the previous draw (for cheap, targeted clearing).
	prevLines      int
	prevCursorLine int

	done bool
}

// lastHistory is set by ReadLine so updateSuggestion stays mutex-scoped.
var lastHistory []string

// ReadLine renders the animated prompt and reads one line of input.
func ReadLine(ctx PromptContext, highlighter *utils.SyntaxHighlighter, history []string) (string, error) {
	fd := int(os.Stdin.Fd())

	promptStatic := Fg(HexRectifier, bold+GlyphPrompt) + " "
	if !term.IsTerminal(fd) {
		return readLineCooked(promptStatic)
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return readLineCooked(promptStatic)
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	e := &editor{
		ctx:     ctx,
		hl:      highlighter,
		width:   TerminalWidth(),
		hlDirty: true,
	}
	if e.width <= 0 {
		e.width = 80
	}
	e.redraw(true)

	stop := make(chan struct{})
	defer close(stop)
	go e.animationLoop(stop)

	// Instant resize healing (Unix). The 1Hz poll remains as fallback.
	winchCh, stopWinch := notifyResize()
	defer stopWinch()
	go func() {
		for range winchCh {
			e.handleResize()
		}
	}()

	lastHistory = history
	histIdx := len(history)

	for {
		var b [1]byte
		_, rerr := os.Stdin.Read(b[:])
		if rerr != nil {
			e.finish()
			return "", rerr
		}

		switch b[0] {
		case 3: // Ctrl+C
			e.finish()
			_, _ = os.Stdout.WriteString("^C\r\n")
			return "", fmt.Errorf("interrupted")
		case 4: // Ctrl+D
			if len(e.buf) == 0 {
				e.finish()
				_, _ = os.Stdout.WriteString("^D\r\n")
				return "", fmt.Errorf("EOF")
			}
		case 13, 10: // Enter
			out := string(e.buf)
			e.finish()
			_, _ = os.Stdout.WriteString("\r\n\x1b]133;C\x07")
			return out, nil
		case 127, 8: // Backspace
			e.mutate(func() {
				if e.cursor > 0 {
					e.buf = append(e.buf[:e.cursor-1], e.buf[e.cursor:]...)
					e.cursor--
					e.hlDirty = true
					e.updateSuggestion()
				}
			})
		case 9: // Tab (basic file completion)
			e.mutate(func() {
				text := string(e.buf)
				parts := strings.Fields(text)
				if len(parts) > 0 {
					lastWord := parts[len(parts)-1]
					matches, _ := filepath.Glob(lastWord + "*")
					if len(matches) == 1 {
						prefix := text[:len(text)-len(lastWord)]
						e.buf = []rune(prefix + matches[0])
						e.cursor = len(e.buf)
						e.hlDirty = true
						e.suggestion = ""
					}
				}
			})
		case 27: // Escape sequences (arrows)
			var seq [2]byte
			if _, err := os.Stdin.Read(seq[:1]); err != nil {
				continue
			}
			if seq[0] != '[' {
				continue
			}
			if _, err := os.Stdin.Read(seq[1:2]); err != nil {
				continue
			}
			switch seq[1] {
			case 'A': // Up → history
				e.mutate(func() {
					if histIdx > 0 {
						histIdx--
						e.buf = []rune(lastHistory[histIdx])
						e.cursor = len(e.buf)
						e.hlDirty = true
						e.suggestion = ""
					}
				})
			case 'B': // Down → history
				e.mutate(func() {
					if histIdx < len(lastHistory)-1 {
						histIdx++
						e.buf = []rune(lastHistory[histIdx])
						e.cursor = len(e.buf)
						e.hlDirty = true
						e.suggestion = ""
					} else if histIdx == len(lastHistory)-1 {
						histIdx = len(lastHistory)
						e.buf = []rune{}
						e.cursor = 0
						e.hlDirty = true
						e.suggestion = ""
					}
				})
			case 'C': // Right → move / accept ghost text
				e.mutate(func() {
					if e.cursor < len(e.buf) {
						e.cursor++
					} else if e.suggestion != "" {
						e.buf = append(e.buf, []rune(e.suggestion)...)
						e.cursor = len(e.buf)
						e.hlDirty = true
						e.suggestion = ""
					}
				})
			case 'D': // Left
				e.mutate(func() {
					if e.cursor > 0 {
						e.cursor--
					}
				})
			}
		default:
			if b[0] >= 32 { // Printable rune
				e.mutate(func() {
					e.buf = append(e.buf[:e.cursor], append([]rune{rune(b[0])}, e.buf[e.cursor:]...)...)
					e.cursor++
					e.hlDirty = true
					e.updateSuggestion()
				})
			}
		}
	}
}

// finish marks the editor done so background goroutines stop redrawing.
func (e *editor) finish() {
	e.mu.Lock()
	e.done = true
	e.mu.Unlock()
}

// handleResize heals the prompt IN PLACE after a terminal resize.
//
// CRITICAL FIX (duplication bug): the previous implementation abandoned the
// line with "\r\n", so every resize event (SIGWINCH + 1Hz width poll during
// maximize/minimize storms) stamped a brand-new prompt line below the old
// one — producing the stacked duplicate prompts.
//
// New model:
//  1. "\r\033[J"  → home the cursor column and erase the wrapped tail below
//     it (kills reflow fragments without touching scrollback above),
//  2. rewind to the TOP of our own drawn block,
//  3. repaint via redrawLocked, which clears exactly prevLines lines.
//
// No newline is ever emitted, so the prompt can never duplicate.
func (e *editor) handleResize() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done {
		return
	}
	if w := TerminalWidth(); w > 0 {
		e.width = w
	}

	// 1) Kill wrapped tail below the cursor.
	_, _ = os.Stdout.WriteString("\r\033[J")

	// 2) Rewind to the top line of our drawn block.
	if e.prevCursorLine > 0 {
		_, _ = fmt.Fprintf(os.Stdout, "\033[%dA", e.prevCursorLine)
	}

	// 3) The old block now spans (prevCursorLine + 1) lines from the top.
	e.prevLines = e.prevCursorLine + 1
	e.prevCursorLine = 0

	e.redrawLocked(true)
}

// animationLoop drives the clock refresh (1Hz) and short glitch bursts, and
// acts as the fallback resize detector on platforms without SIGWINCH.
func (e *editor) animationLoop(stop <-chan struct{}) {
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			e.mu.Lock()
			if e.done {
				e.mu.Unlock()
				return
			}
			resized := false
			if w := TerminalWidth(); w > 0 {
				if w != e.width {
					resized = true
				}
				e.width = w
			}
			startBurst := false
			if e.glitch == 0 && rand.Float64() < 0.30 {
				e.glitch = 0.10
				startBurst = true
			}
			e.mu.Unlock()

			if resized {
				e.handleResize()
			} else {
				e.redraw(true)
			}

			if startBurst {
				time.AfterFunc(150*time.Millisecond, func() {
					e.mu.Lock()
					if e.done {
						e.mu.Unlock()
						return
					}
					e.glitch = 0
					e.mu.Unlock()
					e.redraw(true)
				})
			}
		}
	}
}

// mutate applies a buffer mutation and redraws atomically.
func (e *editor) mutate(fn func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn()
	e.redrawLocked(true)
}

// redraw regenerates panels (if regen) and redraws atomically.
func (e *editor) redraw(regen bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.redrawLocked(regen)
}

// redrawLocked assembles ONE frame and writes it with a single syscall.
// Caller holds e.mu.
func (e *editor) redrawLocked(regen bool) {
	if regen {
		e.left, e.right, e.leftLen, e.rightLen = RenderPromptParts(e.ctx, e.glitch)
	}

	// Recompute highlighting ONLY when the buffer changed.
	if e.hlDirty {
		text := string(e.buf)
		if e.hl != nil {
			e.hlCache = e.hl.HighlightCommand(text)
		} else {
			e.hlCache = text
		}
		e.hlDirty = false
	}

	w := e.width
	if w <= 0 {
		w = 80
	}

	wBuf := runewidth.StringWidth(string(e.buf))
	wGhost := runewidth.StringWidth(e.suggestion)
	wBefore := runewidth.StringWidth(string(e.buf[:e.cursor]))

	contentLen := e.leftLen + wBuf + wGhost
	newLines := contentLen/w + 1

	// Right panel only when the whole line fits (2-col safety margin),
	// preventing any overlap with the input area.
	showRight := e.rightLen > 0 && newLines == 1 && e.leftLen+wBuf+wGhost+e.rightLen+2 <= w

	var b strings.Builder

	// 1) Clear the previous draw area using cheap LINE clears only.
	if e.prevLines > 0 {
		if e.prevCursorLine > 0 {
			fmt.Fprintf(&b, "\033[%dA", e.prevCursorLine)
		}
		b.WriteString("\r\033[2K")
		for i := 1; i < e.prevLines; i++ {
			b.WriteString("\033[B\033[2K")
		}
		if e.prevLines > 1 {
			fmt.Fprintf(&b, "\033[%dA", e.prevLines-1)
		}
	} else {
		b.WriteString("\r\033[2K")
	}

	// 2) Left panel.
	b.WriteString(e.left)

	// 3) Right panel on line 0 (absolute column positioning).
	if showRight {
		col := w - e.rightLen // ends at column w-1, never at the wrap edge
		if col < e.leftLen+1 {
			col = e.leftLen + 1
		}
		fmt.Fprintf(&b, "\033[%dG", col)
		b.WriteString(e.right)
		fmt.Fprintf(&b, "\033[%dG", e.leftLen+1)
	}

	// 4) Buffer + ghost text.
	b.WriteString(e.hlCache)
	if e.suggestion != "" {
		b.WriteString(fgHex(HexSubtle) + e.suggestion + ansiReset)
	}

	// 5) Reposition cursor (absolute column, relative rows).
	totalBefore := e.leftLen + wBefore
	cursorLine := totalBefore / w
	cursorCol := totalBefore % w
	endLine := contentLen / w
	if endLine > cursorLine {
		fmt.Fprintf(&b, "\033[%dA", endLine-cursorLine)
	}
	fmt.Fprintf(&b, "\033[%dG", cursorCol+1)

	e.prevLines = newLines
	e.prevCursorLine = cursorLine

	// SINGLE WRITE: the entire frame in one syscall.
	_, _ = os.Stdout.WriteString(b.String())
}

// updateSuggestion refreshes the ghost-text from history. Caller holds e.mu.
func (e *editor) updateSuggestion() {
	e.suggestion = ""
	current := string(e.buf)
	if current == "" {
		return
	}
	for i := len(lastHistory) - 1; i >= 0; i-- {
		if strings.HasPrefix(lastHistory[i], current) && lastHistory[i] != current {
			e.suggestion = lastHistory[i][len(current):]
			return
		}
	}
}

func readLineCooked(prompt string) (string, error) {
	fmt.Print(prompt)
	var buf [1024]byte
	n, err := os.Stdin.Read(buf[:])
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(buf[:n])), nil
}
