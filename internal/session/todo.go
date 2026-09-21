// internal/session/todo.go
// Purpose: the agentic harness's task list — the visible plan of record for
// multi-step work, persisted to ~/.helix/todo.json (0600).
//
// Why it lives in session rather than agent: the list must outlive one turn and
// one process. An agentic loop that replans across several turns, or a session
// resumed tomorrow, needs the same list; state that only exists inside a single
// HandleInput call cannot serve that.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TodoState is a task's lifecycle position.
type TodoState string

const (
	TodoPending    TodoState = "pending"
	TodoInProgress TodoState = "in_progress"
	TodoDone       TodoState = "done"
	TodoBlocked    TodoState = "blocked"

	// TodoSuperseded is the state the agent reaches for when the work it has
	// already done shows a task is no longer the right thing to do.
	//
	// It exists because neither of the alternatives is honest. Marking it
	// "done" claims work that never happened. Deleting it destroys a task the
	// user wrote and still believes is tracked. Superseded says what actually
	// occurred — this was the plan, it stopped being the plan, and here is the
	// reason — and it is reversible with `/todo open <id>`.
	TodoSuperseded TodoState = "superseded"
)

// TodoOrigin records who put an item on the list.
//
// The field exists because the list has two authors now. Before the agent could
// write to it, every item was the user's and provenance was implicit; an agent
// that can revise and supersede makes "who wrote this" a question with
// consequences, and the answer must not be a guess.
type TodoOrigin string

const (
	// TodoFromUser is the ZERO VALUE on purpose. Every item in a todo.json
	// written before this field existed was typed by a human, so an absent
	// origin decoding to "user" is not a default — it is the correct answer.
	TodoFromUser  TodoOrigin = ""
	TodoFromAgent TodoOrigin = "agent"
)

// ByAgent reports whether the agent created this item.
func (o TodoOrigin) ByAgent() bool { return o == TodoFromAgent }

// Label renders the origin for a list the user is reading.
func (o TodoOrigin) Label() string {
	if o.ByAgent() {
		return "helix"
	}
	return "you"
}

// ValidTodoState reports whether s names a real state, and returns the
// canonical form. Callers use it to reject typos instead of silently writing a
// state nothing will ever match.
func ValidTodoState(s string) (TodoState, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pending", "todo", "open":
		return TodoPending, true
	case "in_progress", "in-progress", "active", "doing", "wip":
		return TodoInProgress, true
	case "done", "complete", "completed", "finished":
		return TodoDone, true
	case "blocked", "block", "stuck":
		return TodoBlocked, true
	case "superseded", "supersede", "obsolete", "dropped":
		return TodoSuperseded, true
	}
	return "", false
}

// Symbol is the one-glyph state marker for list rendering.
func (s TodoState) Symbol() string {
	switch s {
	case TodoDone:
		return "✔"
	case TodoInProgress:
		return "▸"
	case TodoBlocked:
		return "✖"
	case TodoSuperseded:
		return "⊘"
	default:
		return "·"
	}
}

