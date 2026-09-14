// internal/hooks/file_event_test.go
// Purpose: the pre-file event must be able to refuse, and its match subject
// must carry enough to write a useful rule.
package hooks

import "testing"

// isPreEvent is what decides whether a blocking hook may DENY. An event missing
// from it still runs its hooks and still reports a non-zero exit — and then
// proceeds anyway. That is worse than having no hook: the user has a rule they
// can watch fire, output saying it refused, and the step happens regardless.
//
// The list is derived from Events() rather than repeated, so adding an event
// and forgetting to route it fails here.
func TestEveryPreEventCanBlock(t *testing.T) {
	for _, ev := range Events() {
		isPre := isPreEvent(ev)
		namedPre := len(ev) > 4 && ev[:4] == "pre-"
		switch {
		case namedPre && !isPre:
			t.Errorf("%q is named as a pre-event but isPreEvent says otherwise — a "+
				"blocking hook on it would print a refusal and let the step happen", ev)
		case !namedPre && isPre:
			t.Errorf("%q is not a pre-event but is treated as one; a post-* hook "+
				"cannot deny an action that already ran", ev)
		}
	}
}

func TestPreFileIsRoutable(t *testing.T) {
	if !isPreEvent(PreFile) {
		t.Fatal("pre-file cannot block")
	}
	if isPreEvent(PostFile) {
		t.Fatal("post-file can block; the write already happened")
	}
	if got, ok := ValidEvent("pre-file"); !ok || got != PreFile {
		t.Error("/hooks add pre-file would be refused as an unknown event")
	}
}

// A file rule wants to key on the action ("every write"), the path (".env,
// whatever is done to it"), or both. Matching on only one of them makes half
// the useful rules impossible to write.
func TestFileHookSubjectCarriesActionAndPath(t *testing.T) {
	c := Context{Tool: "file", Action: "write", Command: "secrets/prod.env"}
	got := hookSubject(c)
	if got != "write secrets/prod.env" {
		t.Fatalf("hookSubject = %q, want %q", got, "write secrets/prod.env")
	}

	// A rule anchored on the action must distinguish a write from a read of the
	// same file.
	h := Hook{Name: "n", Event: PreFile, Command: "true", Match: "^write "}
	s := &Set{Hooks: []Hook{h}}
	if err := s.compile(0); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !s.Hooks[0].Matches(hookSubject(c)) {
		t.Error("a `^write ` rule did not match a write step")
	}
	read := Context{Tool: "file", Action: "read", Command: "secrets/prod.env"}
	if s.Hooks[0].Matches(hookSubject(read)) {
		t.Error("a `^write ` rule matched a read step")
	}
}

// Other tools keep the old subject exactly — a shell hook matches the command.
func TestNonFileSubjectsAreUnchanged(t *testing.T) {
	if got := hookSubject(Context{Tool: "shell", Action: "run", Command: "rm -rf build"}); got != "rm -rf build" {
		t.Errorf("shell subject = %q, want the bare command", got)
	}
	if got := hookSubject(Context{Tool: "git", Action: "push"}); got != "push" {
		t.Errorf("git subject = %q, want the action when there is no command", got)
	}
}
