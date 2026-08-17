// internal/ux/voiceviz.go
//
// Purpose: Sci-fi voice-mode visualization — a single-line animated HUD that
// renders while Helix listens, decodes, or speaks (BlackBox "living AI" UX).
// Follows the Thinker conventions: TrueColor Helix palette, 100ms frames,
// cursor hidden while active, line healed on Stop, no-op on non-TTY outputs.
//
// States:
//   - VizListening:    reactive waveform bars while the mic is hot
//   - VizTranscribing: scanner sweep while STT decodes the clip
//   - VizSpeaking:     phase-shifted sine wave while TTS audio plays
//   - VizStandby:      slow "breathing" pulse during wake-word standby
//
// The waveform is synthetic (frame-driven) by default; callers with live
// amplitude (streaming capture) feed it via SetLevel for a mic-reactive bar.
package ux

import (
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// VizState selects the voice HUD animation.
type VizState int

const (
	// VizListening renders the mic-hot waveform.
	VizListening VizState = iota
	// VizTranscribing renders the STT decode sweep.
	VizTranscribing
	// VizSpeaking renders the TTS output wave.
	VizSpeaking
	// VizStandby renders the wake-word breathing pulse.
	VizStandby
)

// vizWidth is the waveform window width in cells.
const vizWidth = 16

// vizBars maps a 0..1 intensity to a block glyph.
var vizBars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// VoiceViz renders the live voice HUD on one terminal line.
type VoiceViz struct {
	mu      sync.Mutex
	state   VizState
	label   string
	running bool
	stop    chan struct{}
	frame   int
	level   float64 // 0..1 live amplitude; <0 = synthetic animation
	start   time.Time
	tty     bool
}

// NewVoiceViz creates an idle voice HUD.
func NewVoiceViz() *VoiceViz {
	return &VoiceViz{
		level: -1,
		tty:   term.IsTerminal(int(os.Stdout.Fd())),
	}
}

// Start begins the animation loop in the given state. No-op on non-TTY
// outputs or when already running (SetState switches a running HUD).
func (v *VoiceViz) Start(state VizState) {
	v.mu.Lock()
	if v.running || !v.tty {
		v.state = state
		v.mu.Unlock()
		return
	}
	v.running = true
	v.state = state
	v.start = time.Now()
	v.stop = make(chan struct{})
	v.mu.Unlock()

	fmt.Print("\033[?25l")
	go v.loop(v.stop)
}

// SetState switches the animation without restarting the loop.
func (v *VoiceViz) SetState(state VizState) {
	v.mu.Lock()
	v.state = state
	v.mu.Unlock()
}

// SetLevel feeds a live amplitude sample (0..1). Values outside the range are
// clamped; call with a negative value to return to synthetic animation.
func (v *VoiceViz) SetLevel(level float64) {
	v.mu.Lock()
	if level > 1 {
		level = 1
	}
	v.level = level
	v.mu.Unlock()
}

// Stop halts the animation, clears the line, and restores the cursor.
func (v *VoiceViz) Stop() {
	v.mu.Lock()
	if !v.running {
		v.mu.Unlock()
		return
	}
	v.running = false
	close(v.stop)
	v.mu.Unlock()

	fmt.Print("\r\033[2K\033[?25h")
}

// Running reports whether the HUD is animating.
func (v *VoiceViz) Running() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.running
}

// loop drives the 100ms frame ticker until stopped.
func (v *VoiceViz) loop(stop chan struct{}) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			v.mu.Lock()
			v.frame++
			line := v.renderLocked()
			v.mu.Unlock()
			fmt.Print("\r\033[2K" + line)
		}
	}
}

// renderLocked assembles one frame. Caller holds v.mu.
func (v *VoiceViz) renderLocked() string {
	switch v.state {
	case VizListening:
		return v.renderWaveLocked(thinkCyan, "◉ LISTENING", true)
	case VizTranscribing:
		return v.renderSweepLocked("◌ DECODING SPEECH")
	case VizSpeaking:
		return v.renderWaveLocked(thinkOrange, "◈ HELIX SPEAKING", false)
	default:
		return v.renderPulseLocked()
	}
}

