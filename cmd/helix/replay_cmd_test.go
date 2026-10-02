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

var updateReplay = flag.Bool("update-replay", false, "rewrite testdata/replay_v1.ndjson and replay_v2.ndjson")

type fakePlanner struct {
	got    []string
	rounds []int
}

// ReplayRounds fakes a replay: with more than one round, the first round's
// read ran and a second round answered.
func (f *fakePlanner) ReplayRounds(request string, lessons []agent.LearnedLesson, maxRounds int) (*agent.ReplayResult, error) {
	f.got = append(f.got, request)
	f.rounds = append(f.rounds, maxRounds)
	if request == "fail" {
		return nil, errors.New("planner unavailable")
	}
	read := ai.PlanStep{Tool: "file", Action: "read", Args: map[string]string{"path": "package.json"}}
	if len(lessons) > 0 {
		read.Args["path"] = "go.mod"
	}
	res := &agent.ReplayResult{Rounds: 1, End: agent.ReplayEndPlanned,
		Steps: []agent.ReplayStep{{Step: read, Round: 1, OK: true}}}
	if len(lessons) > 0 && maxRounds <= 1 {
		res.Steps = append(res.Steps, agent.ReplayStep{Step: ai.PlanStep{Tool: "shell", Command: "go build ./..."}, Round: 1, OK: true})
	}
	if maxRounds > 1 {
		res.Steps[0].Ran = true
		if read.Args["path"] == "package.json" {
			res.Steps[0].OK, res.Steps[0].Err = false, "no such file"
		}
		res.Steps = append(res.Steps, agent.ReplayStep{Step: ai.PlanStep{Tool: "response", Message: "Built it."}, Round: 2, OK: true})
		res.Rounds, res.End = 2, agent.ReplayEndAnswered
	} else {
		res.Steps = append(res.Steps, agent.ReplayStep{Step: ai.PlanStep{Tool: "response", Message: "Built it."}, Round: 1, OK: true})
	}
	res.Reply = "Built it."
	return res, nil
}

func TestServeReplaysAnswersEveryLineInOrder(t *testing.T) {
	in := strings.Join([]string{
		`{"v":1,"id":"e1","request":"build the project"}`,
		``,
		`{"v":1,"id":"e1","request":"build the project","lessons":[{"id":"L1","text":"This is a Go module."}]}`,
		`not json`,
		`{"v":3,"id":"e3","request":"x"}`,
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
	for i, want := range map[int]string{2: "malformed", 3: "version 3", 4: "planner unavailable", 5: "malformed"} {
		if got[i].OK || !strings.Contains(got[i].Error, want) {
			t.Errorf("response %d: want an error containing %q, got %+v", i, want, got[i])
		}
	}
	if len(fp.got) != 3 {
		t.Errorf("the planner ran %d times; malformed and wrong-version lines must not reach it", len(fp.got))
	}
	if got[0].V != 1 || got[0].Rounds != 0 || got[0].Steps[0].Round != 0 || got[0].End != "" {
		t.Errorf("a v1 request got v2 fields: %+v", got[0])
	}
}

// A v2 request asks for rounds; the response says which round planned each
// step, which steps ran and how, and how the replay ended. A v1 request may
// not ask for rounds, and no request gets more than maxReplayRounds.
func TestServeReplaysV2Rounds(t *testing.T) {
	in := strings.Join([]string{
		`{"v":2,"id":"e1","request":"build the project","rounds":4}`,
		`{"v":2,"id":"e2","request":"build the project"}`,
		`{"v":1,"id":"e3","request":"build the project","rounds":4}`,
		`{"v":2,"id":"e4","request":"build the project","rounds":99}`,
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
			t.Fatal(err)
		}
		got = append(got, r)
	}
	r := got[0]
	if r.V != 2 || r.Rounds != 2 || r.End != agent.ReplayEndAnswered || r.Steps[0].Outcome != "failed" ||
		r.Steps[0].Err != "no such file" || r.Steps[0].Round != 1 || r.Steps[1].Round != 2 {
		t.Errorf("v2 rounds: %+v", r)
	}
	if got[1].Rounds != 1 || got[1].End != agent.ReplayEndPlanned || got[1].Steps[0].Outcome != "" {
		t.Errorf("v2 without rounds: %+v", got[1])
	}
	if got[2].OK || !strings.Contains(got[2].Error, "version 2") {
		t.Errorf("v1 with rounds: %+v", got[2])
	}
	if fp.rounds[0] != 4 || fp.rounds[1] != 1 || fp.rounds[len(fp.rounds)-1] != maxReplayRounds {
		t.Errorf("rounds passed to the planner: %v", fp.rounds)
	}
}

// TestReplayWireGolden pins the replay protocol. The engine keeps a copy at
// adapters/remote/helix/testdata/replay_v1.ndjson and decodes it with unknown
// fields refused. If this fails, the protocol changed: bump ReplayWireVersion
// and update both copies.
func TestReplayWireGolden(t *testing.T) {
	req := replayRequest{V: 1, ID: "0199a1b2c3d4e5f6a7b8c9d0e1f2", Request: "build the project",
		Lessons: []agent.LearnedLesson{{ID: "01a0f7afc76b7637f51421cbd985", Text: "This is a Go module: read go.mod."}}}
	resp := replayResponse{V: 1, ID: req.ID, OK: true,
		Steps: []replayStep{{Tool: "file", Action: "read", Subject: "read go.mod"}, {Tool: "shell", Subject: "go build ./..."},
			{Tool: "response"}},
		Reply: "Built it.", Usage: replayUsage{ModelCalls: 1, InputChars: 9100, OutputChars: 240}}
	fail := replayResponse{V: 1, ID: req.ID, Error: "planner unavailable"}
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

// TestReplayWireGoldenV2 pins version 2 (D-021). The engine keeps a copy at
// adapters/remote/helix/testdata/replay_v2.ndjson.
func TestReplayWireGoldenV2(t *testing.T) {
	req := replayRequest{V: 2, ID: "0199a1b2c3d4e5f6a7b8c9d0e1f2", Request: "what does the verify package do?",
		Lessons: []agent.LearnedLesson{{ID: "01a0f7afc76b7637f51421cbd985", Text: "Read the named file once and answer from it."}},
		Rounds:  4}
	resp := replayResponse{V: 2, ID: req.ID, OK: true,
		Steps: []replayStep{
			{Tool: "file", Action: "glob", Subject: "glob **/verify/*.go", Round: 1, Outcome: "ok"},
			{Tool: "file", Action: "read", Subject: "read verify/verify.go", Round: 2, Outcome: "ok"},
			{Tool: "file", Action: "read", Subject: "read verify/missing.go", Round: 2, Outcome: "failed", Err: "no such file or directory"},
			{Tool: "response", Round: 3},
		},
		Reply: "It is the nutrient test.", Rounds: 3, End: "answered",
		Usage: replayUsage{ModelCalls: 3, InputChars: 27300, OutputChars: 720}}
	var buf bytes.Buffer
	for _, v := range []any{req, resp} {
		line, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(line, '\n'))
	}
	golden := filepath.Join("testdata", "replay_v2.ndjson")
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
		t.Fatalf("replay wire v2 changed.\n got: %s\nwant: %s", buf.String(), want)
	}
}
