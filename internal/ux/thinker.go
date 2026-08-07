// internal/ux/thinker.go
//
// Purpose: HELIX "reasoning" indicator — a single-line animated scanner that
// renders while Helix waits on a neural link (planner, chat, model listing).
// Mirrors the rag.Progress aesthetic: orange bolt, magenta label, cyan scanner
// window over grid-grey cells, orange elapsed timer. Hides the cursor while
// active and heals the line cleanly on Stop so output starts on a fresh line.
package ux

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// thinkerWidth is the scanner window width in cells.
const thinkerWidth = 10

// thinkHex mirrors the Helix TrueColor palette.
const (
	thinkCyan    = "\033[38;2;4;217;255m" // Electric Cyan
	thinkMagenta = "\033[38;2;255;0;85m"  // Neon Magenta
	thinkOrange  = "\033[38;2;255;153;0m" // Orange
	thinkSubtle  = "\033[38;2;68;68;68m"  // Grid Grey
	thinkReset   = "\033[0m"
)

// Thinker renders a live "reasoning" indicator on one terminal line.
type Thinker struct {
	mu      sync.Mutex
	label   string
	running bool
	stop    chan struct{}
	frame   int
	start   time.Time
	tty     bool
}

// NewThinker creates an idle thinker with the given stage label
// (e.g. "HELIX :: REASONING").
//
// Args:
//   - label: magenta stage text shown next to the bolt.
//
// Returns: *Thinker. Complexity: O(1).
func NewThinker(label string) *Thinker {
	if label == "" {
		label = "HELIX :: REASONING"
	}
	return &Thinker{
		label: label,
		tty:   term.IsTerminal(int(os.Stdout.Fd())),
	}
}

// Start begins the 100ms animation loop and hides the cursor.
// No-op on non-TTY outputs (pipes, scripts) or when already running.
//
// Args: none. Returns: none. Complexity: O(1).
func (t *Thinker) Start() {
	t.mu.Lock()
	if t.running || !t.tty {
		t.mu.Unlock()
		return
	}
	t.running = true
	t.start = time.Now()
	t.stop = make(chan struct{})
	t.mu.Unlock()

	fmt.Print("\033[?25l")
	go t.loop(t.stop)
}

// Stop halts the animation, clears the line, and restores the cursor.
//
// Args: none. Returns: none. Complexity: O(1).
func (t *Thinker) Stop() {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	t.running = false
	close(t.stop)
	t.mu.Unlock()

	// Heal the line: erase the frame, show the cursor, stay on this line so
	// the next print (e.g. [NEURAL_NET] →) starts on a clean frame.
	fmt.Print("\r\033[2K\033[?25h")
}

// loop drives the 100ms frame ticker until stopped.
func (t *Thinker) loop(stop chan struct{}) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			t.mu.Lock()
			t.frame++
			line := t.renderLocked()
			t.mu.Unlock()
			fmt.Print("\r\033[2K" + line)
		}
	}
}

// renderLocked assembles one frame. Caller holds t.mu.
//
// Args: none. Returns: the fully-colored single-line frame. Complexity: O(width).
func (t *Thinker) renderLocked() string {
	var b strings.Builder
	b.WriteString(thinkOrange + "⚡" + thinkReset + " ")
	b.WriteString(thinkMagenta + t.label + thinkReset + " ")
	b.WriteString(thinkSubtle + "╢" + thinkReset)

	pos := t.frame % thinkerWidth
	for i := 0; i < thinkerWidth; i++ {
		switch i {
		case pos:
			b.WriteString(thinkCyan + "█" + thinkReset)
		case (pos - 1 + thinkerWidth) % thinkerWidth:
			b.WriteString(thinkCyan + "▓" + thinkReset)
		case (pos - 2 + thinkerWidth) % thinkerWidth:
			b.WriteString(thinkCyan + "▒" + thinkReset)
		default:
			b.WriteString(thinkSubtle + "░" + thinkReset)
		}
	}

	b.WriteString(thinkSubtle + "╟" + thinkReset)
	b.WriteString(" " + thinkOrange +
		fmt.Sprintf("%.1fs", time.Since(t.start).Seconds()) + thinkReset)
	return b.String()
}
