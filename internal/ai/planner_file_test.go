// internal/ai/planner_file_test.go
// Purpose: the `file` tool widens what Helix can do; it must not widen what the
// planner is allowed to emit.
package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// filePlan builds a plan the way the planner really delivers one — as model
// output through the parser — so the test exercises the same path a live turn
// does rather than a struct the parser might never produce.
func filePlan(t *testing.T, action string, args map[string]string, extra string) (*Plan, error) {
	t.Helper()
	a, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return ParsePlanFromModelOutput(fmt.Sprintf(
		`{"intent":"chat","steps":[{"tool":"file","action":%q,"args":%s%s}]}`,
		action, a, extra))
}

// The action vocabulary closes like every other tool's.
func TestFileActionVocabularyIsClosed(t *testing.T) {
	if _, err := filePlan(t, "chmod", map[string]string{"path": "x"}, ""); err == nil {
		t.Fatal("an unsupported file action survived validation")
	}
}

// Synonyms normalize rather than failing the turn — the same courtesy vision
// gets — but only the ones named.
func TestFileActionSynonymsNormalize(t *testing.T) {
	cases := map[string]string{
		"cat": "read", "read": "read",
		"ls": "list", "dir": "list", "list": "list",
		"find": "glob", "glob": "glob",
		"search": "grep", "grep": "grep",
		"replace": "edit", "edit": "edit",
		"create": "write", "write": "write",
	}
	for given, want := range cases {
		args := map[string]string{"path": "a.go", "pattern": "x", "old_string": "y", "content": "z"}
		p, err := filePlan(t, given, args, "")
		if err != nil {
			t.Errorf("action %q was dropped: %v", given, err)
			continue
		}
		if got := p.Steps[0].Action; got != want {
			t.Errorf("action %q normalized to %q, want %q", given, got, want)
		}
	}
}

// A step missing its required argument cannot run, so it is dropped at
// validation rather than discovered at dispatch and failing the whole turn.
func TestFileStepsMissingRequiredArgsAreDropped(t *testing.T) {
	cases := []struct {
		action string
		args   map[string]string
		why    string
	}{
		{"read", map[string]string{}, "no path"},
		{"glob", map[string]string{"path": "."}, "no pattern"},
		{"grep", map[string]string{"path": "."}, "no pattern"},
		{"edit", map[string]string{"path": "a.go"}, "no old_string"},
		{"edit", map[string]string{"old_string": "x"}, "no path"},
		{"write", map[string]string{"content": "x"}, "no path"},
	}
	for _, c := range cases {
		if _, err := filePlan(t, c.action, c.args, ""); err == nil {
			t.Errorf("file/%s with %s survived validation", c.action, c.why)
		}
	}
}

// list defaults to "." — asking the model for a path it does not need is the
// kind of friction that makes it emit a shell step instead.
func TestFileListDefaultsToCurrentDirectory(t *testing.T) {
	p, err := filePlan(t, "list", map[string]string{}, "")
	if err != nil {
		t.Fatalf("a bare list was dropped: %v", err)
	}
	if got := p.Steps[0].Args["path"]; got != "." {
		t.Errorf("list path defaulted to %q, want %q", got, ".")
	}
}

// A file step must never carry a raw command. If it could, the file tool would
// be a second door into shell execution that skips the shell tool's validation.
func TestFileStepNeverCarriesACommand(t *testing.T) {
	p, err := filePlan(t, "read", map[string]string{"path": "a.go"}, `,"command":"rm -rf /"`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Steps[0].Command != "" {
		t.Errorf("a file step kept a command: %q — that is a shell bypass", p.Steps[0].Command)
	}
}

// The prompt must not still be teaching the model the technique this tool
// replaced. An in-place sed that does not match exits 0 and changes nothing.
func TestPlannerNoLongerTeachesInPlaceSed(t *testing.T) {
	prompt := strings.ToLower(BuildPlannerPrompt("do a thing", "", ""))
	for _, banned := range []string{"sed -i", "perl -pi"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("the planner prompt still recommends %q; file/edit exists so that a "+
				"non-matching edit fails instead of silently succeeding", banned)
		}
	}
	if !strings.Contains(prompt, "file tool rules") {
		t.Error("the planner prompt does not document the file tool, so the model cannot emit one")
	}
}
