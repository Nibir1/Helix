// cmd/helix/blackbox.go
//
// Purpose: /blackbox — the single entry point to Helix's living mode.
//
// Eight commands (/voice, /manual, /voice-setup, /voice-status, /wake, /say,
// /tts, /eyes) were one capability split across eight names. That split was
// visible in the worst possible place: turning Helix on meant remembering
// which subsystem lived behind which verb, and the two halves of perception —
// ears and eyes — could be in contradictory states with nothing reporting it.
// /blackbox on turns the whole thing on: microphone, camera, speech, and the
// companion loop that lets Helix speak without being asked.
//
// Three properties are load-bearing and must survive any edit here:
//
//  1. Degrade, never refuse the whole mode. A missing camera or a text-only
//     model costs the camera, not the conversation. The only hard precondition
//     is a working recorder + STT chain, because without those "voice mode" is
//     a lie.
//  2. The way out is always available. /blackbox off returns to the keyboard,
//     and the spoken phrases ("manual mode", "switch to manual mode") still do
//     the same from the microphone — that is the safety valve /manual used to
//     be, and removing the command must not remove the valve.
//  3. Status may not overstate. Reporting readiness the machine cannot deliver
//     is the specific bug this file's readiness helpers exist to prevent: the
//     camera gate used to check only whether a MODEL could see, never whether a
//     frame could be captured, so /eyes status printed "ready" on a host with
//     no ffmpeg and the first capture failed.
package main

import (
	"fmt"
	"strings"

	"helix/internal/shell"
	"helix/internal/speech"

	"github.com/fatih/color"
)

// blackBoxUsage is the one place the subcommand vocabulary is written down.
var blackBoxUsage = []string{
	"/blackbox on               microphone, camera, speech, companion",
	"/blackbox off              back to the keyboard (or say \"manual mode\")",
	"/blackbox status           hearing, sight, wake, context, transcript",
	"/blackbox setup            choose STT/TTS providers, with live pricing",
	"",
	"/blackbox look [question]  capture a frame and answer a question on it",
	"/blackbox eyes on|off      camera only, without changing the mode",
	"/blackbox wake on|off      hands-free waking between turns",
	"/blackbox tts on|off       whether ordinary replies are spoken aloud",
	"/blackbox say <text>       speak text through the TTS chain",
	"/blackbox log on|off       keep a local text record of what was said",
	"/blackbox stats            measured latencies and wake rate",
}

// handleBlackBoxCommand implements /blackbox and its subcommands.
func handleBlackBoxCommand(c cmdArgs) {
	switch c.Sub() {
	case "", "on", "start", "live":
		blackBoxOn()
	case "off", "stop", "manual", "exit":
		blackBoxOff()
	case "status":
		blackBoxStatus()
	case "setup":
		handleVoiceSetup()

	// --- perception ---
	case "look", "see", "describe":
		describeWhatIsSeen(c.From(1))
	case "eyes":
		blackBoxEyes(c.Shift())

	// --- the folded toggles, unchanged in behavior ---
	case "wake":
		handleWakeCommand(c.Shift())
	case "tts":
		handleTTSCommand(c.Shift())
	case "say":
		handleSayCommand(c.Shift())
	case "mictest":
		handleMicTest()
	case "log", "logs", "transcript":
		handleVoiceLogCommand(c.Shift())
	case "stats", "metrics":
		handleVoiceStatsCommand()

	default:
		color.Yellow("Unknown: /blackbox %s", c.Sub())
		for _, line := range blackBoxUsage {
			color.Cyan("%s", line)
		}
	}
}

// blackBoxOn enters live mode: ears, eyes, voice, and initiative together.
//
// The ordering matters. The microphone is the hard requirement and is checked
// first, so a host that cannot listen is told that before anything else has
// been switched on. The camera is best-effort and reports its own reason for
// staying dark — a live mode that silently has no eyes is worse than one that
// says why.
func blackBoxOn() {
	if voiceModeActive {
		fmt.Println("Already live. /blackbox status shows what is on.")
		return
	}
	if err := voiceEntryPreflight(); err != nil {
		color.Red("Cannot go live: %v", err)
		return
	}

	// Eyes follow the mode. This inverts the old opt-in on purpose — live mode
	// is a camera consent moment by definition — and the frame invariants are
	// unchanged: one frame at a time, held in memory, never written to disk.
	//
	// Decided BEFORE the banner, because the banner reports it. Enabling the
	// camera afterwards printed "SIGHT • off" and then "Eyes ENABLED" two lines
	// later, which is the banner lying about the state it exists to show.
	eyesWhy := ""
	if ready, why := visionReady(); ready {
		cfg.Vision.Enabled = true
		_ = cfg.SavePreferences()
		journalVisionEvent("enabled", "", 0)
	} else {
		eyesWhy = why
	}

	enterVoiceMode(true)
	if eyesWhy != "" {
		fmt.Println(shell.Hint("camera stays off: " + eyesWhy))
	}

	startCompanion()
}

