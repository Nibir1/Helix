// cmd/helix/metabolism_cmd.go
// Purpose: /metabolism — opt-in, local-only recording of planner turns for the
// Metabolism engine's baseline (internal/metabolism, docs/harness.md §10).
package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"

	"helix/internal/agent"
	"helix/internal/commands"
	"helix/internal/metabolism"
	"helix/internal/shell"
)

// metabolismRec is the session's recorder. Nil only if it could not be set up,
// and every use is nil-safe.
var metabolismRec *metabolism.Recorder

// initMetabolism wires the recorder into the agent. Recording starts only if
// the user turned it on; a session that records says so at startup, the same
// way a non-default approval posture does.
func initMetabolism() {
	rec, err := metabolism.Open(metabolism.Options{
		Enabled:  cfg.UserPrefs.MetabolismRecord,
		Usage:    agent.MeterUsage,
		Declines: commands.DeclinedConfirmations,
	})
	if err != nil {
		uiWarn("metabolism", "recording unavailable: "+err.Error())
		return
	}
	metabolismRec = rec
	if agentCore != nil {
		agentCore.Metabolism = rec
		agentCore.DeliverLessons = cfg.UserPrefs.MetabolismLessons
	}
	if rec.Enabled() {
		uiOK("metabolism", "recording planner turns locally · /metabolism off stops it")
	}
	if cfg.UserPrefs.MetabolismLessons {
		if rec.Enabled() {
			uiOK("lessons", "delivering tested lessons into planner turns · /lessons off stops it")
		} else {
			uiIdle("lessons", "on, but paused: lessons are delivered only while /metabolism records")
		}
	}
}

// handleMetabolismCommand implements /metabolism [on|off|status].
func handleMetabolismCommand(c cmdArgs) {
	if metabolismRec == nil {
		uiFail("metabolism", "recording is not available in this session")
		return
	}
	switch c.Lower() {
	case "on", "enable":
		if err := setMetabolismRecording(true); err != nil {
			uiFail("metabolism", err.Error())
			return
		}
		uiOK("metabolism", "on — recording planner turns to "+metabolismRec.Path())
		uiDetail("Local only: nothing is sent anywhere. Recording never changes what Helix does.")
	case "off", "disable":
		_ = setMetabolismRecording(false)
		uiIdle("metabolism", "off — what was recorded is kept")
	case "", "status":
		printMetabolismStatus()
	default:
		uiUsage("/metabolism [on|off|status]")
	}
}

func setMetabolismRecording(on bool) error {
	if err := metabolismRec.SetEnabled(on); err != nil {
		return err
	}
	cfg.UserPrefs.MetabolismRecord = on
	return cfg.SavePreferences()
}

func printMetabolismStatus() {
	uiToggle("METABOLISM", metabolismRec.Enabled(),
		"recording planner turns, locally", "nothing is recorded", "/metabolism <on|off>")
	path := metabolismRec.Path()
	size, lines := logFootprint(path)
	rows := []uiRow{
		{Label: "FILE", Value: shell.Value(path)},
		{Label: "RECORDS", Value: shell.Value(strconv.Itoa(lines)) + shell.Muted("  in the active file")},
		{Label: "SIZE", Value: shell.Value(humanBytes(uint64(size)))},
		{Label: "INGEST", Value: shell.Muted("metabolism ingest -from " + path)},
	}
	w := shell.KVWidth("FILE", "RECORDS", "SIZE", "INGEST")
	for _, r := range rows {
		fmt.Println(shell.KV(r.Label, r.Value, w))
	}
}

// logFootprint reports the active file's size and line count; zero for a file
// that does not exist yet, which is the normal state before the first turn.
func logFootprint(path string) (int64, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return 0, 0
	}
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		n++
	}
	return info.Size(), n
}
