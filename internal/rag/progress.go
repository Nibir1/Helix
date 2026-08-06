// internal/rag/progress.go
// Purpose: Live, single-line, Helix-styled progress engine for indexing and
// fetching operations. Determinate mode renders a TrueColor cell bar;
// indeterminate mode renders an animated scanner window.
//
// Aesthetic: magenta stage label, cyan filled cells, magenta leading edge,
// grid-grey empties, orange percentage, white counters — the Tron/Red-Team
// palette used across the prompt and splash.
package rag

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"helix/internal/shell"
)

const barWidth = 26

// Progress renders a live progress bar on one terminal line.
type Progress struct {
	mu      sync.Mutex
	stage   string
	current int
	total   int // 0 => indeterminate
	running bool
	stop    chan struct{}
	frame   int
}

// NewProgress creates an idle progress renderer.
func NewProgress() *Progress { return &Progress{} }

// Set updates a determinate stage (current/total).
func (p *Progress) Set(stage string, current, total int) {
	p.mu.Lock()
	p.stage = stage
	p.current = current
	p.total = total
	p.mu.Unlock()
}

// SetStage switches to an indeterminate (scanner) stage.
func (p *Progress) SetStage(stage string) { p.Set(stage, 0, 0) }

// Start begins the 100ms render loop and hides the cursor.
func (p *Progress) Start() {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.stop = make(chan struct{})
	p.mu.Unlock()
	fmt.Print("\033[?25l")
	go p.loop(p.stop)
}

// Stop halts rendering, clears the line, and restores the cursor.
func (p *Progress) Stop() {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.running = false
	close(p.stop)
	p.mu.Unlock()
	fmt.Print("\r\033[2K\033[?25h\n")
}

func (p *Progress) loop(stop chan struct{}) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			p.mu.Lock()
			p.frame++
			line := p.renderLocked()
			p.mu.Unlock()
			fmt.Print("\r\033[2K" + line)
		}
	}
}

// renderLocked assembles one frame. Caller holds p.mu.
func (p *Progress) renderLocked() string {
	stage := p.stage
	if stage == "" {
		stage = "WORKING"
	}
	if len(stage) > 24 {
		stage = stage[:24]
	}
	stage = fmt.Sprintf("%-24s", stage)

	bolt := shell.Fg(shell.HexTertiary, "⚡")
	label := shell.Fg(shell.HexSecondary, stage)

	var bar strings.Builder
	if p.total > 0 {
		ratio := float64(p.current) / float64(p.total)
		if ratio > 1 {
			ratio = 1
		}
		filled := int(ratio * barWidth)
		for i := 0; i < barWidth; i++ {
			switch {
			case i < filled:
				bar.WriteString(shell.Fg(shell.HexPrimary, "█"))
			case i == filled:
				bar.WriteString(shell.Fg(shell.HexSecondary, "▓"))
			default:
				bar.WriteString(shell.Fg(shell.HexSubtle, "░"))
			}
		}
		pct := shell.Fg(shell.HexTertiary, fmt.Sprintf("%3d%%", int(ratio*100)))
		count := shell.Fg(shell.HexText, fmt.Sprintf("%d/%d", p.current, p.total))
		return fmt.Sprintf("%s %s %s%s%s %s %s",
			bolt, label,
			shell.Fg(shell.HexSubtle, "╢"), bar.String(), shell.Fg(shell.HexSubtle, "╟"),
			pct, count)
	}

	// Indeterminate scanner window.
	pos := p.frame % barWidth
	for i := 0; i < barWidth; i++ {
		switch i {
		case pos:
			bar.WriteString(shell.Fg(shell.HexPrimary, "█"))
		case (pos - 1 + barWidth) % barWidth:
			bar.WriteString(shell.Fg(shell.HexSecondary, "▓"))
		case (pos - 2 + barWidth) % barWidth:
			bar.WriteString(shell.Fg(shell.HexSecondary, "▒"))
		default:
			bar.WriteString(shell.Fg(shell.HexSubtle, "░"))
		}
	}
	dots := shell.Fg(shell.HexSubtle, "…")
	return fmt.Sprintf("%s %s %s%s%s %s",
		bolt, label,
		shell.Fg(shell.HexSubtle, "╢"), bar.String(), shell.Fg(shell.HexSubtle, "╟"),
		dots)
}