// blackBoxOff returns to the keyboard and closes both sensors.
//
// Leaving the camera on after leaving the mode would be exactly the kind of
// privacy surprise the opt-in exists to prevent, so one command closes what one
// command opened.
func blackBoxOff() {
	if !voiceModeActive && !cfg.Vision.Enabled {
		fmt.Println("Already in keyboard mode.")
		return
	}
	stopCompanion()
	if cfg.Vision.Enabled {
		setVisionEnabled(false)
	}
	if voiceModeActive {
		exitVoiceMode(true)
	}
}

// blackBoxEyes toggles the camera WITHOUT touching the conversation mode, so
// the privacy kill switch stays a single, fast action while Helix keeps
// listening. This is what "turn off your eyes" routes to.
func blackBoxEyes(c cmdArgs) {
	switch c.Sub() {
	case "on", "enable":
		if ready, why := visionReady(); !ready {
			color.Yellow("Cannot open the camera: %s", why)
			for _, line := range visionUnavailableHelp() {
				color.Yellow("%s", line)
			}
			return
		}
		setVisionEnabled(true)
	case "off", "disable":
		setVisionEnabled(false)
	case "", "status":
		fmt.Println(blackBoxEyesLine())
	default:
		color.Yellow("Usage: /blackbox eyes <on|off|status>")
	}
}

// blackBoxStatus prints the merged report: one place that answers "what can
// Helix do right now", followed by the full speech-chain detail.
func blackBoxStatus() {
	mode := shell.Badge(shell.StateIdle, "standby") + shell.Muted("  keyboard input")
	if voiceModeActive {
		mode = shell.Badge(shell.StateGood, "LIVE") + shell.Muted("  listening")
	}

	w := shell.KVWidth("MODE", "HEARING", "SIGHT", "WAKE", "INITIATIVE", "CONTEXT",
		"INTERRUPT", "TRANSCRIPT")
	fmt.Println(shell.PanelTitle("blackbox"))
	fmt.Println(shell.KV("MODE", mode, w))
	fmt.Println(shell.KV("HEARING", blackBoxHearingLine(), w))
	fmt.Println(shell.KV("SIGHT", blackBoxEyesLine(), w))
	// The usage text has advertised wake since this command was created, and
	// wake was the one state it never printed —
	// it lived only behind /blackbox wake status. A summary that omits a state
	// it advertises is the same overstatement rule this file opens with.
	fmt.Println(shell.KV("WAKE", blackBoxWakeLine(), w))
	fmt.Println(shell.KV("INITIATIVE", companionStatusLine(), w))
	// Conversational context sits with the sensors rather than in the speech
	// chain below because it is the same question they answer: what is Helix
	// holding on to right now. It retains recent AUDIO in memory, so a user
	// reading this panel for privacy reasons needs it in the panel.
	fmt.Println(shell.KV("CONTEXT", blackBoxContextLine(speech.ConversationReport()), w))
	// Barge-in samples the microphone between sentences, so it belongs on the
	// panel that answers "what is listening right now" rather than being a
	// silent config flag.
	fmt.Println(shell.KV("INTERRUPT", blackBoxBargeInLine(), w))
	// Whether speech is being written to disk belongs in the same report as
	// whether the microphone and camera are open: it is the third thing a
	// privacy-conscious user wants to know, and it had no surface at all.
	fmt.Println(shell.KV("TRANSCRIPT", voiceLogStatusLine(), w))
	fmt.Println(shell.PanelEnd())

	// The deep chain report (STT/TTS providers, recorder, latencies) is the
	// same one /voice-status printed; it is detail under the summary now rather
	// than a second command nobody remembered to run.
	handleVoiceStatus()
}

// blackBoxHearingLine summarises the ear in one line: can Helix record, and
// what will transcribe it. The summary answers "will this work" so the chain
// tables below can answer "and how".
func blackBoxHearingLine() string {
	if _, err := speech.DetectRecorder(); err != nil {
		return shell.Badge(shell.StateBad, "no recorder") +
			shell.Muted("  /setup installs sox")
	}
	reg := speech.Default()
	if reg == nil || len(reg.STTChain()) == 0 {
		return shell.Badge(shell.StateWarn, "no transcription") +
			shell.Muted("  /blackbox setup picks one")
	}
	return shell.Badge(shell.StateGood, "ready") +
		shell.Muted("  ") + shell.Value(strings.Join(reg.STTChain(), " → "))
}

