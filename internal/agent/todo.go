// internal/agent/todo.go
// Purpose: the `todo` tool step — the agent editing the plan of record.
//
// WHY THE AGENT WRITES HERE AT ALL. The list was injected into every planner
// prompt as read-only data, which meant the only party that ever learned
// anything could not record it. A plan written before any work happened is a
// guess; the agent is the one that reads the code, runs the tests, and finds out
// that step 3 is already done and step 4 has to happen first. It had no way to
// say so, and no way to note work it discovered was needed — so on a long job
// the real plan lived inside one planner call and was re-derived from scratch on
// the next round, which is how steps get silently dropped and work gets redone.
//
// WHY THIS IS NOT A GATED TOOL. Every other tool here runs commands, changes
// files, reaches the network or opens a camera. This one edits a list of
// sentences in a 0600 file in Helix's own state directory. Grading it medium
// and asking "may I update my task list?" between every step would train the
// user to approve without reading, which is the failure mode confirmations
// exist to avoid. The authority is bounded by what the list can DO — nothing —
// and by the rule in session/todo_agent.go: the agent may redirect anything and
// erase only its own.
//
// What is NOT skipped: plan mode still prints instead of acting, `/dry-run`
// still declines to change state, and the change is always announced on screen.
// The user sees every edit as it happens.
package agent

import (
	"fmt"
	"strconv"
	"strings"

	"helix/internal/ai"
	"helix/internal/session"
)

// handleTodoStep applies one agent edit to the task list and returns the line
// that goes back to the planner.
func (a *Agent) handleTodoStep(step ai.PlanStep) (string, error) {
	if a.Todos == nil {
		return "", fmt.Errorf("no task list is loaded")
	}
	action := strings.TrimSpace(step.Action)
	subject, err := todoSubject(action, step.Args)
	if err != nil {
		return "", err
	}

	if a.Permission() == PermissionPlan {
		a.render.PrintWarning("[plan] would " + subject)
		return "", nil
	}
	if a.execConfig.DryRun {
		a.render.PrintWarning("[Dry Run] Would " + subject)
		return "", nil
	}

	item, err := a.applyTodoAction(action, step.Args)
	if err != nil {
		return "", err
	}

	// Remember that this turn touched this task, whoever wrote it.
	//
	// THE RUN THAT FOUND THIS. The loop counted only tasks the agent CREATED,
	// so a run that worked entirely on the user's three tasks reported no
	// outstanding work at every iteration: the "still working" label never
	// appeared, and when the budget ran out the warning that should have said
	// so was skipped. The turn ended silently with task #2 in progress, the
	// edit it described never made, and nothing on screen saying the job had
	// stopped rather than finished.
	//
	// Marking a task in_progress is the agent ADOPTING it. That is exactly the
	// moment it becomes this run's work, regardless of who wrote it down.
	a.noteTodoTouched(item.ID)

	// Say it. In a live conversation the screen is the thing nobody is looking
	// at: a multi-step job used to run for half a minute emitting EXEC lines
	// with no spoken sign that Helix had understood, started, or finished.
	a.speakTodoStep(action, item, a.Todos.Items())
	a.announcePlanOnce(action)

	// Always on screen. The agent revising the user's own plan is exactly the
	// thing that must not happen quietly, and an edit the user only discovers
	// later by typing /todo is a plan that changed behind their back.
	//
	// Rendered in the same shape /todo uses — state glyph, id, text, then
	// labelled notes hanging in a column — rather than as a SYSTEM line with a
	// prose tail. The two were different enough that the running commentary and
	// the list it was editing did not look like the same thing.
	for _, line := range TodoStepLines(action, item) {
		a.render.PrintSystemMessage(line)
	}

	return todoReceipt(action, item) + "\n\nCurrent list:\n" + a.Todos.Summary(0), nil
}

// announcePlanOnce speaks the plan the first time a task is STARTED.
//
// Not on the adds. A three-task plan arrives as three separate add steps, and
// narrating each is three interruptions for one decision; waiting for the first
// "in progress" means the plan is announced once, complete, at the moment the
// user is waiting to hear that Helix understood them.
func (a *Agent) announcePlanOnce(action string) {
	if action != "state" || a.planAnnounced || a.Todos == nil {
		return
	}
	a.planAnnounced = true
	a.speakPlan(a.Todos.Items())
}

