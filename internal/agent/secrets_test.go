package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"helix/internal/ai"
	"helix/internal/commands"
)

// askRecorder answers every yes/no question the same way and keeps them.
type askRecorder struct {
	answer bool
	asked  []string
}

func (r *askRecorder) AskYesNo(q string) bool                   { r.asked = append(r.asked, q); return r.answer }
func (r *askRecorder) AskLine(string) string                    { return "" }
func (r *askRecorder) AskTypedConfirmation(string, string) bool { return false }

// autoAgent is an agent in /permissions auto, in a directory holding a .env
// with a secret, answering confirmations with answer.
func autoAgent(t *testing.T, answer bool) (*Agent, *askRecorder) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("API_TOKEN=hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ag, _ := newTestAgent(t)
	ag.SetPermission(PermissionAuto)
	rec := &askRecorder{answer: answer}
	restore := commands.ActivePrompter()
	commands.SetPrompter(rec)
	t.Cleanup(func() { commands.SetPrompter(restore) })
	return ag, rec
}

// Reading a secret asks even under /permissions auto and from a trusted
// source, the two postures an injected "print the key" would ride through.
func TestFileReadOfASecretAlwaysAsks(t *testing.T) {
	step := ai.PlanStep{Tool: "file", Action: "read", Args: map[string]string{"path": ".env"}, Trusted: true}

	ag, rec := autoAgent(t, false)
	out, err := ag.handleFileStep(step)
	if err != nil || out != "" || len(rec.asked) != 1 {
		t.Fatalf("declined: out %q err %v asked %v", out, err, rec.asked)
	}

	ag, rec = autoAgent(t, true)
	out, err = ag.handleFileStep(step)
	if err != nil || !strings.Contains(out, "hunter2") || len(rec.asked) != 1 {
		t.Fatalf("approved: out %q err %v asked %v", out, err, rec.asked)
	}

	// An ordinary read in the same posture does not ask.
	ag, rec = autoAgent(t, false)
	if err := os.WriteFile("notes.md", []byte("ship on Friday"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = ag.handleFileStep(ai.PlanStep{Tool: "file", Action: "read", Args: map[string]string{"path": "notes.md"}})
	if err != nil || !strings.Contains(out, "ship on Friday") || len(rec.asked) != 0 {
		t.Fatalf("ordinary read: out %q err %v asked %v", out, err, rec.asked)
	}
}

func TestShellCommandTouchingASecretAlwaysAsks(t *testing.T) {
	ag, rec := autoAgent(t, false)
	if err := ag.handleShellStepWithEscalation(ai.PlanStep{Tool: "shell", Command: "cat .env", Trusted: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	if len(rec.asked) != 1 {
		t.Fatalf("asked %v", rec.asked)
	}
}

// A replay cannot ask, and what it reads reaches the model provider in the
// next round: it never reads a secret.
func TestReplayNeverReadsASecret(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("API_TOKEN=hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prompts := stubPlannerSequence(t,
		`{"intent":"file","steps":[{"tool":"file","action":"read","args":{"path":".env"}}]}`,
		`{"intent":"chat","steps":[{"tool":"response","message":"done"}]}`)
	ag, _ := newTestAgent(t)
	res, err := ag.ReplayRounds("what is in my env file?", nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	if s := res.Steps[0]; s.OK || !strings.Contains(s.Err, "not read in a replay") {
		t.Fatalf("step %+v", s)
	}
	for _, p := range *prompts {
		if strings.Contains(p, "hunter2") {
			t.Fatal("the secret reached the planner's prompt")
		}
	}
}
