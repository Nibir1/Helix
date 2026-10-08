// internal/agent/thinking_flip_test.go
// Purpose: the planning-mode coin flip (Metabolism D-031). A recorded turn
// with the flip on lands in one arm, plans accordingly, and says which; the
// switch never outlives the turn.
package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"helix/internal/ai"
	"helix/internal/input"
	"helix/internal/metabolism"
)

func flipAgent(t *testing.T, flip, switchable, coin bool) (*Agent, string) {
	t.Helper()
	dir := t.TempDir()
	rec, err := metabolism.Open(metabolism.Options{Dir: dir, Enabled: true, Usage: MeterUsage})
	if err != nil {
		t.Fatal(err)
	}
	prevSwitch, prevCoin := thinkingSwitchable, coinFlip
	thinkingSwitchable = func() bool { return switchable }
	coinFlip = func() bool { return coin }
	t.Cleanup(func() {
		thinkingSwitchable, coinFlip = prevSwitch, prevCoin
		ai.SetPlannerThinkingOff(false)
	})
	return &Agent{render: HeadlessRenderer{}, Metabolism: rec, FlipThinking: flip}, filepath.Join(dir, metabolism.FileName)
}

// runTurn begins and finishes a recorded planner turn, reporting whether the
// planner's thinking was off during it, and returns the episode line.
func runTurn(t *testing.T, a *Agent, path string) (bool, string) {
	t.Helper()
	a.beginEpisode()
	off := ai.PlannerThinkingOff()
	a.turnPlanned = true
	a.lastResponse = "done"
	a.finishEpisode(input.InputEvent{Text: "run the tests"})
	if ai.PlannerThinkingOff() {
		t.Fatal("the switch outlived the turn")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return off, strings.SplitN(string(data), "\n", 2)[0]
}

func TestThinkingFlipArms(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		flip, switchable, coin bool
		off                    bool
		field                  string
	}{
		{"off arm", true, true, true, true, `"thinking":"off"`},
		{"on arm", true, true, false, false, `"thinking":"on"`},
		{"flip not on", false, true, true, false, ""},
		{"provider cannot switch", true, false, true, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, path := flipAgent(t, tc.flip, tc.switchable, tc.coin)
			off, line := runTurn(t, a, path)
			if off != tc.off {
				t.Fatalf("thinking off during the turn = %v, want %v", off, tc.off)
			}
			if tc.field == "" {
				if strings.Contains(line, `"thinking"`) {
					t.Fatalf("no coin was flipped, but the episode names an arm: %s", line)
				}
				return
			}
			if !strings.Contains(line, tc.field) {
				t.Fatalf("episode lacks %s: %s", tc.field, line)
			}
		})
	}
}

// An unrecorded turn is never flipped: an arm nobody measures teaches nothing.
func TestThinkingFlipNeedsRecording(t *testing.T) {
	a, _ := flipAgent(t, true, true, true)
	if err := a.Metabolism.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	a.beginEpisode()
	if ai.PlannerThinkingOff() {
		t.Fatal("thinking switched off on an unrecorded turn")
	}
}
