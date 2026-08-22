// cmd/helix/wake.go
// Purpose: /wake on|off|status — the UI for true hands-free conversation.
// Enabling turns on wake-word listening (between turns in the interactive
// shell; continuously in `helix daemon`), applying safe defaults the first
// time. Privacy stays opt-in: off by default, instant to disable.
package main

import (
	"fmt"

	"helix/internal/config"
	"helix/internal/speech"

	"github.com/fatih/color"
)

// handleWakeCommand implements /wake <on|off|status>.
func handleWakeCommand(c cmdArgs) {
	switch c.Lower() {
	case "on", "enable":
		enableWakeWord()
	case "off", "disable":
		cfg.Speech.WakeWord.Enabled = false
		_ = cfg.SavePreferences()
		color.Yellow("Wake word DISABLED — hands-free listening is off.")
	case "", "status":
		printWakeStatus()
	default:
		color.Yellow("Usage: /wake <on|off|status>")
	}
}

// enableWakeWord applies defaults on first enable (phrase/engine/sensitivity),
// persists, and tells the user how to go truly hands-free.
func enableWakeWord() {
	def := config.WakeWordDefaults()
	ww := &cfg.Speech.WakeWord
	if ww.Engine == "" {
		ww.Engine = def.Engine
	}
	if ww.Phrase == "" {
		ww.Phrase = def.Phrase
	}
	if ww.SensitivityPreset == "" {
		ww.SensitivityPreset = def.SensitivityPreset
	}
	if ww.CooldownS <= 0 {
		ww.CooldownS = def.CooldownS
	}
	if ww.ChunkMs <= 0 {
		ww.ChunkMs = def.ChunkMs
	}
	ww.Enabled = true
	_ = cfg.SavePreferences()

	color.Green("Wake word ENABLED (\"%s\", engine: %s, preset: %s).", ww.Phrase, ww.Engine, ww.SensitivityPreset)
	if _, err := speech.DetectRecorder(); err != nil {
		color.Red("No audio recorder found — run /setup to install sox before hands-free will work.")
		return
	}
	for _, line := range wakeBannerLines(ww.Engine, ww.Phrase) {
		color.Cyan("%s", line)
	}
}

// wakeBannerLines is the /wake on explanation, worded for the engine that will
// actually do the detecting.
//
// The banner used to promise `after each turn I listen for "hey helix"`
// unconditionally. With the default `energy` engine that is false: it scores
// normalized RMS over a chunk (internal/wakeword/energy.go), so ANY speech or
// loud sound wakes it — it cannot recognize a phrase, and never claimed to
// internally. printWakeStatus already differentiated the two detectors; this
// makes the enable banner agree with it.
//
// Args:
//   - engine: the configured engine ("energy" — the default — or "sidecar").
//   - phrase: the configured wake phrase.
//
// Returns: the lines to print, in order.
// Complexity: O(1).
func wakeBannerLines(engine, phrase string) []string {
	if phrase == "" {
		phrase = "hey helix"
	}

	var lines []string
	if engine == "sidecar" {
		lines = append(lines,
			fmt.Sprintf("Hands-free is live in THIS shell: after each turn I listen for %q before the next one.", phrase))
	} else {
		lines = append(lines,
			"Hands-free is live in THIS shell: after each turn I listen before the next one.",
			fmt.Sprintf("Engine %q wakes on ANY speech or loud sound — say anything to continue; it cannot", engineOrDefault(engine)),
			fmt.Sprintf("match the phrase %q. For true phrase spotting, run an openWakeWord-class", phrase),
			"sidecar and set speech.wake_word.engine=sidecar (see docs/edge_deployment.md §5.1).")
	}
	return append(lines,
		"The wake word gates turns AFTER this one — a voice turn already in progress needs no wake.",
		"For always-on conversation (no terminal open), run:  helix daemon",
		"Say \"go to sleep\" or \"stop listening\" anytime to pause; /wake off to disable.")
}

// engineOrDefault names the engine that will run when config leaves it blank.
func engineOrDefault(engine string) string {
	if engine == "" {
		return "energy"
	}
	return engine
}

// printWakeStatus summarizes the hands-free configuration and readiness.
func printWakeStatus() {
	ww := cfg.Speech.WakeWord
	state := "off"
	if ww.Enabled {
		state = "on"
	}
	engine := ww.Engine
	if engine == "" {
		engine = "energy"
	}
	phrase := ww.Phrase
	if phrase == "" {
		phrase = "hey helix"
	}
	fmt.Printf("Wake word: %s (phrase %q, engine %s, preset %s)\n",
		state, phrase, engine, orDefault(ww.SensitivityPreset, "balanced"))

	if ww.Enabled {
		if _, err := speech.DetectRecorder(); err != nil {
			color.Red("Recorder: missing — run /setup to install sox.")
			return
		}
		color.Green("Recorder: ok — hands-free is ready.")
		if ww.Engine == "sidecar" {
			color.Cyan("Detector: sidecar (%s) — matches the phrase", ww.SidecarURL)
		} else {
			color.Cyan("Detector: energy onset (everywhere-works default) — wakes on ANY speech,")
			color.Cyan("          not on the phrase; the phrase is only used by the sidecar engine.")
		}
	} else {
		color.Cyan("Run /wake on to enable hands-free conversation.")
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
