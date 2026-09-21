// internal/ux/lineownership_test.go
// Purpose: the animated HUD must not paint over a reply.
//
// A real duplex session produced this, and it is the whole reason the file
// exists:
//
//	┏━ HELIX ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ deepseek / deepseek-flash ━
//	● ◈ HELIX SPEAKING ╢▄▁▁▃▄▄▃▄▇▇▅▃▃▄▄▂╟ 2.6s outstanding.
//
// The band header survived because it ends in a newline. Everything after it
// did not: the SPEAKING HUD repaints "\r\033[2K" every 100ms, so a paragraph
// streaming into that same line was erased ten times a second and only the
// fragment written after the final repaint reached the user. The answer was
// generated and spoken, and destroyed on screen.
package ux

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"helix/internal/shell"
)

func resetLine(t *testing.T) {
	t.Helper()
	lineSuspends.Store(0)
	lineSuspendPeak.Store(0)
	terminalLineHeld.Store(false)
	t.Cleanup(func() {
		lineSuspends.Store(0)
		terminalLineHeld.Store(false)
	})
}

// The property in one line: while output holds the line, the animation loop
// skips its frame.
func TestSuspendedLineStopsTheAnimation(t *testing.T) {
	resetLine(t)

	if LineSuspended() {
		t.Fatal("the line starts suspended")
	}
	SuspendLine()
	if !LineSuspended() {
		t.Fatal("SuspendLine did not take the line — the HUD would keep painting " +
			"over the reply, which is the original bug")
	}
	ResumeLine()
	if LineSuspended() {
		t.Fatal("ResumeLine did not release the line; the HUD would never animate again")
	}
}

// Several HUDs can be alive at once — a turn's own and the speaking one — so
// holds nest. An inner release must not resume the line under an outer writer.
func TestSuspensionsNest(t *testing.T) {
	resetLine(t)

	SuspendLine()
	SuspendLine()
	ResumeLine()
	if !LineSuspended() {
		t.Fatal("an inner release resumed the animation while an outer writer still " +
			"owns the line — its text would be painted over")
	}
	ResumeLine()
	if LineSuspended() {
		t.Fatal("the outer release did not free the line")
	}
}

// A stray release must not drive the counter negative: the next SuspendLine
// would then have to be called twice before it took effect, and the reply after
// it would be wiped with no way to explain why.
func TestUnbalancedResumeCannotGoNegative(t *testing.T) {
	resetLine(t)

	ResumeLine()
	ResumeLine()
	SuspendLine()
	if !LineSuspended() {
		t.Fatal("after stray releases, one SuspendLine no longer holds the line")
	}
}

func TestSuspendIsRaceFree(t *testing.T) {
	resetLine(t)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			SuspendLine()
			ResumeLine()
		}()
	}
	wg.Wait()
	if LineSuspended() {
		t.Fatalf("balanced concurrent holds left the line suspended (count=%d); "+
			"the HUD would never animate again", lineSuspends.Load())
	}
}

// LineHeld and LineSuspended are different questions and must stay so.
// LineHeld asks "is a HUD animating" — background chatter checks it and stays
// quiet. LineSuspended asks "is real output writing" — the HUD checks it and
// stands down. Collapsing them would make the reply suppress itself.
func TestHeldAndSuspendedAreDifferentQuestions(t *testing.T) {
	resetLine(t)

	claimTerminalLine()
	if !LineHeld() {
		t.Fatal("claimTerminalLine did not mark the line held")
	}
	if LineSuspended() {
		t.Error("an animating HUD reports the line as suspended; the reply band would " +
			"treat itself as background chatter and stay quiet")
	}

	SuspendLine()
	if !LineHeld() {
		t.Error("suspending output released the HUD's claim; background writers would " +
			"start printing into the middle of the reply")
	}
	if !LineSuspended() {
		t.Error("output did not take the line")
	}
	ResumeLine()
	releaseTerminalLine()
}

