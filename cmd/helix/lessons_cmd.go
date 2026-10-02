// cmd/helix/lessons_cmd.go
// Purpose: /lessons — the lessons Metabolism tested, delivered into planner
// turns (internal/metabolism/lessons.go, docs/harness.md §12).
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"helix/internal/metabolism"
	"helix/internal/shell"
)

// handleLessonsCommand implements /lessons [on|off|status|why <id>|forget <id> <reason>].
func handleLessonsCommand(c cmdArgs) {
	if metabolismRec == nil {
		uiFail("lessons", "Metabolism is not available in this session")
		return
	}
	switch c.Sub() {
	case "on", "enable":
		if !metabolismRec.Enabled() {
			uiFail("lessons", "lessons are delivered only on recorded turns: run /metabolism on first")
			uiDetail("A lesson on an unrecorded turn earns no credit and loses none, so the engine could never tell whether it helps.")
			return
		}
		if err := setLessonDelivery(true); err != nil {
			uiFail("lessons", err.Error())
			return
		}
		uiOK("lessons", "on — tested lessons go into planner turns as data, on a coin flip")
		uiDetail("A lesson can inform a plan; it can never approve a step. /lessons off stops it.")
	case "off", "disable":
		_ = setLessonDelivery(false)
		uiIdle("lessons", "off — planner turns get no lessons")
	case "", "status", "list":
		printLessons()
	case "why", "show":
		explainLesson(c.Arg(1))
	case "forget":
		forgetLesson(c.Arg(1), c.From(2))
	default:
		uiUsage("/lessons [on|off|why <id>|forget <id> <reason>]")
	}
}

func setLessonDelivery(on bool) error {
	if agentCore != nil {
		agentCore.DeliverLessons = on
	}
	cfg.UserPrefs.MetabolismLessons = on
	return cfg.SavePreferences()
}

// lessonsHere loads the delivery file and what applies in this directory.
func lessonsHere() (metabolism.Delivery, metabolism.Scope, map[string]string, error) {
	d, err := metabolismRec.Delivery()
	cwd, _ := os.Getwd()
	return d, metabolism.ScopeFor(cwd), metabolismRec.Forgotten(), err
}

func printLessons() {
	on := cfg.UserPrefs.MetabolismLessons
	detail := "tested lessons go into planner turns, on a coin flip"
	if on && !metabolismRec.Enabled() {
		detail = "paused: recording is off (/metabolism on)"
	}
	uiToggle("LESSONS", on, detail, "planner turns get no lessons", "/lessons <on|off>")
	d, here, forgotten, err := lessonsHere()
	if err != nil {
		uiWarn("lessons", "the delivery file cannot be read, so nothing is delivered: "+err.Error())
		return
	}
	if len(d.Lessons) == 0 {
		uiIdle("lessons", "none yet")
		uiDetail("The engine writes them with `metabolism export` once a lesson has passed its replay test.")
		return
	}
	fmt.Println(shell.KV("FILE", shell.Value(metabolismRec.LessonsPath())+
		shell.Muted("  written "+d.GeneratedAt.Local().Format(time.DateTime)), shell.KVWidth("FILE")))
	machine := metabolism.MachineKey()
	for _, l := range d.Lessons {
		state := shell.Badge(shell.StateIdle, l.State)
		if l.State == metabolism.StateAccepted {
			state = shell.Badge(shell.StateGood, l.State)
		}
		where := shell.Muted("applies here")
		switch {
		case forgotten[l.ID] != "":
			where = shell.Muted("forgotten here")
		case !l.Scope.Applies(here, machine):
			where = shell.Muted("not here (" + l.Scope.Level + ")")
		}
		fmt.Printf("  %s %s  %s · %s\n", shell.Value(shortID(l.ID)), state, where,
			shell.Muted(fmt.Sprintf("delivered on %.0f%% of turns", 100*l.P)))
		fmt.Printf("     %s\n", truncStr(l.Text, 100))
	}
	fmt.Println(shell.Hint("/lessons why <id> · /lessons forget <id> <reason>"))
}

// findLesson matches an ID or a unique prefix of at least six characters.
func findLesson(d metabolism.Delivery, id string) (metabolism.DeliveredLesson, error) {
	id = strings.TrimSpace(id)
	if len(id) < 6 {
		return metabolism.DeliveredLesson{}, fmt.Errorf("give at least six characters of the lesson ID")
	}
	var found []metabolism.DeliveredLesson
	for _, l := range d.Lessons {
		if strings.HasPrefix(l.ID, id) {
			found = append(found, l)
		}
	}
	switch len(found) {
	case 0:
		return metabolism.DeliveredLesson{}, fmt.Errorf("no delivered lesson %q", id)
	case 1:
		return found[0], nil
	}
	return metabolism.DeliveredLesson{}, fmt.Errorf("%q matches %d lessons; give more of the ID", id, len(found))
}

func explainLesson(id string) {
	d, here, forgotten, err := lessonsHere()
	if err != nil {
		uiFail("lessons", err.Error())
		return
	}
	l, err := findLesson(d, id)
	if err != nil {
		uiFail("lessons", err.Error())
		return
	}
	applies := "yes"
	switch {
	case forgotten[l.ID] != "":
		applies = "no: forgotten here (" + forgotten[l.ID] + ")"
	case !l.Scope.Applies(here, metabolism.MachineKey()):
		applies = "no: " + l.Scope.Level + " " + l.Scope.Key
	}
	uiReport("LESSON "+l.ID,
		uiRow{Label: "TEXT", Value: l.Text},
		uiRow{Label: "STATE", Value: fmt.Sprintf("%s · delivered on %.0f%% of the turns it applies to", l.State, 100*l.P)},
		uiRow{Label: "KIND", Value: l.Kind},
		uiRow{Label: "SCOPE", Value: strings.TrimSpace(l.Scope.Level + " " + l.Scope.Key)},
		uiRow{Label: "HERE", Value: applies},
		uiRow{Label: "EVIDENCE", Value: orNothing(l.Evidence)},
		uiRow{Label: "CREDIT", Value: orNothing(l.Credit)},
	)
}

func forgetLesson(id, reason string) {
	d, _, _, err := lessonsHere()
	if err != nil {
		uiFail("lessons", err.Error())
		return
	}
	l, err := findLesson(d, id)
	if err != nil {
		uiFail("lessons", err.Error())
		return
	}
	if strings.TrimSpace(reason) == "" {
		uiUsage("/lessons forget <id> <reason>", "The reason is kept with the lesson's history, so \"why did it forget this?\" has an answer.")
		return
	}
	recorded, err := metabolismRec.Forget(l.ID, reason)
	if err != nil {
		uiFail("lessons", err.Error())
		return
	}
	uiOK("lessons", "forgot "+shortID(l.ID)+": it is no longer delivered here")
	if recorded {
		uiDetail("The engine eliminates it, with your reason, at the next `metabolism ingest`.")
	} else {
		uiDetail("Recording is off, so the engine has not been told; it stays forgotten here regardless.")
	}
}

func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

// orNothing renders a missing value as "none": a lesson with no credit yet
// has none, which is different from a setting being unset (orNone).
func orNothing(s string) string {
	if s == "" {
		return shell.Muted("none")
	}
	return s
}
