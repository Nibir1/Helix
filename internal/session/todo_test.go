package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newList(t *testing.T) (*TodoList, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "todo.json")
	l, err := NewTodoListAt(path)
	if err != nil {
		t.Fatal(err)
	}
	return l, path
}

func TestTodoAddAndPersist(t *testing.T) {
	l, path := newList(t)

	a, err := l.Add("write the parser")
	if err != nil {
		t.Fatal(err)
	}
	b, err := l.Add("write its tests")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatalf("IDs must be unique, both were %d", a.ID)
	}
	if a.State != TodoPending {
		t.Errorf("new task state = %q, want pending", a.State)
	}
	if _, err := l.Add("   "); err == nil {
		t.Error("an empty task must be rejected")
	}

	reloaded, err := NewTodoListAt(path)
	if err != nil {
		t.Fatal(err)
	}
	items := reloaded.Items()
	if len(items) != 2 {
		t.Fatalf("reload lost tasks: %d", len(items))
	}
	// Insertion order is what the user just read off the screen.
	if items[0].Text != "write the parser" || items[1].Text != "write its tests" {
		t.Errorf("order not preserved: %+v", items)
	}

	// A reloaded list must not reuse an existing ID.
	next, err := reloaded.Add("third")
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == a.ID || next.ID == b.ID {
		t.Errorf("reloaded list minted a duplicate ID %d", next.ID)
	}
}

// TestTodoLoadRecoversFromStaleNextID guards a hand-edited file: an ID at or
// above next_id would otherwise be duplicated by the next Add, and SetState
// would then hit the wrong task.
func TestTodoLoadRecoversFromStaleNextID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todo.json")
	body := `{"next_id":1,"items":[{"id":7,"text":"hand written","state":"pending"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := NewTodoListAt(path)
	if err != nil {
		t.Fatal(err)
	}
	added, err := l.Add("next")
	if err != nil {
		t.Fatal(err)
	}
	if added.ID <= 7 {
		t.Errorf("new ID %d collides with the hand-written 7", added.ID)
	}
}

func TestTodoLoadRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todo.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTodoListAt(path); err == nil {
		t.Error("a corrupt task file must be reported, not silently emptied")
	}
}

func TestTodoStateTransitions(t *testing.T) {
	l, _ := newList(t)
	item, _ := l.Add("ship it")

	if _, err := l.SetState(item.ID, TodoInProgress, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetState(item.ID, TodoBlocked, "waiting on review"); err != nil {
		t.Fatal(err)
	}
	got := l.Items()[0]
	if got.State != TodoBlocked {
		t.Errorf("state = %q, want blocked", got.State)
	}
	if got.Note != "waiting on review" {
		t.Errorf("note = %q, want the reason it is blocked", got.Note)
	}
	// An empty note must not erase the reason already recorded.
	if _, err := l.SetState(item.ID, TodoDone, ""); err != nil {
		t.Fatal(err)
	}
	if l.Items()[0].Note != "waiting on review" {
		t.Error("an empty note cleared the existing one")
	}
	if _, err := l.SetState(9999, TodoDone, ""); err == nil {
		t.Error("an unknown ID must report an error")
	}
}

func TestTodoRemovePruneClear(t *testing.T) {
	l, _ := newList(t)
	a, _ := l.Add("a")
	b, _ := l.Add("b")
	c, _ := l.Add("c")

	if _, err := l.SetState(b.ID, TodoDone, ""); err != nil {
		t.Fatal(err)
	}
	n, err := l.PruneDone()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
	ids := []int{l.Items()[0].ID, l.Items()[1].ID}
	if ids[0] != a.ID || ids[1] != c.ID {
		t.Errorf("prune must keep the IDs the user just read: got %v", ids)
	}

	if err := l.Remove(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.Remove(a.ID); err == nil {
		t.Error("removing an absent task must report it")
	}
	if err := l.Clear(); err != nil {
		t.Fatal(err)
	}
	if len(l.Items()) != 0 {
		t.Error("clear left tasks behind")
	}
	// Clear is a new plan, so IDs restart.
	fresh, _ := l.Add("first of the new plan")
	if fresh.ID != 1 {
		t.Errorf("post-clear ID = %d, want 1", fresh.ID)
	}
}

func TestTodoCountsAndSummary(t *testing.T) {
	l, _ := newList(t)
	if l.Summary(10) != "" {
		t.Error("an empty list must contribute nothing to the prompt")
	}

	open, _ := l.Add("open work")
	done, _ := l.Add("finished work")
	if _, err := l.SetState(done.ID, TodoDone, ""); err != nil {
		t.Fatal(err)
	}

	counts := l.Counts()
	if counts[TodoPending] != 1 || counts[TodoDone] != 1 {
		t.Errorf("counts = %v", counts)
	}

	summary := l.Summary(10)
	if !strings.Contains(summary, open.Text) {
		t.Errorf("summary should name the open task: %q", summary)
	}
	// A completed task is not outstanding work; including it would tell the
	// planner to redo it.
	if strings.Contains(summary, done.Text) {
		t.Errorf("summary must exclude completed tasks: %q", summary)
	}

	for i := 0; i < 5; i++ {
		if _, err := l.Add("extra"); err != nil {
			t.Fatal(err)
		}
	}
	if lines := strings.Count(l.Summary(2), "\n"); lines != 2 {
		t.Errorf("Summary(2) produced %d lines, want 2", lines)
	}
	if l.Summary(0) == "" {
		t.Error("Summary(0) means unbounded, not empty")
	}
}

func TestValidTodoState(t *testing.T) {
	cases := map[string]TodoState{
		"pending": TodoPending, "todo": TodoPending, "open": TodoPending,
		"in-progress": TodoInProgress, "wip": TodoInProgress, "DOING": TodoInProgress,
		"done": TodoDone, "completed": TodoDone,
		"blocked": TodoBlocked, " stuck ": TodoBlocked,
	}
	for input, want := range cases {
		got, ok := ValidTodoState(input)
		if !ok || got != want {
			t.Errorf("ValidTodoState(%q) = %q,%v; want %q", input, got, ok, want)
		}
	}
	if _, ok := ValidTodoState("almost"); ok {
		t.Error("an unknown state must be rejected, not coerced")
	}
	for _, s := range []TodoState{TodoPending, TodoInProgress, TodoDone, TodoBlocked} {
		if s.Symbol() == "" {
			t.Errorf("state %q has no display symbol", s)
		}
	}
}

func TestTodoFileIsNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	l, path := newList(t)
	if _, err := l.Add("private plan"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("todo.json mode = %o, want 600", perm)
	}

	// And it must be valid JSON, since a hand-edit is an expected workflow.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Errorf("todo.json is not valid JSON: %v", err)
	}
}
