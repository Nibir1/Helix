// cmd/helix/wake.go
// Purpose: /wake on|off|status — the UI for true hands-free conversation.
// Enabling turns on wake-word listening (between turns in the interactive
// shell; continuously in `helix daemon`), applying safe defaults the first
// time. Privacy stays opt-in: off by default, instant to disable.
package main

import (
	"fmt"
	"strings"

	"helix/internal/config"
	"helix/internal/speech"

	"github.com/fatih/color"
)

// handleWakeCommand implements /wake <on|off|status>.
func handleWakeCommand(raw string) {
	arg := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, "/wake")))
	switch arg {
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
		color.Red("No audio recorder found — install sox (`brew install sox`) before hands-free will work.")
		return
	}
	color.Cyan("Hands-free is live in THIS shell: after each turn I listen for \"%s\" before the next one.", ww.Phrase)
	color.Cyan("For always-on conversation (no terminal open), run:  helix daemon")
	color.Cyan("Say \"go to sleep\" or \"stop listening\" anytime to pause; /wake off to disable.")
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
			color.Red("Recorder: missing — install sox (`brew install sox`).")
			return
		}
		color.Green("Recorder: ok — hands-free is ready.")
		if ww.Engine == "sidecar" {
			color.Cyan("Detector: sidecar (%s)", ww.SidecarURL)
		} else {
			color.Cyan("Detector: energy onset (everywhere-works default)")
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
