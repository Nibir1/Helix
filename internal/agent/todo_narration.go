// internal/agent/todo_narration.go
// Purpose: saying out loud what the plan is, where it has got to, and how it
// ended.
//
// WHY. In a live conversation the screen is the thing you are not looking at.
// A multi-step job would run for thirty seconds emitting EXEC lines nobody
// heard, and the only spoken output was the final reply — so from the user's
// side Helix answered a question and then went quiet for half a minute with no
// indication that it was working, what it had decided to do, or whether it had
// finished. Reported as "it's not following the todo approach, it's just
// replying".
//
// The list already exists and the agent already writes to it. What was missing
// is that none of it was audible. So each edit to the plan gets one short
// spoken line:
//
//	plan written   "Here's the plan: three steps. First, read the parser."
//	task started   "Now: bump the version."
//	task done      "Done. Next: run the tests."
//	task set aside "Skipping the retry loop — it's already there."
//	all finished   "All three done. The details are on screen."
//
// SHORT IS THE WHOLE DESIGN. These interrupt a conversation, and a narration
// that reads out paths and reasons would be worse than silence — the reasons
// are on screen, where they can be read at leisure. Nothing here speaks a path,
// a version or a line number; live.SpeakableSummary would withhold them anyway,
// and a sentence built to be withheld is a sentence not worth building.
package agent

import (
	"fmt"
	"regexp"
	"strings"

	"helix/internal/session"
)

// speakTodoStep narrates one applied edit, if anything is listening.
//
// Called from handleTodoStep after the change has landed, so it can never
// announce work that did not happen — the failure mode of narrating intent
// instead of fact.
func (a *Agent) speakTodoStep(action string, item session.TodoItem, remaining []session.TodoItem) {
	if a == nil || !a.voiceActive() {
		return
	}
	if line := todoNarration(action, item, remaining); line != "" {
		a.speak(line)
	}
}

// todoNarration renders the spoken line for one edit, or "" for edits not
// worth interrupting a conversation over.
func todoNarration(action string, item session.TodoItem, remaining []session.TodoItem) string {
	task := spokenTask(item.Text)

	switch action {
	case "add":
		// Additions are NOT narrated one by one. A three-task plan arrives as
		// three separate add steps, and "I've added X. I've added Y. I've
		// added Z." is three interruptions for one decision — the plan is
		// announced once, by speakPlan, when the batch has landed.
		return ""

	case "revise":
		if item.WasText == "" {
			return ""
		}
		// A rewrite of the USER's own task is the one edit they would dispute,
		// so it is the one that always gets said.
		return "Changing one of your tasks: " + task + ". The reason is on screen."

	case "drop":
		return ""

	case "state":
		switch item.State {
		case session.TodoInProgress:
			return "Now: " + task + "."
		case session.TodoDone:
			if next := firstOpen(remaining); next != "" {
				return "Done. Next: " + spokenTask(next) + "."
			}
			return "That one's done."
		case session.TodoSuperseded:
			return "Skipping " + task + " — the reason is on screen."
		case session.TodoBlocked:
			return "Blocked on " + task + "."
		}
	}
	return ""
}

// speakPlan announces a freshly written plan, once.
//
// Spoken when the agent has just added tasks and has not yet started one, which
// is the moment the user is waiting to hear that Helix understood them.
func (a *Agent) speakPlan(items []session.TodoItem) {
	if a == nil || !a.voiceActive() {
		return
	}
	open := openTasks(items)
	if len(open) == 0 {
		return
	}
	if line := planNarration(open); line != "" {
		a.speak(line)
	}
}

// planNarration renders the spoken plan announcement.
//
// The first task is named and the rest are counted. Reading five tasks aloud is
// a list nobody retains; naming the first tells the user Helix understood them,
// and the count tells them how long this will take — which is the pair of
// things they actually want.
func planNarration(open []session.TodoItem) string {
	first := spokenTask(open[0].Text)
	switch len(open) {
	case 1:
		return "Right — one thing to do: " + first + "."
	case 2:
		return "Right — two steps. First, " + first + "."
	default:
		return fmt.Sprintf("Right — %d steps. First, %s.", len(open), first)
	}
}

