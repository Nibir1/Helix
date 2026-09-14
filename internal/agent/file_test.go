// internal/agent/file_test.go
// Purpose: the file tool arrives through the same gates as every other tool.
//
// A capability that reaches the filesystem is exactly where a harness grows a
// hole, so the properties pinned here are the ones that would not fail loudly
// if they broke: a write graded Low would run without asking under the default
// posture and nobody would notice until a file changed.
package agent

import (
	"strings"
	"testing"

	"helix/internal/commands"
	"helix/internal/hooks"
)

// Reads change nothing; writes change the machine. Under the default `ask`
// posture Low runs and Medium asks, so this mapping IS the question the user
// gets asked — grading a write Low removes that question silently.
func TestFileMutationTiers(t *testing.T) {
	for _, action := range []string{"read", "list", "glob", "grep"} {
		if fileMutates(action) {
			t.Errorf("file/%s is graded as a mutation; it changes nothing and would "+
				"then ask for confirmation on every read", action)
		}
	}
	for _, action := range []string{"edit", "write"} {
		if !fileMutates(action) {
			t.Errorf("file/%s is not graded as a mutation, so it would be graded Low "+
				"and run with no confirmation under the default posture", action)
		}
	}
}

// The tier that the handler derives from fileMutates must be the tier the
// posture table is written against.
func TestFileRiskMatchesTheMutationGrade(t *testing.T) {
	tier := func(action string) commands.ShellRiskLevel {
		if fileMutates(action) {
			return commands.ShellRiskMedium
		}
		return commands.ShellRiskLow
	}
	if tier("read") != commands.ShellRiskLow {
		t.Error("a read is not Low")
	}
	if tier("write") != commands.ShellRiskMedium {
		t.Error("a write is not Medium")
	}
	// Nothing here is High today. If that changes, the handler's High branch
	// must already exist — which it does — so this records the intent rather
	// than asserting a permanent truth.
	if tier("edit") == commands.ShellRiskHigh {
		t.Error("an edit is graded High, which would block it in every posture")
	}
}

// Every event the package advertises must be routable, or /hooks offers a rule
// that silently never fires.
func TestEveryAdvertisedEventIsValid(t *testing.T) {
	for _, ev := range hooks.Events() {
		got, ok := hooks.ValidEvent(string(ev))
		if !ok || got != ev {
			t.Errorf("hooks.Events() advertises %q but ValidEvent rejects it", ev)
		}
	}
	if _, ok := hooks.ValidEvent("pre-file"); !ok {
		t.Error("pre-file is not a valid event name, so /hooks add would refuse it")
	}
}

// The line the user sees names the file. "edit" on its own tells them nothing
// about what they are approving.
func TestFileSubjectNamesTheTarget(t *testing.T) {
	cases := []struct {
		action string
		args   map[string]string
		want   []string
	}{
		{"edit", map[string]string{"path": "internal/ai/planner.go"}, []string{"edit", "planner.go"}},
		{"write", map[string]string{"path": "README.md"}, []string{"write", "README.md"}},
		{"grep", map[string]string{"pattern": "TODO", "path": "internal"}, []string{"grep", "TODO", "internal"}},
		{"glob", map[string]string{"pattern": "**/*_test.go"}, []string{"glob", "**/*_test.go"}},
		{"list", map[string]string{}, []string{"list", "."}},
	}
	for _, c := range cases {
		got := fileSubject(c.action, c.args)
		for _, want := range c.want {
			if !strings.Contains(got, want) {
				t.Errorf("fileSubject(%q, %v) = %q, missing %q", c.action, c.args, got, want)
			}
		}
	}
}

// The confirmation prompt has to say what is about to change. "edit file.go"
// restates the subject; a user deciding whether to approve needs the effect —
// which is the difference between one occurrence and every occurrence.
func TestFileChangeReasonNamesTheEffect(t *testing.T) {
	one := fileChangeReason("edit", map[string]string{
		"path": "a.go", "old_string": "hello", "replace_all": "false"})
	all := fileChangeReason("edit", map[string]string{
		"path": "a.go", "old_string": "hello", "replace_all": "true"})

	if !strings.Contains(one, "first occurrence") {
		t.Errorf("a single-occurrence edit is described as %q", one)
	}
	if !strings.Contains(all, "EVERY occurrence") {
		t.Errorf("a replace_all edit is described as %q — the user cannot tell it "+
			"apart from a single replacement", all)
	}
	if one == all {
		t.Error("replace_all does not change what the confirmation says")
	}

	w := fileChangeReason("write", map[string]string{"path": "a.go", "content": "12345"})
	if !strings.Contains(w, "entire contents") || !strings.Contains(w, "5 bytes") {
		t.Errorf("a whole-file write is described as %q; it must say it replaces "+
			"everything and how much is coming", w)
	}
}
