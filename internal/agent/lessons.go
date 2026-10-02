package agent

import (
	"fmt"
	"math/rand/v2"

	"helix/internal/metabolism"
)

// liveLessonsBlock is the learned-lessons block for a live planner turn, and
// records on the turn's episode which lessons were delivered and which the
// coin withheld (Metabolism Phase 3, docs/harness.md §12).
//
// It delivers only when the user turned lessons on AND the turn is being
// recorded. A lesson delivered on an unrecorded turn could earn no credit and
// lose none, so it could never be eliminated by evidence; delivery and
// measurement go together or not at all.
//
// The block is the same one helix replay uses: fenced, data-only, bounded,
// angle brackets neutralised. A lesson informs the plan and can never
// authorize a step, lower a risk tier or answer a confirmation. A missing or
// unreadable delivery file costs the turn its lessons, never the turn.
func (a *Agent) liveLessonsBlock() string {
	if !a.DeliverLessons || a.episode == nil {
		return ""
	}
	d, err := a.Metabolism.Delivery()
	if err != nil {
		a.render.PrintDebug("lessons: delivery file skipped: " + err.Error())
		return ""
	}
	if len(d.Lessons) == 0 {
		return ""
	}
	forgotten := map[string]bool{}
	for id := range a.Metabolism.Forgotten() {
		forgotten[id] = true
	}
	budget := metabolism.Budget{
		MaxLessons: MaxReplayLessons,
		MaxBytes:   MaxLessonsBlockChars,
		Size:       func(text string) int { return len(lessonLine(text)) },
	}
	r := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	sel := metabolism.Select(d, a.episode.Scope(), metabolism.MachineKey(), forgotten, budget, r)
	a.episode.SetLessons(sel.IDs(), sel.Withheld)
	if len(sel.Delivered) == 0 {
		return ""
	}
	lessons := make([]LearnedLesson, 0, len(sel.Delivered))
	for _, l := range sel.Delivered {
		lessons = append(lessons, LearnedLesson{ID: l.ID, Text: l.Text})
	}
	a.render.PrintDebug(fmt.Sprintf("lessons: delivered %d, withheld %d", len(lessons), len(sel.Withheld)))
	return learnedLessonsBlock(lessons)
}