// speakRunEnd is the closing line: what got done, and where to look.
func (a *Agent) speakRunEnd(settled int, open []session.TodoItem) {
	if a == nil || !a.voiceActive() {
		return
	}
	if line := runEndNarration(settled, open); line != "" {
		a.speak(line)
	}
}

// runEndNarration renders the closing line.
//
// It reports both halves when there are both, because "three done" while two
// remain open is the kind of summary that sends someone away believing a job
// finished. What is LEFT is the part that changes what they do next.
func runEndNarration(settled int, open []session.TodoItem) string {
	switch {
	case settled == 0 && len(open) == 0:
		return ""
	case len(open) == 0 && settled == 1:
		return "That's done. The details are on screen."
	case len(open) == 0:
		return fmt.Sprintf("All %d done. The details are on screen.", settled)
	case settled == 0:
		return fmt.Sprintf("I stopped with %s still open. The list is on screen.",
			countTasks(len(open)))
	default:
		return fmt.Sprintf("%d done, %d still open. The list is on screen.",
			settled, len(open))
	}
}

// countTasks renders a count the way it is said aloud.
func countTasks(n int) string {
	if n == 1 {
		return "one task"
	}
	return fmt.Sprintf("%d tasks", n)
}

// spokenTask trims a task to something worth hearing.
//
// A task written for the screen can carry a path, a line number or a clause in
// parentheses; spoken, those are noise that pushes the verb out of earshot.
// Everything after an em dash, a colon or an opening parenthesis is detail, and
// the head of the sentence is the instruction.
func spokenTask(text string) string {
	t := strings.TrimSpace(text)
	for _, sep := range []string{" — ", " – ", ": ", " ("} {
		if i := strings.Index(t, sep); i > 0 {
			t = t[:i]
		}
	}

	// A path spoken in full is unlistenable — "slash Users slash me slash proj
	// slash internal slash config slash config dot go" buries the verb it was
	// attached to. The base name carries the meaning; the full path is on
	// screen, where it can be read. (live.SpeakableSummary would withhold the
	// whole sentence for containing one, so without this the narration is not
	// merely ugly, it is silent.)
	t = pathInSpeech.ReplaceAllStringFunc(t, func(p string) string {
		if i := strings.LastIndex(p, "/"); i >= 0 && i+1 < len(p) {
			return p[i+1:]
		}
		return p
	})
	t = strings.TrimSpace(strings.TrimRight(t, ".,;"))

	// A long task still has to end somewhere. Cut on a word boundary rather
	// than mid-word, and do not add an ellipsis — spoken, it is silence.
	const maxSpoken = 70
	if len([]rune(t)) > maxSpoken {
		words := strings.Fields(t)
		t = ""
		for _, w := range words {
			if len([]rune(t))+len([]rune(w))+1 > maxSpoken {
				break
			}
			if t != "" {
				t += " "
			}
			t += w
		}
	}
	return t
}

// pathInSpeech matches a path with at least one separator — the shape that is
// worth reducing to a base name. A bare "config.go" is already speakable.
var pathInSpeech = regexp.MustCompile(`(~|\.{1,2})?(/[\w.@+-]+){2,}`)

// firstOpen names the next task still to do, or "".
func firstOpen(items []session.TodoItem) string {
	for _, it := range items {
		if it.State != session.TodoDone && it.State != session.TodoSuperseded {
			return it.Text
		}
	}
	return ""
}

// openTasks filters to work still outstanding, in list order.
func openTasks(items []session.TodoItem) []session.TodoItem {
	var open []session.TodoItem
	for _, it := range items {
		if it.State != session.TodoDone && it.State != session.TodoSuperseded {
			open = append(open, it)
		}
	}
	return open
}
