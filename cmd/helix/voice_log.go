// cmd/helix/voice_log.go
//
// Purpose: BlackBox P2.8 — the opt-in voice interaction log's user surface:
// the runtime toggle, the reader, and the status line.
//
// This was the last carry-over from Phase 2, deliberately deferred until the
// Phase 4 journal existed so the two would share one set of permissions,
// rotation and redaction rules rather than growing two (internal/journal).
//
// The feature is OFF by default and that is load-bearing, not timid: with it
// off there is no directory and no file, so a user who never asks for a
// transcript store never acquires one (threat V5). Because of that, the toggle
// has to be discoverable — a privacy-relevant capability nobody can find is
// only half-shipped — so it lives where every other voice control now lives,
// as a /blackbox subcommand.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"helix/internal/journal"
	"helix/internal/shell"

	"github.com/fatih/color"
)

// voiceLog is nil whenever the feature is disabled, which is the normal case.
// Every method on *journal.VoiceLog tolerates a nil receiver, so call sites
// record unconditionally and never guard.
var voiceLog *journal.VoiceLog

// initVoiceLog opens the log if the user has enabled it. Called once from main
// after preferences load.
func initVoiceLog() {
	dir, err := journal.DefaultVoiceLogDir()
	if err != nil {
		return
	}
	vl, err := journal.OpenVoiceLog(dir, cfg.VoiceLog.Enabled, journal.Options{
		MaxBytes:  cfg.VoiceLog.MaxBytes,
		KeepFiles: cfg.VoiceLog.KeepFiles,
	})
	if err != nil {
		color.Yellow("Voice log disabled: %v", err)
		return
	}
	voiceLog = vl
}

// logHeard records one finalized transcript and what the pipeline did with it.
func logHeard(text, provider string, confidence float64, outcome string) {
	voiceLog.Heard(text, provider, confidence, outcome)
}

// logSpoke records text that was handed to the TTS chain.
func logSpoke(text string) {
	voiceLog.Spoke(text)
}

// handleVoiceLogCommand implements /blackbox log <on|off|status|show [n]>.
func handleVoiceLogCommand(c cmdArgs) {
	switch c.Sub() {
	case "on", "enable":
		setVoiceLogEnabled(true)
	case "off", "disable":
		setVoiceLogEnabled(false)
	case "show", "tail", "read":
		showVoiceLog(c.From(1))
	case "", "status":
		fmt.Println(shell.PanelTitle("voice log"))
		fmt.Println(shell.PanelLine(voiceLogStatusLine()))
		for _, l := range shell.PanelWrap(
			"records what Helix heard and said, as text — never audio. /purge wipes it.",
			shell.Muted) {
			fmt.Println(l)
		}
		fmt.Println(shell.PanelEnd())
	default:
		color.Yellow("Usage: /blackbox log <on|off|status|show [n]>")
	}
}

// setVoiceLogEnabled flips the preference, persists it, and reopens the log.
//
// Turning it OFF deliberately leaves existing entries on disk: silently
// deleting a user's audit trail because they stopped adding to it would be a
// surprise in the destructive direction. /purge is the documented eraser, and
// the message says so.
func setVoiceLogEnabled(on bool) {
	cfg.VoiceLog.Enabled = on
	if err := cfg.SavePreferences(); err != nil {
		color.Red("Could not save the voice-log setting: %v", err)
		return
	}
	initVoiceLog()

	if !on {
		fmt.Println(shell.Badge(shell.StateIdle, "voice log off") +
			shell.Muted("  nothing further is recorded  ·  /purge wipes what exists"))
		return
	}
	if !voiceLog.Enabled() {
		color.Yellow("Voice log could not be opened — nothing is being recorded.")
		return
	}
	fmt.Println(shell.Badge(shell.StateGood, "voice log on") +
		shell.Muted("  "+voiceLog.Path()))
	fmt.Println(shell.Hint("transcripts and replies are stored as text; audio never is"))
}

// voiceLogStatusLine is the one-liner for /blackbox status.
func voiceLogStatusLine() string {
	if !voiceLog.Enabled() {
		return shell.Badge(shell.StateIdle, "off") +
			shell.Muted("  no transcripts stored  ·  /blackbox log on")
	}
	detail := voiceLog.Path()
	if fi, err := os.Stat(detail); err == nil {
		detail += fmt.Sprintf("  ·  %.1f KB", float64(fi.Size())/1024)
	}
	return shell.Badge(shell.StateGood, "recording") + shell.Muted("  "+detail)
}

// showVoiceLog prints the last n entries (default 20).
func showVoiceLog(arg string) {
	if !voiceLog.Enabled() {
		fmt.Println(shell.Hint("voice log is off — /blackbox log on starts recording"))
		return
	}
	n := 20
	if v, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil && v > 0 {
		n = v
	}
	entries := voiceLog.Tail(n)
	if len(entries) == 0 {
		fmt.Println(shell.Hint("voice log is empty — nothing has been heard or said yet"))
		return
	}

	fmt.Println(shell.PanelTitle("voice log"))
	for _, e := range entries {
		stamp := shell.Muted(e.TS.Format("15:04:05"))
		switch e.Dir {
		case journal.DirHeard:
			// The user's own words get the same magenta ❯ the live transcript
			// echo uses, so the log reads like the session it records.
			line := stamp + " " + shell.Fg(shell.HexSecondary, "❯ ") + shell.Fg(shell.HexText, e.Text)
			meta := e.Provider
			if e.Confidence > 0 {
				meta += fmt.Sprintf(" %.2f", e.Confidence)
			}
			if e.Outcome != "" {
				meta = strings.TrimSpace(meta + " → " + e.Outcome)
			}
			fmt.Println(shell.PanelLine(line + shell.Muted("   "+meta)))
		case journal.DirSpoke:
			fmt.Println(shell.PanelLine(stamp + " " +
				shell.Fg(shell.HexPrimary, "◂ ") + shell.Value(e.Text)))
		default:
			fmt.Println(shell.PanelLine(stamp + " " + shell.Muted("· "+e.Note)))
		}
	}
	fmt.Println(shell.PanelEnd())
}