// THE WIRING, written before trusting the helpers. Twice today a correct helper
// sat beside a call site that had stopped using it, and every unit test passed.
// This one drives the real PrintAIMessage and samples whether the line was
// actually held WHILE it wrote.
func TestPrintAIMessageHoldsTheLineWhileItWrites(t *testing.T) {
	resetLine(t)

	u := NewUX()
	u.typingSpeed = time.Millisecond // ~200ms for the text below

	// The sampler MUST be bounded. Without a deadline it spins forever when the
	// hold is never taken — so a broken PrintAIMessage hangs the suite instead
	// of failing it, which is a worse outcome than the bug being tested.
	var mu sync.Mutex
	sawHeld := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if LineSuspended() {
				mu.Lock()
				sawHeld = true
				mu.Unlock()
				return
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()

	// Long enough that the sampler gets a look in, with the typing effect ON so
	// the animated path is the one under test.
	u.PrintAIMessage(strings.Repeat("the quick brown fox. ", 4), true)
	<-done

	mu.Lock()
	held := sawHeld
	mu.Unlock()
	if !held {
		t.Error("PrintAIMessage never suspended the line — an animated HUD would " +
			"repaint over the reply and wipe it as it streams")
	}
	if LineSuspended() {
		t.Error("PrintAIMessage left the line suspended; the HUD would never animate again")
	}
}

// The streaming path takes the hold on first content and keeps it for the whole
// stream. Per-chunk would let the HUD repaint in the gaps between tokens, which
// is the same bug arriving one token at a time.
func TestStreamWriterHoldsAcrossTheWholeStream(t *testing.T) {
	resetLine(t)

	u := NewUX()
	w := u.StreamAIMessage()

	if LineSuspended() {
		t.Fatal("a stream that has produced nothing already holds the line")
	}
	w.Chunk("first ")
	if !LineSuspended() {
		t.Fatal("the stream did not take the line on first content")
	}
	w.Chunk("second ")
	if !LineSuspended() {
		t.Fatal("the hold was dropped between chunks — the HUD repaints in the gaps")
	}
	w.Close()
	if LineSuspended() {
		t.Error("Close did not release the line")
	}
}

// A stream that never produced content took no hold, so Close must not release
// one it does not own — that would drive the counter negative and leave the
// NEXT reply unprotected.
func TestEmptyStreamReleasesNothing(t *testing.T) {
	resetLine(t)

	SuspendLine() // somebody else owns the line
	u := NewUX()
	w := u.StreamAIMessage()
	w.Chunk("   ") // whitespace only: no content
	w.Close()

	if !LineSuspended() {
		t.Fatal("an empty stream released somebody else's hold on the line")
	}
	ResumeLine()
}

// And the loop must honour it. This is the half that cannot be seen from the
// helpers: a correct SuspendLine with a loop that ignores it changes nothing.
func TestAnimationLoopSkipsFramesWhileSuspended(t *testing.T) {
	resetLine(t)

	v := NewVoiceViz()
	// Force the animating state a TTY would give it; Start is a no-op off-TTY.
	v.mu.Lock()
	v.tty = true
	v.running = true
	v.state = VizSpeaking
	v.stop = make(chan struct{})
	v.mu.Unlock()

	painted := captureStdout(t, func() {
		SuspendLine()
		go v.loop(v.stop)
		time.Sleep(350 * time.Millisecond) // three ticks would have painted
		close(v.stop)
		ResumeLine()
	})
	if strings.Contains(painted, "SPEAKING") {
		t.Errorf("the animation painted while output held the line:\n%q", painted)
	}

	// And it resumes afterwards, or the HUD is dead for the rest of the session.
	v.mu.Lock()
	v.stop = make(chan struct{})
	v.mu.Unlock()
	painted = captureStdout(t, func() {
		go v.loop(v.stop)
		time.Sleep(250 * time.Millisecond)
		close(v.stop)
	})
	if !strings.Contains(painted, "SPEAKING") {
		t.Errorf("the animation never resumed after the hold was released:\n%q", painted)
	}
}

// The HUD is redrawn in place ten times a second, so a field that grows
// re-lays the whole row. Measured before the fix: 42 → 43 → 44 columns as the
// timer crossed 10s and 100s, which in a terminal narrower than the line wraps
// and makes the waveform appear to jump. Reported as "the progress bar shakes".
func TestHUDWidthIsStableAsTheTimerGrows(t *testing.T) {
	resetLine(t)

	for _, state := range []VizState{VizListening, VizSpeaking, VizTranscribing} {
		v := NewVoiceViz()
		v.mu.Lock()
		v.state = state
		v.mu.Unlock()

		widths := map[int]bool{}
		for _, elapsed := range []time.Duration{
			0, 9 * time.Second, 10 * time.Second, 99 * time.Second, 100 * time.Second,
		} {
			v.mu.Lock()
			v.start = time.Now().Add(-elapsed)
			line := shell.Plain(v.renderLocked())
			v.mu.Unlock()
			widths[runewidth.StringWidth(line)] = true
		}
		if len(widths) != 1 {
			t.Errorf("%v renders at %d different widths as the timer grows (%v) — the "+
				"row re-lays itself and the waveform appears to shake", state, len(widths), widths)
		}
	}
}

// And across frames, so the animation itself does not move the row.
func TestHUDWidthIsStableAcrossFrames(t *testing.T) {
	resetLine(t)

	v := NewVoiceViz()
	v.mu.Lock()
	v.state = VizSpeaking
	v.start = time.Now()
	v.mu.Unlock()

	widths := map[int]bool{}
	for f := 0; f < 60; f++ {
		v.mu.Lock()
		v.frame = f
		line := shell.Plain(v.renderLocked())
		v.mu.Unlock()
		widths[runewidth.StringWidth(line)] = true
	}
	if len(widths) != 1 {
		t.Errorf("the speaking HUD renders at %d widths across 60 frames: %v", len(widths), widths)
	}
}

// The SPEAKING indicator must not animate a level Helix does not have.
//
// The model's audio is decoded and played, never metered, so a full-range
// waveform beside it is a fabricated signal — which is what "the progress bar
// shakes" looks like. Measured: the old interference pattern changed 41% of the
// row every frame at 10fps; the pulse changes 12%.
func TestSpeakingIndicatorIsCalmerThanTheListeningWave(t *testing.T) {
	churn := func(state VizState) float64 {
		v := NewVoiceViz()
		v.mu.Lock()
		v.state, v.start = state, time.Now()
		v.mu.Unlock()

		prev := ""
		changed, total := 0, 0
		for f := 0; f < 30; f++ {
			v.mu.Lock()
			v.frame = f
			line := shell.Plain(v.renderLocked())
			v.mu.Unlock()
			// Compare RUNES between the brackets. The bar glyphs are
			// three bytes each, so a byte-wise diff of a slice taken at
			// byte offsets compares the wrong things.
			runes := []rune(line)
			a, b := -1, -1
			for i, r := range runes {
				if r == '╢' {
					a = i + 1
				}
				if r == '╟' {
					b = i
				}
			}
			if a < 0 || b <= a {
				continue
			}
			bar := string(runes[a:b])
			if prev != "" && len([]rune(bar)) == len([]rune(prev)) {
				pr := []rune(prev)
				for i, r := range []rune(bar) {
					total++
					if r != pr[i] {
						changed++
					}
				}
			}
			prev = bar
		}
		if total == 0 {
			t.Fatalf("%v rendered no bar to measure", state)
		}
		return float64(changed) / float64(total)
	}

	speaking := churn(VizSpeaking)
	listening := churn(VizListening)

	if speaking >= listening {
		t.Errorf("the SPEAKING indicator churns %.0f%% per frame against LISTENING's "+
			"%.0f%% — it animates a level Helix does not have",
			speaking*100, listening*100)
	}
	if speaking > 0.25 {
		t.Errorf("the speaking indicator changes %.0f%% of the row per frame; at 10fps "+
			"that reads as shaking", speaking*100)
	}
}

// Chrome carries no label. `┄ step 1 of 3` was polished and still came out as
// `[SYSTEM]    ┄ step 1 of 3`: the new line inside the old frame, which reads
// worse than either alone and is what "the UI is still stale" meant.
func TestChromeCarriesNoLabelPrefix(t *testing.T) {
	u := NewUX()

	out := captureStdout(t, func() { u.PrintChrome("┄ step 1 of 3") })
	plain := strings.TrimSpace(shell.Plain(out))

	if strings.Contains(plain, "[") {
		t.Errorf("chrome was stamped with a label: %q", plain)
	}
	if plain != "┄ step 1 of 3" {
		t.Errorf("chrome was altered: %q, want it emitted verbatim", plain)
	}

	// And a message still IS labelled — the two channels must stay different.
	msg := shell.Plain(captureStdout(t, func() { u.PrintSystemMessage("a real message") }))
	if !strings.Contains(msg, "SYSTEM") {
		t.Errorf("PrintSystemMessage lost its label: %q", strings.TrimSpace(msg))
	}
}

// An empty chrome line prints nothing. stepLine returns "" for a single-step
// plan, and a blank row between every step is worse than no marker at all.
func TestEmptyChromePrintsNothing(t *testing.T) {
	u := NewUX()
	if out := captureStdout(t, func() { u.PrintChrome("") }); out != "" {
		t.Errorf("an empty chrome line printed %q", out)
	}
}

// EVERY print yields the line, not just the reply.
//
// SuspendLine started around PrintAIMessage because a wiped reply was the
// visible half. It was not the whole: a live session prints step markers, EXEC
// lines, warnings and info between HUD frames, and each lands on the row the
// HUD is repainting ten times a second. Reported as "the progress bar glitches
// when it starts to speak OR ANYTHING ELSE PRINTS ON THE SCREEN" — the second
// half of that sentence is the general case.
func TestEveryPrintPathYieldsTheAnimatedLine(t *testing.T) {
	u := NewUX()

	paths := map[string]func(){
		"PrintSystemMessage": func() { u.PrintSystemMessage("x") },
		"PrintCommand":       func() { u.PrintCommand("glob **/*.go") },
		"PrintSuccess":       func() { u.PrintSuccess("x") },
		"PrintError":         func() { u.PrintError("x") },
		"PrintWarning":       func() { u.PrintWarning("x") },
		"PrintInfo":          func() { u.PrintInfo("x") },
		"PrintData":          func() { u.PrintData("x") },
		"PrintChrome":        func() { u.PrintChrome("  ┄ step 1 of 2") },
	}

	for name, call := range paths {
		t.Run(name, func(t *testing.T) {
			resetLine(t)

			// Observed from INSIDE the write, not by a sampler. A print is far
			// faster than any poll interval, so a goroutine watching the
			// counter races and reports a false failure; a writer that checks
			// the counter as the bytes arrive cannot miss the window.
			lineSuspendPeak.Store(0)
			_ = captureStdout(t, call)
			if lineSuspendPeak.Load() == 0 {
				t.Errorf("%s does not suspend the animated line — its output lands on "+
					"the row the HUD is repainting ten times a second", name)
			}
			if LineSuspended() {
				t.Errorf("%s left the line suspended; the HUD never animates again", name)
			}
		})
	}
}

// A tool step is chrome, not a message: it carries no bracketed label and it
// starts where every other line starts.
func TestExecLinesAreChromeAndAligned(t *testing.T) {
	resetLine(t)
	u := NewUX()

	out := shell.Plain(captureStdout(t, func() { u.PrintCommand("glob **/*.md") }))
	line := strings.TrimRight(out, "\n")

	if strings.Contains(line, "[EXEC]") {
		t.Errorf("a tool step still carries a bracketed label: %q", line)
	}
	if !strings.HasPrefix(line, "  ") {
		t.Errorf("a tool step starts at column %d, not 2 — the left edge of a live "+
			"session breaks in and out line by line: %q",
			len(line)-len(strings.TrimLeft(line, " ")), line)
	}
	if !strings.Contains(line, "glob **/*.md") {
		t.Errorf("the command itself is missing: %q", line)
	}
}

// Every printed line starts at column two. Warnings, errors and data were the
// last ones starting at column zero, so a live session's left edge stepped in
// and out depending on which kind of line came next.
func TestEveryPrintedLineStartsAtColumnTwo(t *testing.T) {
	resetLine(t)
	u := NewUX()

	paths := map[string]func(){
		"PrintWarning": func() { u.PrintWarning("Instruction Firewall: plan quarantined") },
		"PrintError":   func() { u.PrintError("Planner model error: context deadline exceeded") },
		"PrintData":    func() { u.PrintData("1. 50 Funniest YouTube videos of All Time") },
		"PrintInfo":    func() { u.PrintInfo("a note") },
		"PrintSuccess": func() { u.PrintSuccess("done") },
		"PrintCommand": func() { u.PrintCommand("glob **/*.go") },
	}
	for name, call := range paths {
		out := shell.Plain(captureStdout(t, call))
		for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if line == "" {
				continue
			}
			indent := len(line) - len(strings.TrimLeft(line, " "))
			if indent != 2 {
				t.Errorf("%s starts at column %d, not 2 — the left edge of a live "+
					"session steps in and out line by line: %q", name, indent, line)
			}
		}
	}
}