// TodoItem is one task.
type TodoItem struct {
	ID     int        `json:"id"`
	Text   string     `json:"text"`
	State  TodoState  `json:"state"`
	Note   string     `json:"note,omitempty"`
	Origin TodoOrigin `json:"origin,omitempty"`

	// WasText is the user's own wording, kept when the agent rewrites an item.
	// The agent may redirect the plan — that is the point of letting it write
	// here — but it may not quietly replace what you asked for with what it
	// decided you meant. Empty unless a user-authored item was revised.
	WasText string `json:"was_text,omitempty"`

	// Reason is why the agent added, revised or superseded this item. Required
	// of the agent for every change to a user-authored item: a plan that
	// changed under you without saying why is indistinguishable from a plan
	// that lost track of itself.
	Reason string `json:"reason,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Revised reports whether the agent rewrote text the user originally wrote.
func (i TodoItem) Revised() bool { return i.WasText != "" }

// TodoList is the persisted task list.
type TodoList struct {
	mu     sync.Mutex
	path   string
	items  []TodoItem
	nextID int
}

type todoFile struct {
	NextID int        `json:"next_id"`
	Items  []TodoItem `json:"items"`
}

// TodoPath returns ~/.helix/todo.json.
func TodoPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helix", "todo.json"), nil
}

// NewTodoList loads (or starts) the task list at the default path.
func NewTodoList() (*TodoList, error) {
	path, err := TodoPath()
	if err != nil {
		return nil, err
	}
	return NewTodoListAt(path)
}

// NewTodoListAt uses an explicit path (tests).
func NewTodoListAt(path string) (*TodoList, error) {
	l := &TodoList{path: path, nextID: 1}
	if err := l.load(); err != nil {
		return nil, err
	}
	return l, nil
}

// Items returns a copy of the list in insertion order.
func (l *TodoList) Items() []TodoItem {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]TodoItem(nil), l.items...)
}

// Add appends a task and returns it.
func (l *TodoList) Add(text string) (TodoItem, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return TodoItem{}, fmt.Errorf("task text cannot be empty")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	item := TodoItem{
		ID:        l.nextID,
		Text:      text,
		State:     TodoPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	l.nextID++
	l.items = append(l.items, item)
	return item, l.save()
}

// SetState moves a task to a new state. Returns the updated task.
func (l *TodoList) SetState(id int, state TodoState, note string) (TodoItem, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.items {
		if l.items[i].ID != id {
			continue
		}
		l.items[i].State = state
		l.items[i].UpdatedAt = time.Now()
		if note != "" {
			l.items[i].Note = note
		}
		return l.items[i], l.save()
	}
	return TodoItem{}, fmt.Errorf("no task with id %d", id)
}

// Remove deletes one task by ID.
func (l *TodoList) Remove(id int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.items {
		if l.items[i].ID == id {
			l.items = append(l.items[:i], l.items[i+1:]...)
			return l.save()
		}
	}
	return fmt.Errorf("no task with id %d", id)
}

// Clear empties the list. IDs restart, because a cleared list is a new plan.
func (l *TodoList) Clear() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = nil
	l.nextID = 1
	return l.save()
}

// PruneDone drops completed tasks and reports how many went. Unlike Clear it
// keeps the ID counter, so surviving tasks keep the IDs the user just read.
func (l *TodoList) PruneDone() (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := make([]TodoItem, 0, len(l.items))
	removed := 0
	for _, it := range l.items {
		if it.State == TodoDone {
			removed++
			continue
		}
		kept = append(kept, it)
	}
	l.items = kept
	return removed, l.save()
}

// Counts returns per-state totals for a compact status line.
func (l *TodoList) Counts() map[TodoState]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[TodoState]int{}
	for _, it := range l.items {
		out[it.State]++
	}
	return out
}

// Summary renders the list as the fenced, data-only block the planner sees.
// Returns "" when there is nothing open, so a finished list adds no prompt
// weight.
func (l *TodoList) Summary(max int) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var open []TodoItem
	for _, it := range l.items {
		// Done and superseded are both settled. Presenting finished work as
		// outstanding invites the planner to redo it; presenting a task the
		// agent already set aside invites it to re-litigate its own decision.
		if it.State == TodoDone || it.State == TodoSuperseded {
			continue
		}
		open = append(open, it)
	}
	if len(open) == 0 {
		return ""
	}
	if max > 0 && len(open) > max {
		open = open[:max]
	}
	var b strings.Builder
	for _, it := range open {
		// The ID is here so the planner can ADDRESS an item — without it the
		// agent can read the list but has no way to say which task it means.
		// The author is here so it knows whose plan it is revising: changing
		// its own note is bookkeeping, changing the user's is an overrule.
		fmt.Fprintf(&b, "- #%d [%s] (by %s) %s\n", it.ID, it.State, it.Origin.Label(), it.Text)
	}
	return b.String()
}

func (l *TodoList) load() error {
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var f todoFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("todo list is corrupt (%s): %w", l.path, err)
	}
	l.items = f.Items
	l.nextID = f.NextID
	if l.nextID <= 0 {
		l.nextID = 1
	}
	// A hand-edited file can hold IDs at or above next_id; without this, Add
	// would mint a duplicate and SetState would hit the wrong task.
	for _, it := range l.items {
		if it.ID >= l.nextID {
			l.nextID = it.ID + 1
		}
	}
	return nil
}

// save persists the list. Callers hold l.mu.
func (l *TodoList) save() error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(todoFile{NextID: l.nextID, Items: l.items}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(l.path, data, 0o600)
}
