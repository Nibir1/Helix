// cmd/helix/wake.go
// Purpose: /blackbox wake on|off|status — the UI for true hands-free conversation.
// Enabling turns on wake-word listening (between turns in the interactive
// shell; continuously in `helix daemon`), applying safe defaults the first
// time. Privacy stays opt-in: off by default, instant to disable.
package main

import (
	"fmt"

	"helix/internal/config"
	"helix/internal/shell"
	"helix/internal/speech"
)

// handleWakeCommand implements /blackbox wake <on|off|status>.
func handleWakeCommand(c cmdArgs) {
	switch c.Lower() {
	case "on", "enable":
		enableWakeWord()
	case "off", "disable":
		cfg.Speech.WakeWord.Enabled = false
		_ = cfg.SavePreferences()
		uiIdle("wake word", "off — hands-free listening is disabled")
	case "", "status":
		printWakeStatus()
	default:
		uiUsage("/blackbox wake <on|off|status>")
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

	uiOK("wake word", fmt.Sprintf("%q  ·  %s  ·  %s", ww.Phrase, ww.Engine, ww.SensitivityPreset))
	if _, err := speech.DetectRecorder(); err != nil {
		uiFail("no recorder", "hands-free needs one")
		uiUsage("/setup installs sox")
		return
	}
	for _, line := range wakeBannerLines(ww.Engine, ww.Phrase) {
		uiDetail(line)
	}
}

// wakeBannerLines is the /blackbox wake on explanation, worded for the engine that will
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
		"Say \"go to sleep\" or \"stop listening\" anytime to pause; /blackbox wake off to disable.")
}

// engineOrDefault names the engine that will run when config leaves it blank.
func engineOrDefault(engine string) string {
	if engine == "" {
		return "energy"
	}
	return engine
}

// printWakeStatus summarizes the hands-free configuration and readiness.
//
// The phrase is reported differently per engine on purpose. The default
// `energy` detector scores normalized RMS and cannot match words at all, so
// stating "phrase: hey helix" beside it — which this panel used to do
// unconditionally — is the same false promise the enable banner was corrected
// for in wakeBannerLines. A stored-but-unused phrase is said to be exactly that.
func printWakeStatus() {
	ww := cfg.Speech.WakeWord
	engine := engineOrDefault(ww.Engine)
	phrase := orDefault(ww.Phrase, "hey helix")

	w := shell.KVWidth("STATE", "DETECTOR", "PHRASE", "RECORDER")
	fmt.Println(shell.PanelTitle("wake word"))

	if ww.Enabled {
		fmt.Println(shell.KV("STATE", shell.Badge(shell.StateGood, "on")+
			shell.Muted("  listening between turns"), w))
	} else {
		fmt.Println(shell.KV("STATE", shell.Badge(shell.StateIdle, "off")+
			shell.Muted("  /blackbox wake on enables hands-free conversation"), w))
	}

	if engine == "sidecar" {
		fmt.Println(shell.KV("DETECTOR", shell.Value("sidecar")+
			shell.Muted("  "+orDefault(ww.SidecarURL, "no URL configured")), w))
		fmt.Println(shell.KV("PHRASE", shell.Value(fmt.Sprintf("%q", phrase))+
			shell.Muted("  ·  sensitivity "+orDefault(ww.SensitivityPreset, "balanced")), w))
	} else {
		fmt.Println(shell.KV("DETECTOR", shell.Value("energy onset")+
			shell.Muted("  the everywhere-works default  ·  wakes on ANY speech or loud sound"), w))
		fmt.Println(shell.KV("PHRASE", shell.Muted(fmt.Sprintf(
			"%q is stored but unused — this engine cannot match words", phrase)), w))
	}

	if _, err := speech.DetectRecorder(); err != nil {
		fmt.Println(shell.KV("RECORDER", shell.Badge(shell.StateBad, "missing")+
			shell.Muted("  /setup installs sox — hands-free cannot work without it"), w))
	} else {
		fmt.Println(shell.KV("RECORDER", shell.Badge(shell.StateGood, "ready"), w))
	}
	fmt.Println(shell.PanelEnd())

	if engine != "sidecar" {
		fmt.Println(shell.Hint("for true phrase spotting run an openWakeWord-class sidecar · " +
			"docs/edge_deployment.md §5.1"))
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