// applyTodoAction routes to the agent-scoped list API, which is where the
// "redirect anything, erase only your own" rule is enforced. It is enforced
// there rather than here so that a second caller cannot get a weaker version of
// it by forgetting a check.
func (a *Agent) applyTodoAction(action string, args map[string]string) (session.TodoItem, error) {
	reason := strings.TrimSpace(args["reason"])

	if action == "add" {
		return a.Todos.AgentAdd(args["text"], reason)
	}

	id, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(args["id"]), "#"))
	if err != nil {
		return session.TodoItem{}, fmt.Errorf("todo %s needs a numeric id, got %q", action, args["id"])
	}

	switch action {
	case "revise":
		return a.Todos.AgentRevise(id, args["text"], reason)
	case "state":
		st, ok := session.ValidTodoState(args["state"])
		if !ok {
			return session.TodoItem{}, fmt.Errorf(
				"unknown task state %q — use pending, in_progress, done, blocked or superseded", args["state"])
		}
		return a.Todos.AgentSetState(id, st, reason)
	case "drop":
		if err := a.Todos.AgentRemove(id); err != nil {
			return session.TodoItem{}, err
		}
		return session.TodoItem{ID: id}, nil
	}
	return session.TodoItem{}, fmt.Errorf("unsupported todo action: %s", action)
}

// todoSubject renders a step as one line, and rejects the arguments that cannot
// work before anything is changed.
func todoSubject(action string, args map[string]string) (string, error) {
	id := strings.TrimSpace(args["id"])
	switch action {
	case "add":
		text := strings.TrimSpace(args["text"])
		if text == "" {
			return "", fmt.Errorf("todo add needs text")
		}
		return "add task: " + text, nil
	case "revise":
		if id == "" || strings.TrimSpace(args["text"]) == "" {
			return "", fmt.Errorf("todo revise needs an id and text")
		}
		return "rewrite task #" + id + " as: " + strings.TrimSpace(args["text"]), nil
	case "state":
		if id == "" || strings.TrimSpace(args["state"]) == "" {
			return "", fmt.Errorf("todo state needs an id and a state")
		}
		return "mark task #" + id + " " + strings.TrimSpace(args["state"]), nil
	case "drop":
		if id == "" {
			return "", fmt.Errorf("todo drop needs an id")
		}
		return "delete task #" + id, nil
	}
	return "", fmt.Errorf("unsupported todo action: %s", action)
}

// TodoStepLines renders one applied edit for the screen: a head line carrying
// the state glyph, the id and the text, then any notes beneath it.
//
// Exported so the layout can be rendered in a test and looked at, which is the
// only way to check a layout — §9 rule 12. It takes the item rather than the
// Agent so nothing about it needs a session to render.
func TodoStepLines(action string, item session.TodoItem) []string {
	// The verb LEADS, in its own column. It was a trailing word at first and
	// read as a fragment dangling off the end of the task text; in front, the
	// line says what just happened to task N and then what task N is, which is
	// the order the reader wants it in.
	//
	// For "state" the verb is the destination, not the mechanism: "done" and
	// "set aside" tell you what changed, where "set" tells you only that
	// something did.
	verb := map[string]string{"add": "added", "revise": "rewrote", "drop": "deleted"}[action]
	if action == "state" {
		verb = map[session.TodoState]string{
			session.TodoInProgress: "started",
			session.TodoDone:       "done",
			session.TodoBlocked:    "blocked",
			session.TodoSuperseded: "set aside",
			session.TodoPending:    "reopened",
		}[item.State]
	}
	if verb == "" {
		verb = "changed"
	}

	head := fmt.Sprintf("%s TASK %-2d  %-9s  %s",
		todoStateGlyph(item.State), item.ID, verb, item.Text)
	if action == "drop" {
		// There is no text to show: the item is gone.
		head = fmt.Sprintf("%s TASK %-2d  %-9s", todoStateGlyph(item.State), item.ID, verb)
	}
	lines := []string{strings.TrimRight(head, " ")}

	if item.WasText != "" {
		lines = append(lines, todoStepNote("you wrote", item.WasText))
	}
	if item.Reason != "" {
		lines = append(lines, todoStepNote("why", item.Reason))
	}
	return lines
}

// todoStepNote hangs one labelled note under a step line, in the same column
// /todo uses so the two readings line up.
func todoStepNote(label, value string) string {
	// Indented to the TEXT column of the head line above: glyph(1) + " TASK "(6)
	// + id(2) + 2 + verb(9) + 2.
	return strings.Repeat(" ", 22) + "↳ " + label + strings.Repeat(" ", maxInt(1, 10-len(label))) + value
}

// todoStateGlyph mirrors the list's state markers so a step and the list entry
// it produced carry the same symbol.
func todoStateGlyph(s session.TodoState) string {
	switch s {
	case session.TodoDone:
		return "✔"
	case session.TodoInProgress:
		return "▸"
	case session.TodoBlocked:
		return "✖"
	case session.TodoSuperseded:
		return "⊘"
	default:
		return "·"
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// todoReceipt states what happened, for the screen and for the planner.
func todoReceipt(action string, item session.TodoItem) string {
	switch action {
	case "add":
		return fmt.Sprintf("added #%d: %s", item.ID, item.Text)
	case "revise":
		return fmt.Sprintf("rewrote #%d: %s", item.ID, item.Text)
	case "state":
		return fmt.Sprintf("#%d is now %s: %s", item.ID, item.State, item.Text)
	case "drop":
		return fmt.Sprintf("deleted #%d", item.ID)
	}
	return "task list updated"
}