// blackBoxWakeLine summarises hands-free triggering in one line.
//
// It names what the configured engine actually does rather than the phrase,
// because the default energy detector cannot match words — the same correction
// wakeBannerLines and printWakeStatus carry. A phrase shown here would be the
// third place making a promise the detector does not keep.
func blackBoxWakeLine() string {
	ww := cfg.Speech.WakeWord
	if !ww.Enabled {
		return shell.Badge(shell.StateIdle, "off") +
			shell.Muted("  /blackbox wake on for hands-free turns")
	}
	if engineOrDefault(ww.Engine) == "sidecar" {
		return shell.Badge(shell.StateGood, "listening") + shell.Muted("  ") +
			shell.Value(orDefault(ww.Phrase, "hey helix")) +
			shell.Muted("  ·  sidecar")
	}
	return shell.Badge(shell.StateGood, "listening") +
		shell.Muted("  any speech or loud sound  ·  energy onset")
}

// blackBoxEyesLine is the camera's honest one-liner, used by both the merged
// status and the eyes subcommand.
func blackBoxEyesLine() string {
	ready, why := visionReady()
	switch {
	case cfg.Vision.Enabled && ready && cameraDeliveredNothing():
		// Enabled, ffmpeg present, model can see — and the camera has never
		// actually produced a frame. Saying "watching" here is the readiness lie
		// this file exists to prevent: on macOS an unauthorized camera passes
		// every check that can be made cheaply and delivers nothing forever.
		return shell.Badge(shell.StateBad, "no frames") +
			shell.Muted("  camera opens but delivers nothing — likely an OS "+
				"permission  ·  /blackbox look shows why")
	case cfg.Vision.Enabled && ready:
		return shell.Badge(shell.StateGood, "watching") + shell.Muted("  ") +
			shell.Value(visionRouteDescription()) +
			shell.Muted(fmt.Sprintf("  ·  %d frame/turn", visionMaxFrames()))
	case cfg.Vision.Enabled && !ready:
		// Enabled but unusable is the state the old status report could not
		// express, and the one most worth saying out loud.
		return shell.Badge(shell.StateBad, "on but blind") + shell.Muted("  "+why)
	case ready:
		return shell.Badge(shell.StateIdle, "off") +
			shell.Muted("  ready when you are  ·  ") + shell.Value(visionRouteDescription())
	default:
		return shell.Badge(shell.StateIdle, "off") + shell.Muted("  "+why)
	}
}

// blackBoxBargeInLine reports how a reply can be stopped.
//
// Ctrl+C is listed in both states because it is the one that always works and
// needs no microphone. Voice interruption is deliberately described by its
// limit — sentence boundaries — rather than as "barge-in", which promises full
// duplex that this does not have.
func blackBoxBargeInLine() string {
	if speech.BargeInEnabled() {
		return shell.Badge(shell.StateGood, "voice + Ctrl+C") +
			shell.Muted("  speak in the gap between sentences")
	}
	return shell.Badge(shell.StateIdle, "Ctrl+C") +
		shell.Muted("  /config barge-in on adds voice")
}

// blackBoxContextLine reports conversational context without overstating it.
//
// Takes the report rather than fetching it so every branch below is reachable
// from a test — which is how the wording below is held to one line each. KV now
// wraps rather than escaping the panel, so length is a readability question
// rather than a rendering bug, but a status panel whose every row runs to two
// lines is still worse than one whose rows do not.
//
// The states are separated because they mean genuinely different things, and
// the interesting ones are the unhappy middle:
//
//   - "not applied" is the state this whole mechanism exists to expose. An
//     unpatched csm.rs ACCEPTS a context field and silently discards it, so the
//     request succeeds and the voice is never conditioned. Rendering that as
//     "on" would claim prosody the user is not getting.
//   - "retained, unused" is the privacy-relevant inverse: audio is being held in
//     memory and no voice in the chain can consume it. A cost with no benefit,
//     invisible unless said.
//   - "ready" is honest ignorance. Before the first spoken reply nothing has
//     been sent, and an unconfirmed feature must not render as a working one.
func blackBoxContextLine(rep speech.ContextReport) string {
	switch {
	case !rep.Enabled && rep.Provider == "":
		return shell.Badge(shell.StateIdle, "off") +
			shell.Muted("  replies are synthesized one at a time")
	case !rep.Enabled:
		return shell.Badge(shell.StateIdle, "off") + shell.Muted("  ") +
			shell.Value(rep.Provider) +
			shell.Muted(" can use it  ·  /config context-turns 4")
	case rep.Provider == "":
		return shell.Badge(shell.StateWarn, "retained, unused") +
			shell.Muted("  no context-capable voice in the chain")
	}

	held := shell.Muted(fmt.Sprintf("  %d turn%s  ·  %s",
		rep.Turns, plural(rep.Turns), compactBytes(int64(rep.Bytes))))

	switch {
	case rep.Rejected:
		return shell.Badge(shell.StateBad, "refused") + shell.Muted("  ") +
			shell.Value(rep.Provider) +
			shell.Muted(" rejected it — plain synthesis for the rest of this session")
	case rep.Ignored:
		return shell.Badge(shell.StateWarn, "not applied") + shell.Muted("  ") +
			shell.Value(rep.Provider) +
			shell.Muted(" accepted it silently  ·  needs docs/csm-context.patch")
	case rep.Honored:
		return shell.Badge(shell.StateGood, "conditioning") + shell.Muted("  ") +
			shell.Value(rep.Provider) + held
	default:
		return shell.Badge(shell.StateIdle, "ready") + shell.Muted("  ") +
			shell.Value(rep.Provider) + held + shell.Muted("  ·  not yet sent")
	}
}

