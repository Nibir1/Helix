// internal/shell/about.go
// Purpose: Renders the /about splash — static Neon-Magenta HELIX banner plus a
// continuously glitching identity zone (version / creator / motto) animated with
// the legacy TUI rhythm (2s cadence, 150ms bursts) until any key is pressed.
//
// Design rules (per spec):
//   - The ASCII banner is NEVER glitched.
//   - The underline divider is STATIC (no glitch).
//   - The "[ press any key to return ]" hint is Electric Cyan.
//   - The block cursor is hidden during the splash (no glyph-over-cursor
//     artifacts like the highlighted "L"), and the frozen frame KEEPS its
//     glitch when the user presses a key.
//
// Dependencies: stdlib + golang.org/x/term.
package shell

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// AboutArt is the static HELIX ASCII banner. It is NEVER glitched.
const AboutArt = `
╔════════════════════════════════════════════════════════════════╗
║                                                                ║
║               ██╗  ██╗███████╗██╗     ██╗██╗  ██╗              ║
║               ██║  ██║██╔════╝██║     ██║╚██╗██╔╝              ║
║               ███████║█████╗  ██║     ██║ ╚███╔╝               ║
║               ██╔══██║██╔══╝  ██║     ██║ ██╔██╗               ║
║               ██║  ██║███████╗███████╗██║██╔╝ ██╗              ║
║               ╚═╝  ╚═╝╚══════╝╚══════╝╚═╝╚═╝  ╚═╝              ║
║                                                                ║
╚════════════════════════════════════════════════════════════════╝`

// Center pads s with leading spaces so it appears centered in width columns.
// ANSI sequences are ignored when measuring.
func Center(s string, width int) string {
	w := visibleWidth(s)
	if width <= w {
		return s
	}
	return strings.Repeat(" ", (width-w)/2) + s
}

// RenderAbout prints the full /about splash.
//
// Animation model (mirrors the legacy TUI glitch rhythm):
//   - banner printed once, static, in Neon Magenta (HexSecondary),
//   - identity zone glitches at a 0.05 baseline and bursts to 0.20 every 2s
//     for 150ms — exactly like GlitchTickMsg/resetGlitch,
//   - on keypress the zone is redrawn once at 0.15 so the frozen scrollback
//     frame KEEPS its glitch instead of healing clean,
//   - the cursor is hidden for the entire splash to prevent block-cursor
//     artifacts, and restored on exit.
func RenderAbout(version string) {
	width := TerminalWidth()
	if width <= 0 {
		width = 80
	}

	magenta := func(s string) string { return Fg(HexSecondary, s) }
	white := func(s string) string { return Fg(HexText, s) }
	orangeItalic := func(s string) string { return "\033[3m" + Fg(HexTertiary, s) }
	subtle := func(s string) string { return Fg(HexSubtle, s) }
	cyan := func(s string) string { return Fg(HexPrimary, s) }

	// 1) Static banner — Neon Magenta, never glitched.
	for _, line := range strings.Split(AboutArt, "\n") {
		fmt.Println(Center(magenta(line), width))
	}
	fmt.Println()

	vText := fmt.Sprintf("Helix v%s - AI-Native Shell", version)
	aText := "Creator - " + identityName
	qText := "- We scream truth through broken amps while empires rot in silence -"
	uText := strings.Repeat("─", 50)
	hint := "[ press any key to return ]"

	// zone builds the animated identity block.
	// NOTE: the underline is intentionally STATIC; the hint is cyan.
	zone := func(prob float64) []string {
		return []string{
			Center(white(Glitch(vText, prob)), width),
			Center(magenta(Glitch(aText, prob)), width),
			"",
			Center(orangeItalic(Glitch(qText, prob)), width),
			"",
			Center(subtle(uText), width),
			"",
			Center(cyan(hint), width),
		}
	}
	zoneLines := len(zone(0))

	// drawZone overwrites the zone in place (single write) and parks the
	// cursor at the TOP-LEFT of the zone for the next frame. The trailing \r
	// is critical: in raw mode LF preserves the column, which is what used to
	// strand the block cursor on top of the "L" in "Shell".
	drawZone := func(prob float64) {
		var b strings.Builder
		for _, l := range zone(prob) {
			b.WriteString("\r\033[2K" + l + "\n")
		}
		fmt.Fprintf(&b, "\033[%dA\r", zoneLines)
		_, _ = os.Stdout.WriteString(b.String())
	}

	fd := int(os.Stdin.Fd())
	interactive := term.IsTerminal(fd)

	var old *term.State
	keyCh := make(chan byte, 1)
	if interactive {
		st, err := term.MakeRaw(fd)
		if err != nil {
			interactive = false
		} else {
			old = st
			// One-shot pump: reads exactly one byte (the "any key") and exits,
			// so no goroutine is left behind to steal future prompt input.
			go func() {
				var b [1]byte
				if _, rerr := os.Stdin.Read(b[:]); rerr == nil {
					keyCh <- b[0]
				} else {
					keyCh <- '\n'
				}
			}()
		}
	}

	if !interactive {
		// Static fallback for pipes / scripts.
		for _, l := range zone(0.05) {
			fmt.Println(l)
		}
		return
	}

	// Hide the block cursor so it can never paint over glyphs.
	fmt.Print("\033[?25l")

	drawZone(0.05)
	tick := time.NewTicker(2 * time.Second)
	waiting := true
	for waiting {
		select {
		case <-keyCh:
			// KEEP THE GLITCH ALIVE: freeze the frame at 0.15 instead of
			// healing it clean, so the scrollback retains its living glitch.
			drawZone(0.15)
			waiting = false
		case <-tick.C:
			drawZone(0.20) // burst  (legacy GlitchTickMsg probability)
			time.Sleep(150 * time.Millisecond)
			drawZone(0.05) // heal   (legacy resetGlitch baseline)
		}
	}
	tick.Stop()
	_ = term.Restore(fd, old)

	// Move below the zone, clear the hint line, restore the cursor.
	fmt.Printf("\033[%dB\r", zoneLines-1)
	fmt.Print("\033[2K")
	fmt.Print("\033[?25h")
	fmt.Println()
}
