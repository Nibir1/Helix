package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"helix/internal/agent"
	"helix/internal/ai"
)

var updateReplay = flag.Bool("update-replay", false, "rewrite testdata/replay_v1.ndjson")

type fakePlanner struct{ got []string }

func (f *fakePlanner) PlanReplay(request string, lessons []agent.LearnedLesson) (*ai.Plan, error) {
	f.got = append(f.got, request)
	if request == "fail" {
		return nil, errors.New("planner unavailable")
	}
	steps := []ai.PlanStep{{Tool: "file", Action: "read", Args: map[string]string{"path": "package.json"}}}
	if len(lessons) > 0 {
		steps = []ai.PlanStep{{Tool: "file", Action: "read", Args: map[string]string{"path": "go.mod"}},
			{Tool: "shell", Command: "go build ./..."}}
	}
	steps = append(steps, ai.PlanStep{Tool: "response", Message: "Built it."})
	return &ai.Plan{Steps: steps}, nil
}

func TestServeReplaysAnswersEveryLineInOrder(t *testing.T) {
	in := strings.Join([]string{
		`{"v":1,"id":"e1","request":"build the project"}`,
		``,
		`{"v":1,"id":"e1","request":"build the project","lessons":[{"id":"L1","text":"This is a Go module."}]}`,
		`not json`,
		`{"v":2,"id":"e3","request":"x"}`,
		`{"v":1,"id":"e4","request":"fail"}`,
		`{"v":1,"id":"e5","request":"x","surprise":true}`,
	}, "\n")
	var out bytes.Buffer
	fp := &fakePlanner{}
	if err := serveReplays(strings.NewReader(in), &out, fp); err != nil {
		t.Fatal(err)
	}
	var got []replayResponse
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var r replayResponse
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("stdout carries a non-protocol line %q", line)
		}
		got = append(got, r)
	}
	if len(got) != 6 {
		t.Fatalf("%d responses for 6 requests", len(got))
	}
	if !got[0].OK || got[0].Steps[0].Subject != "read package.json" || got[0].Reply != "Built it." {
		t.Errorf("without lessons: %+v", got[0])
	}
	if !got[1].OK || got[1].Steps[0].Subject != "read go.mod" || got[1].Steps[1].Subject != "go build ./..." {
		t.Errorf("with lessons: %+v", got[1])
	}
	for i, want := range map[int]string{2: "malformed", 3: "version 2", 4: "planner unavailable", 5: "malformed"} {
		if got[i].OK || !strings.Contains(got[i].Error, want) {
			t.Errorf("response %d: want an error containing %q, got %+v", i, want, got[i])
		}
	}
	if len(fp.got) != 3 {
		t.Errorf("the planner ran %d times; malformed and wrong-version lines must not reach it", len(fp.got))
	}
}

// TestReplayWireGolden pins the replay protocol. The engine keeps a copy at
// adapters/remote/helix/testdata/replay_v1.ndjson and decodes it with unknown
// fields refused. If this fails, the protocol changed: bump ReplayWireVersion
// and update both copies.
func TestReplayWireGolden(t *testing.T) {
	req := replayRequest{V: ReplayWireVersion, ID: "0199a1b2c3d4e5f6a7b8c9d0e1f2", Request: "build the project",
		Lessons: []agent.LearnedLesson{{ID: "01a0f7afc76b7637f51421cbd985", Text: "This is a Go module: read go.mod."}}}
	resp := replayResponse{V: ReplayWireVersion, ID: req.ID, OK: true,
		Steps: []replayStep{{Tool: "file", Action: "read", Subject: "read go.mod"}, {Tool: "shell", Subject: "go build ./..."},
			{Tool: "response"}},
		Reply: "Built it.", Usage: replayUsage{ModelCalls: 1, InputChars: 9100, OutputChars: 240}}
	fail := replayResponse{V: ReplayWireVersion, ID: req.ID, Error: "planner unavailable"}
	var buf bytes.Buffer
	for _, v := range []any{req, resp, fail} {
		line, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(line, '\n'))
	}
	golden := filepath.Join("testdata", "replay_v1.ndjson")
	if *updateReplay {
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update-replay to create it)", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("replay wire changed.\n got: %s\nwant: %s", buf.String(), want)
	}
}

func TestReplayCommandOnlyAnswersToReplay(t *testing.T) {
	if handled, _ := runReplayCommand([]string{"remote", "status"}); handled {
		t.Fatal("helix replay claimed another command")
	}
	if handled, code := runReplayCommand([]string{"replay", "extra"}); !handled || code != 2 {
		t.Fatalf("extra args: handled=%v code=%d", handled, code)
	}
}