// compactBytes renders a size in the shortest honest unit.
//
// A KB-only format spends six digits saying "4096.0 KB" for the ordinary full
// audio buffer — wider than the column and harder to read than the number it
// stands for. GB matters for the other caller: /purge weighs model-weight
// directories, where the whole point of the number is deciding whether the disk
// space is worth reclaiming, and "1433.6 MB" is a worse answer than "1.4 GB".
//
// One formatter rather than two, on int64 so a multi-gigabyte directory cannot
// overflow it on a 32-bit board.
func compactBytes(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1f GB", float64(n)/gb)
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/mb)
	default:
		return fmt.Sprintf("%.1f KB", float64(n)/kb)
	}
}

// cameraDeliveredNothing reports whether every capture attempt so far has
// failed.
//
// "Never attempted" is deliberately NOT a failure: a fresh session has not tried
// yet and must not be accused of being broken. Only a recorded failure with no
// success behind it counts.
func cameraDeliveredNothing() bool {
	if visionSvc == nil {
		return false
	}
	err, everWorked := visionSvc.LastFailure()
	return err != nil && !everWorked
}

// visionReady reports whether a frame could actually be captured AND
// understood right now, and why not when it cannot.
//
// Both halves are required and they fail for unrelated reasons: the model half
// is a capability question (is the selected model multimodal?), the capture
// half is a host question (is there an ffmpeg to shell out to?). Checking only
// the first is what let /eyes on succeed on a machine that could never produce
// a frame.
func visionReady() (bool, string) {
	if !captureAvailable() {
		return false, "no ffmpeg on PATH — /setup installs it"
	}
	if !visionAvailable() {
		return false, fmt.Sprintf("%s cannot process images", visionRouteDescription())
	}
	return true, ""
}

// blackBoxDetail is the /help expansion. It names the safety valve first
// because that is the thing a user in live mode most urgently needs to know.
func blackBoxDetail() []string {
	// No restatement of Summary here: /help <command> prints the summary
	// directly above this block, and the two said the same sentence twice.
	out := []string{
		"Say \"manual mode\" at any time to return to the keyboard. Ctrl+C stops a",
		"reply mid-sentence. \"Turn off your eyes\" closes the camera without",
		"leaving the conversation.",
		"",
	}
	out = append(out, blackBoxUsage...)
	return append(out,
		"",
		"Camera frames are captured one at a time, held in memory, and never",
		"written to disk. Only metadata reaches the journal.",
		"",
		"Nothing you say is stored unless you ask: /blackbox log on keeps a local",
		"text record of transcripts and replies (never audio), and /purge wipes it.",
		"",
		"The microphone is muted while Helix is speaking — it cannot hear itself,",
		"which also means it cannot be interrupted by voice mid-sentence.")
}

// blackBoxMigrationNote answers the user who types a command that no longer
// exists. Eight verbs became one, and a bare "unknown command" for a verb that
// worked yesterday is a bad way to learn that.
func blackBoxMigrationNote(verb string) (string, bool) {
	moved := map[string]string{
		"/voice":        "/blackbox on",
		"/manual":       "/blackbox off",
		"/voice-setup":  "/blackbox setup",
		"/voice-status": "/blackbox status",
		"/eyes":         "/blackbox eyes on|off  (or /blackbox look)",
		"/wake":         "/blackbox wake on|off",
		"/tts":          "/blackbox tts on|off",
		"/say":          "/blackbox say <text>",
	}
	to, ok := moved[strings.ToLower(strings.TrimSpace(verb))]
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%s folded into /blackbox — use %s", verb, to), true
}
