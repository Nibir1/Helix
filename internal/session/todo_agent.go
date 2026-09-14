// internal/session/todo_agent.go
// Purpose: what the AGENT may do to the task list, as distinct from what you
// may do to it.
//
// THE PROBLEM THIS SOLVES. The list was user-authored and read-only to the
// model: open items were injected into every planner prompt as data, and
// nothing the model learned could change them. That is wrong in both
// directions. A plan written before any work happened is a guess, and the agent
// is the party that finds out it was wrong — it reads the code, runs the tests,
// and discovers that step 3 is unnecessary and step 4 needs to happen first.
// Meanwhile the agent had no way to record work IT decided was needed, so a
// multi-step job's real plan lived only inside one planner call and was
// re-derived from scratch on the next.
//
// So the agent can write here now. The question this file answers is what that
// authority stops at.
//
// THE RULE: THE AGENT MAY REDIRECT ANYTHING AND ERASE ONLY ITS OWN.
//
// It can add items, reorder its intent, revise your wording, mark your task
// done, or supersede it outright — every one of those with a reason attached.
// What it cannot do is DELETE something you wrote. Not because its judgment is
// worse than yours; the user asked for this precisely because a human plan can
// be wrong. It is because deletion is the only one of those operations that
// cannot be seen or undone. A superseded task is visible in `/todo`, carries the
// reason it was set aside, and comes back with `/todo open <id>`. A deleted one
// is a task you still believe is tracked.
//
// Revision follows the same principle: your original wording is kept in
// WasText. The agent gets to change the plan; it does not get to change the
// record of what you asked for.
package session

import (
	"fmt"
	"strings"
	"time"
)

// maxAgentTodos bounds how long the agent may make the list. A model that
// writes forty steps is not planning, and the list is injected into every
// subsequent prompt, so an unbounded one crowds out the conversation that
// produced it.
const maxAgentTodos = 30

// AgentAdd appends a task the agent decided was necessary.
func (l *TodoList) AgentAdd(text, reason string) (TodoItem, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return TodoItem{}, fmt.Errorf("task text cannot be empty")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if open := l.countOpenLocked(); open >= maxAgentTodos {
		return TodoItem{}, fmt.Errorf(
			"the list already has %d open tasks (limit %d) — finish or supersede some before adding, "+
				"or break the work down at a coarser grain", open, maxAgentTodos)
	}

	now := time.Now()
	item := TodoItem{
		ID:        l.nextID,
		Text:      text,
		State:     TodoPending,
		Origin:    TodoFromAgent,
		Reason:    strings.TrimSpace(reason),
		CreatedAt: now,
		UpdatedAt: now,
	}
	l.nextID++
	l.items = append(l.items, item)
	return item, l.save()
}

// AgentSetState moves a task, including one the user wrote.
//
// A reason is REQUIRED when the agent touches a user-authored item and the new
// state is not simply progress. Marking your task done or superseded is the
// agent overruling you, and an overrule with no stated reason is
// indistinguishable from a mistake.
func (l *TodoList) AgentSetState(id int, state TodoState, reason string) (TodoItem, error) {
	reason = strings.TrimSpace(reason)
	l.mu.Lock()
	defer l.mu.Unlock()

	idx, err := l.indexOfLocked(id)
	if err != nil {
		return TodoItem{}, err
	}
	it := &l.items[idx]

	needsReason := !it.Origin.ByAgent() && (state == TodoDone || state == TodoSuperseded)
	if needsReason && reason == "" {
		return TodoItem{}, fmt.Errorf(
			"task %d was written by the user; marking it %s needs a reason saying what you found", id, state)
	}

	it.State = state
	if reason != "" {
		it.Reason = reason
	}
	it.UpdatedAt = time.Now()
	return *it, l.save()
}

// AgentRevise rewrites a task's text, preserving the user's original wording.
func (l *TodoList) AgentRevise(id int, text, reason string) (TodoItem, error) {
	text = strings.TrimSpace(text)
	reason = strings.TrimSpace(reason)
	if text == "" {
		return TodoItem{}, fmt.Errorf("revised text cannot be empty — to drop a task, supersede it")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	idx, err := l.indexOfLocked(id)
	if err != nil {
		return TodoItem{}, err
	}
	it := &l.items[idx]

	if text == it.Text {
		return TodoItem{}, fmt.Errorf("task %d already says exactly that", id)
	}
	if !it.Origin.ByAgent() && reason == "" {
		return TodoItem{}, fmt.Errorf(
			"task %d was written by the user; rewriting it needs a reason saying what you found", id)
	}

	// Only the FIRST revision records the original. A second rewrite must not
	// overwrite the user's wording with the agent's own previous attempt —
	// that would launder the agent's text into the provenance field and lose
	// the one thing this exists to keep.
	if !it.Origin.ByAgent() && it.WasText == "" {
		it.WasText = it.Text
	}
	it.Text = text
	if reason != "" {
		it.Reason = reason
	}
	it.UpdatedAt = time.Now()
	return *it, l.save()
}

// AgentRemove deletes a task the AGENT created. A user-authored task is refused
// — see the file header for why supersede is offered instead.
func (l *TodoList) AgentRemove(id int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	idx, err := l.indexOfLocked(id)
	if err != nil {
		return err
	}
	if !l.items[idx].Origin.ByAgent() {
		return fmt.Errorf(
			"task %d was written by the user and cannot be deleted — mark it superseded with a reason instead, "+
				"which leaves it visible and reversible", id)
	}
	l.items = append(l.items[:idx], l.items[idx+1:]...)
	return l.save()
}

// indexOfLocked finds an item by ID. The caller holds the lock.
func (l *TodoList) indexOfLocked(id int) (int, error) {
	for i := range l.items {
		if l.items[i].ID == id {
			return i, nil
		}
	}
	return 0, fmt.Errorf("no task with id %d", id)
}

// countOpenLocked counts tasks still considered outstanding.
func (l *TodoList) countOpenLocked() int {
	n := 0
	for _, it := range l.items {
		if it.State != TodoDone && it.State != TodoSuperseded {
			n++
		}
	}
	return n
}
