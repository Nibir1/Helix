// cmd/helix/wake.go
// Purpose: /blackbox wake on|off|status — the UI for true hands-free conversation.
// Enabling turns on wake-word listening (between turns in the interactive
// shell; continuously in `helix daemon`), applying safe defaults the first
// time. On by default since 2026-09-09; typed-only to enable, instant to
// disable, and an explicit `false` in config is never overridden.
package main

import (
	"fmt"

	"helix/internal/config"
	"helix/internal/shell"
	"helix/internal/speech"
)

// handleWakeCommand implements /blackbox wake <on|off|status>.
//
// ONE switch, by owner decision. There were two — `wake on` for the gaps
// between spoken turns and `wake always on` for the keyboard prompt — and the
// split confused a real user twice in one session: they enabled the first and
// asked "now how do I wake it up?", because nothing was listening and no banner
// said so. "Listen for me" is one intention, so it is one command. The narrow
// behaviour survives as `speech.wake_word.always_listen: false` in config,
// which is the right home for a preference that needs no verb.
//
// The switch is on Sub() — the FIRST argument — not Lower(), which is the whole
// argument string. That was a live bug for as long as a nested subcommand
// existed: `/blackbox wake always on` produced Rest = "always on", matched no
// case, and printed the usage line while changing nothing. Every one-word form
// worked, which is exactly why it survived a test suite and had to be found by
// a user.
func handleWakeCommand(c cmdArgs) {
	switch c.Sub() {
	case "on", "enable":
		enableWakeWord()
	case "off", "disable":
		disableWakeWord()
	case "", "status":
		printWakeStatus()
	default:
		uiUsage("/blackbox wake <on|off|status>")
	}
}

// disableWakeWord closes both halves: no wake between turns, and no microphone
// at the prompt.
//
// Both, because one command turned them on. Leaving the prompt armed after
// "wake off" would be a microphone the user believes they have just closed.
func disableWakeWord() {
	cfg.Speech.WakeWord.Enabled = config.BoolPtr(false)
	cfg.Speech.WakeWord.AlwaysListen = config.BoolPtr(false)
	_ = cfg.SavePreferences()
	uiIdle("wake word", "off — the microphone is closed, at the prompt and between turns")
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
	ww.Enabled = config.BoolPtr(true)
	// The prompt is armed too. See handleWakeCommand for why this is not a
	// second switch: someone who says "listen for me" does not mean "listen
	// for me only while I am already talking to you".
	if shell.KeyWaitSupported() {
		ww.AlwaysListen = config.BoolPtr(true)
	}
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
	// The FIRST line now answers the question this banner used to leave hanging.
	//
	// It said "hands-free is live in THIS shell: after each turn I listen before
	// the next one", which presupposes there are turns — and a user who had just
	// run this command was at the keyboard with nothing listening. They asked
	// "now how do I wake it up?", which is the banner's fault, not theirs. It
	// says what is listening RIGHT NOW and how to use it.
	armed := shell.KeyWaitSupported()
	if engine == "sidecar" {
		if armed {
			lines = append(lines,
				fmt.Sprintf("Listening now, at this prompt: say %q and I go live. Keep typing and nothing changes.", phrase))
		} else {
			lines = append(lines,
				fmt.Sprintf("Hands-free is live in THIS shell: after each turn I listen for %q before the next one.", phrase))
		}
	} else {
		if armed {
			lines = append(lines,
				"Listening now, at this prompt: make any sound and I go live. Keep typing and nothing changes.")
		} else {
			lines = append(lines,
				"Hands-free is live in THIS shell: after each turn I listen before the next one.")
		}
		lines = append(lines,
			fmt.Sprintf("Engine %q wakes on ANY speech or loud sound — say anything to continue; it cannot", engineOrDefault(engine)),
			fmt.Sprintf("match the phrase %q. For true phrase spotting, run an openWakeWord-class", phrase),
			"sidecar and set speech.wake_word.engine=sidecar (see docs/edge_deployment.md §5.1).")
	}
	if shell.KeyWaitSupported() {
		lines = append(lines,
			"Once live, it never ends itself: say \"manual mode\" or type /blackbox off to come back.")
	}
	return append(lines,
		"The wake word gates turns AFTER this one — a voice turn already in progress needs no wake.",
		// "run: helix daemon" without saying WHERE sent a user to type it at
		// this prompt, where it is not a slash command: it reached the planner,
		// which investigated with `ps aux | grep helix` instead of starting
		// anything. The same words from their shell worked first try. A hint
		// that names a command has to name the place, or it is a trap the
		// hint itself set.
		"For always-on conversation (no terminal open), exit Helix and run  helix daemon",
		"from your shell — it is a CLI entry point, not a command inside this prompt.",
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

	w := shell.KVWidth("STATE", "DETECTOR", "PHRASE", "RECORDER", "AT THE PROMPT")
	fmt.Println(shell.PanelTitle("wake word"))

	// STATE reports the between-turns half and AT THE PROMPT reports the other;
	// said as a pair rather than as one summary, because "listening between
	// turns" on its own is what the owner read as "nothing is happening".
	if ww.Listening() {
		fmt.Println(shell.KV("STATE", shell.Badge(shell.StateGood, "on")+
			shell.Muted("  between spoken turns  ·  the prompt is the row below"), w))
	} else {
		// OFF can only be an explicit `enabled: false`, since an absent key
		// reads as the default (on) — so name the file rather than leave the
		// user to wonder why an on-by-default feature is off. Older builds
		// wrote that false themselves, which is the single most likely reason
		// a reader is looking at this row.
		fmt.Println(shell.KV("STATE", shell.Badge(shell.StateIdle, "off")+
			shell.Muted("  your config sets enabled: false — listening is on by "+
				"default, so this is being honoured, not defaulted"), w))
		fmt.Println(shell.KV("", shell.Muted("/blackbox wake on turns it on and persists"), w))
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
	// Where the wake word is heard, which is a different question from whether
	// it is on: between spoken turns only, or also at an idle keyboard prompt.
	fmt.Println(shell.KV("AT THE PROMPT", alwaysListenStatusLine(), w))
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
