// cmd/helix/replay_cmd.go
// Purpose: `helix replay` — plan past requests again, with lessons, without
// executing anything. The host half of Metabolism's nutrient test
// (docs/harness.md §11).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"helix/internal/agent"
	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/daemon"
	"helix/internal/shell"
)

// ReplayWireVersion is the newest replay protocol version. Bump it on any
// change a reader could misparse; the engine pins each version with a golden
// file. Version 2 (D-021) adds rounds to the request and each step's round
// and outcome to the response. A response has its request's version, so a
// v1 engine still gets v1 bytes.
const ReplayWireVersion = 2

// maxReplayRounds caps what a request may ask for. The live follow-up budget
// (3 for a file lookup) ends a replay well before this.
const maxReplayRounds = 8

// replayRequest is one NDJSON line on stdin.
type replayRequest struct {
	V       int                   `json:"v"`
	ID      string                `json:"id"`
	Request string                `json:"request"`
	Lessons []agent.LearnedLesson `json:"lessons,omitempty"`
	// Rounds is how many planner rounds to replay (v2). 0 or 1 is a first
	// plan that executes nothing; more runs read-only file steps between
	// rounds (docs/harness.md §11).
	Rounds int `json:"rounds,omitempty"`
}

// replayStep is one planned step. In v2 it says which round planned it and,
// for a read-only file step that ran, how that went: Outcome is "" (planned,
// not run), "ok" or "failed".
type replayStep struct {
	Tool    string `json:"tool"`
	Action  string `json:"action,omitempty"`
	Subject string `json:"subject,omitempty"`
	Round   int    `json:"round,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	Err     string `json:"err,omitempty"`
}

type replayUsage struct {
	ModelCalls  int `json:"model_calls"`
	InputChars  int `json:"input_chars"`
	OutputChars int `json:"output_chars"`
}

// replayResponse is one NDJSON line on stdout, in request order.
type replayResponse struct {
	V     int          `json:"v"`
	ID    string       `json:"id"`
	OK    bool         `json:"ok"`
	Error string       `json:"error,omitempty"`
	Steps []replayStep `json:"steps,omitempty"`
	Reply string       `json:"reply,omitempty"`
	// Rounds and End (v2): how many rounds were planned and how the replay
	// ended (agent.ReplayEnd*).
	Rounds int         `json:"rounds,omitempty"`
	End    string      `json:"end,omitempty"`
	Usage  replayUsage `json:"usage"`
}

// replayPrompter refuses every confirmation. A replay plans and never runs,
// so nothing should ask; if something does, the answer is no.
type replayPrompter struct{}

func (replayPrompter) AskYesNo(string) bool                     { return false }
func (replayPrompter) AskLine(string) string                    { return "" }
func (replayPrompter) AskTypedConfirmation(string, string) bool { return false }
func (replayPrompter) Unattended() bool                         { return true }

// runReplayCommand handles `helix replay`. It reads requests from stdin until
// EOF and answers each on stdout.
func runReplayCommand(args []string) (bool, int) {
	if len(args) == 0 || args[0] != "replay" {
		return false, 0
	}
	if len(args) > 1 {
		fmt.Fprintln(os.Stderr, "usage: helix replay   (NDJSON requests on stdin, responses on stdout)")
		return true, 2
	}
	// Stdout is the protocol. Anything Helix would normally print goes to
	// stderr instead, so a stray status line can never corrupt a response.
	proto := os.Stdout
	os.Stdout = os.Stderr
	// Nothing runs during a replay, but nothing must ever wait on a pager
	// with no one to answer it either.
	daemon.DisablePagers()

	planner, err := newReplayAgent()
	if err != nil {
		fmt.Fprintln(os.Stderr, "helix replay:", err)
		return true, 1
	}
	if err := serveReplays(os.Stdin, proto, planner); err != nil {
		fmt.Fprintln(os.Stderr, "helix replay:", err)
		return true, 1
	}
	return true, 0
}

// newReplayAgent is a headless agent with the user's provider and nothing
// else: no session, no task list, no hooks, no Metabolism recording, no RAG.
func newReplayAgent() (*agent.Agent, error) {
	c, err := config.DefaultConfig()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := ai.InitProviders(ai.ProviderSettings{
		Provider: c.Provider, Model: c.ProviderModel,
		CustomBaseURL: c.CustomProviderBaseURL, LlamaCppBaseURL: c.LLM.LlamaCppURL,
	}); err != nil {
		return nil, fmt.Errorf("ai providers: %w", err)
	}
	if c.Provider != "" {
		_ = ai.UseProvider(c.Provider)
		if c.ProviderModel != "" {
			ai.UseModel(c.ProviderModel)
		}
	}
	commands.SetPrompter(replayPrompter{})
	ag := agent.NewAgentWithRenderer(shell.DetectEnvironment(), nil, commands.NewDirectorySandbox(),
		commands.DefaultExecuteConfig(), false, nil, nil, nil, agent.HeadlessRenderer{})
	return ag, nil
}

// replayPlanner is the slice of the agent serveReplays needs.
type replayPlanner interface {
	ReplayRounds(request string, lessons []agent.LearnedLesson, maxRounds int) (*agent.ReplayResult, error)
}

// serveReplays answers each request line. A bad line gets an error response
// and the stream continues: one malformed request must not cost the rest.
func serveReplays(in io.Reader, out io.Writer, p replayPlanner) error {
	enc := json.NewEncoder(out)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if err := enc.Encode(replayOne(line, p)); err != nil {
			return err
		}
	}
	return sc.Err()
}

func replayOne(line string, p replayPlanner) replayResponse {
	var req replayRequest
	dec := json.NewDecoder(strings.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return replayResponse{V: ReplayWireVersion, Error: "malformed request: " + err.Error()}
	}
	resp := replayResponse{V: req.V, ID: req.ID}
	if req.V < 1 || req.V > ReplayWireVersion {
		resp.V = ReplayWireVersion
		resp.Error = fmt.Sprintf("replay wire version %d, this Helix speaks 1 to %d", req.V, ReplayWireVersion)
		return resp
	}
	if req.V == 1 && req.Rounds != 0 {
		resp.Error = "rounds needs replay wire version 2"
		return resp
	}
	rounds := min(max(req.Rounds, 1), maxReplayRounds)
	before := agent.MeterUsage()
	res, err := p.ReplayRounds(req.Request, req.Lessons, rounds)
	after := agent.MeterUsage()
	resp.Usage = replayUsage{
		ModelCalls:  max(after.Calls-before.Calls, 0),
		InputChars:  int(max(after.PromptChars-before.PromptChars, 0)),
		OutputChars: int(max(after.ResponseChars-before.ResponseChars, 0)),
	}
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	for _, st := range res.Steps {
		step := replayStep{Tool: st.Step.Tool, Action: st.Step.Action, Subject: agent.StepSubject(st.Step)}
		if req.V >= 2 {
			step.Round = st.Round
			switch {
			case st.Ran && st.OK:
				step.Outcome = "ok"
			case st.Ran:
				step.Outcome, step.Err = "failed", st.Err
			}
		}
		resp.Steps = append(resp.Steps, step)
	}
	resp.Reply = res.Reply
	if req.V >= 2 {
		resp.Rounds, resp.End = res.Rounds, res.End
	}
	resp.OK = true
	return resp
}