// renderWaveLocked draws the amplitude bars. Mic-reactive when a live level
// has been fed; otherwise a smooth synthetic interference pattern.
func (v *VoiceViz) renderWaveLocked(waveColor, label string, micDot bool) string {
	var b strings.Builder
	dot := thinkOrange
	if micDot && v.frame%10 < 5 {
		dot = thinkMagenta // blinking record dot
	}
	b.WriteString(dot + "●" + thinkReset + " ")
	b.WriteString(thinkMagenta + label + thinkReset + " ")
	b.WriteString(thinkSubtle + "╢" + thinkReset)

	t := float64(v.frame) * 0.45
	for i := 0; i < vizWidth; i++ {
		x := float64(i)
		// Two out-of-phase sines make a lively interference pattern.
		amp := 0.5 + 0.5*math.Sin(t+x*0.9)*math.Sin(t*0.7+x*0.4)
		if v.level >= 0 {
			amp *= 0.25 + 0.75*v.level // live-amplitude scaling
		}
		idx := int(amp * float64(len(vizBars)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(vizBars) {
			idx = len(vizBars) - 1
		}
		color := waveColor
		if idx >= len(vizBars)-2 {
			color = thinkMagenta // peaks flash magenta
		}
		b.WriteString(color + string(vizBars[idx]) + thinkReset)
	}

	b.WriteString(thinkSubtle + "╟" + thinkReset)
	b.WriteString(" " + thinkOrange +
		fmt.Sprintf("%.1fs", time.Since(v.start).Seconds()) + thinkReset)
	return b.String()
}

// renderSweepLocked draws the STT decode scanner (thinker-style sweep).
func (v *VoiceViz) renderSweepLocked(label string) string {
	var b strings.Builder
	b.WriteString(thinkOrange + "⚡" + thinkReset + " ")
	b.WriteString(thinkMagenta + label + thinkReset + " ")
	b.WriteString(thinkSubtle + "╢" + thinkReset)

	pos := v.frame % vizWidth
	for i := 0; i < vizWidth; i++ {
		switch i {
		case pos:
			b.WriteString(thinkCyan + "█" + thinkReset)
		case (pos - 1 + vizWidth) % vizWidth:
			b.WriteString(thinkCyan + "▓" + thinkReset)
		case (pos - 2 + vizWidth) % vizWidth:
			b.WriteString(thinkCyan + "▒" + thinkReset)
		default:
			b.WriteString(thinkSubtle + "░" + thinkReset)
		}
	}

	b.WriteString(thinkSubtle + "╟" + thinkReset)
	b.WriteString(" " + thinkOrange +
		fmt.Sprintf("%.1fs", time.Since(v.start).Seconds()) + thinkReset)
	return b.String()
}

// renderPulseLocked draws the wake-standby breathing ring: a helix glyph that
// slowly brightens and dims, signalling "asleep but aware".
func (v *VoiceViz) renderPulseLocked() string {
	phases := []string{"◦", "○", "◎", "◉", "◎", "○"}
	glyph := phases[(v.frame/4)%len(phases)]

	// Breathing brightness: dim grey → cyan → dim grey.
	color := thinkSubtle
	if p := (v.frame / 4) % len(phases); p == 2 || p == 4 {
		color = thinkCyan
	} else if p == 3 {
		color = thinkMagenta
	}

	var b strings.Builder
	b.WriteString(color + glyph + thinkReset + " ")
	b.WriteString(thinkSubtle + "HELIX :: STANDBY" + thinkReset + " ")
	b.WriteString(thinkCyan + "── say the wake phrase ──" + thinkReset + " ")
	b.WriteString(thinkOrange +
		fmt.Sprintf("%.0fs", time.Since(v.start).Seconds()) + thinkReset)
	return b.String()
}
