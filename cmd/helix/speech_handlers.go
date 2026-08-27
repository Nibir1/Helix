// cmd/helix/speech_handlers.go
// Purpose: BlackBox Phase 1 speech UX — /voice-setup wizard with transparent
// pricing (ADR-006), /say, /tts, /listen, and /voice-status. Follows the
// provider-wizard conventions of helpers.go (useProviderInteractive).
package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/shell"
	"helix/internal/speech"

	"helix/internal/providers"
	"net/http"

	"github.com/fatih/color"
	"helix/internal/edge"
	"net/url"
	"runtime"
)

// speechConfigFrom maps the persisted config section onto the speech package
// selection.
func speechConfigFrom(sc config.SpeechConfig) speech.Config {
	return sc.Runtime()
}

// knownVoices lists valid voice identifiers per TTS provider.
//
// This exists because the wizard used to ask for a bare "Voice id/name" right
// after printing a table whose most prominent column is the MODEL — so the
// obvious answer was to retype the model, which the provider then rejects with
// an HTTP 400 at /say time, long after the wizard said "Voice link configured."
// Showing the real options at the moment of asking turns a paid round trip into
// a correct answer.
var knownVoices = map[string][]string{
	"openai": {"alloy", "ash", "ballad", "cedar", "coral", "echo", "fable",
		"marin", "nova", "onyx", "sage", "shimmer", "verse"},
	"deepgram":     {"aura-2-thalia-en", "aura-2-andromeda-en", "aura-2-apollo-en"},
	"elevenlabs":   {"<voice id from your ElevenLabs voice library>"},
	"kokoro-local": {"af_bella", "af_sarah", "am_adam", "bf_emma"},
	"piper-local":  {"<voice model name installed in your Piper sidecar>"},
}

// askVoiceID prompts for the voice, showing the provider's valid values.
//
// Piper is the awkward case and gets its own explanation. Its "voice" is not a
// name from a fixed list — it is the .onnx model file you launched the server
// with — so Helix cannot enumerate them, and printing a placeholder in angle
// brackets told the user nothing except that Helix did not know either. Naming
// what the value IS, and where the models come from, is the honest version.
func askVoiceID(provider string) string {
	def := "provider default"
	if provider == "piper-local" {
		fmt.Println(shell.PanelTitle("voice"))
		for _, l := range shell.PanelWrap(
			"Piper has no voice list — the voice IS the .onnx model the server was "+
				"launched with, so leaving this blank is the right answer here.",
			shell.Muted) {
			fmt.Println(l)
		}
		fmt.Println(shell.PanelGap())
		fmt.Println(shell.PanelLine(shell.Muted("browse  ") +
			shell.Value("https://huggingface.co/rhasspy/piper-voices")))
		fmt.Println(shell.PanelLine(shell.Muted("examples ") +
			shell.Value("en_US-lessac-medium.onnx  ·  en_GB-alba-medium.onnx")))
		fmt.Println(shell.PanelEnd())
		def = "leave blank"
	} else if voices, ok := knownVoices[provider]; ok {
		fmt.Println(shell.PanelTitle("voice"))
		for _, v := range voices {
			fmt.Println(shell.PanelLine(shell.Value(v)))
		}
		fmt.Println(shell.PanelEnd())
	}
	voice := strings.TrimSpace(commands.AskLine(
		shell.Prompt("voice name — not the model name", def)))
	if voice == "" {
		return ""
	}

	// Catch the exact mistake the old prompt invited, while it is still free
	// to correct, rather than at the first paid synthesis.
	if valid, ok := knownVoices[provider]; ok && !validName(valid, voice) &&
		!strings.HasPrefix(valid[0], "<") {
		wizStep(shell.StateWarn, voice, "is not a known "+provider+" voice")
		wizDetail("valid: " + strings.Join(valid, ", "))
		if !wizConfirm("use it anyway") {
			wizStep(shell.StateIdle, provider, "using the provider default voice")
			return ""
		}
	}
	return voice
}

// askFallback prompts for a fallback PROVIDER, describing each option.
//
// A bare list of names is not a choice anyone can make well: "deepgram,
// elevenlabs, kokoro-local, openai" says nothing about which are free, which
// need a key you do not have, or which need a server you are not running. So
// each option is shown with what it costs, whether it is local, and whether it
// is READY — which is the deciding fact, since a fallback needing a key you have
// not set is not a fallback at all.
//
// Numbered, like the main table, so the answer is a digit rather than a name
// spelled exactly right.
func askFallback(kind string, names []string, primary string) []string {
	// Callers pass the DISPLAY form ("STT"); every lookup below keys on the
	// lowercase registry name. Without this, speechCredentialState matched no
	// case and the whole table rendered as "unknown".
	key := strings.ToLower(kind)

	options := make([]string, 0, len(names))
	for _, n := range names {
		if n != primary {
			options = append(options, n)
		}
	}
	if len(options) == 0 {
		return nil
	}

	fmt.Println(shell.PanelTitle("fallback " + strings.ToLower(kind)))
	for _, l := range shell.PanelWrap(
		"used only when "+primary+" fails — pick one that can already serve a request, "+
			"or leave it blank", shell.Muted) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelGap())

	cells := make([][]string, 0, len(options))
	for i, name := range options {
		row := fallbackRow(key, name)
		cells = append(cells, []string{
			shell.Fg(shell.HexMuted, fmt.Sprintf("%2d)", i+1)),
			shell.Value(name),
			shell.Muted(row.cost),
			shell.Badge(row.state, row.ready),
			shell.Muted(row.note),
		})
	}
	for _, l := range shell.Table([]string{"", "provider", "cost", "ready", ""}, cells) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelEnd())

	answer := strings.TrimSpace(commands.AskLine(
		shell.Prompt("fallback "+strings.ToLower(kind)+" — number, name, or blank", "none")))
	if answer == "" {
		return nil
	}

	fallback := answer
	if n, err := strconv.Atoi(answer); err == nil {
		if n < 1 || n > len(options) {
			wizStep(shell.StateIdle, fmt.Sprintf("option %d", n),
				"does not exist — skipping the fallback")
			return nil
		}
		fallback = options[n-1]
	}

	if !validName(names, fallback) {
		wizStep(shell.StateIdle, fallback, "is not a registered provider — ignored")
		wizDetail("valid: " + strings.Join(options, ", "))
		return nil
	}
	if fallback == primary {
		wizStep(shell.StateIdle, fallback,
			"is already the primary — a chain cannot fall back to itself")
		return nil
	}
	return []string{fallback}
}

// fallbackDescription is one rendered row of the fallback table.
type fallbackDescription struct {
	cost  string
	ready string
	note  string
	// state colours the readiness word. Readiness is the only column that
	// decides the choice, and five look-alike phrases in one grey column made
	// the reader compare them by hand.
	state shell.State
}

// fallbackRow describes a candidate fallback: what it costs, and — the part that
// actually decides the choice — whether it can serve a request today.
func fallbackRow(kind, name string) fallbackDescription {
	out := fallbackDescription{cost: "—", ready: "unknown", state: shell.StateIdle}

	reg := speech.Default()
	if reg == nil {
		return out
	}
	requiresKey, hasKey, ok := speechCredentialState(reg, kind, name)
	if !ok {
		return out
	}

	if catalog, err := speech.LoadMergedCatalog(); err == nil {
		for _, e := range catalog {
			if e.Kind == kind && e.Provider == name {
				out.cost = speech.FormatUnit(e)
				break
			}
		}
	}

	switch {
	case !requiresKey:
		// Local sidecars: free, but only useful if the server is running, and
		// the port is the thing that usually is not.
		out.cost = "free"
		if spec, isSidecar := sidecarSpecs()[name]; isSidecar {
			// A container-hosted sidecar on a host with no daemon is not
			// "not running yet" — it is not available, and saying so here is
			// the difference between an informed skip and the QA session that
			// picked kokoro and hit a failed docker pull.
			if vs, known := voiceSidecars()[name]; known && vs.Unmet != nil {
				if _, unmet := vs.Unmet(); unmet {
					out.ready, out.state = "needs docker", shell.StateBad
					out.note = "piper-local is the docker-free voice"
					return out
				}
			}
			endpoint := sidecarEndpoint(kind, name, spec.Default)
			// Ask the sidecar directly rather than inferring from the port.
			// "port in use" was a guess that could mean the sidecar is running,
			// or that something unrelated holds the address — two states needing
			// opposite actions, reported identically.
			switch {
			case sidecarAnswersAt(kind, name, endpoint):
				out.ready, out.state = "running", shell.StateGood
				out.note = endpoint
			case portOfEndpoint(endpoint) > 0 && !edge.PortAvailable(portOfEndpoint(endpoint)):
				out.ready, out.state = "port taken", shell.StateBad
				out.note = "something else holds " + endpoint
			default:
				out.ready, out.state = "not started", shell.StateWarn
				out.note = "Helix can start it: " + endpoint
			}
		} else {
			out.ready, out.state = "ready", shell.StateGood
		}
	case hasKey:
		out.ready, out.state = "ready", shell.StateGood
		out.note = "key already stored"
	default:
		out.ready, out.state = "needs a key", shell.StateWarn
		out.note = "you will be asked for one"
	}
	return out
}

// sidecarEndpoint returns the configured endpoint for a local provider, or its
// default.
func sidecarEndpoint(kind, name, fallback string) string {
	switch kind {
	case "stt":
		if url, ok := cfg.Speech.STT.Endpoints[name]; ok && strings.TrimSpace(url) != "" {
			return url
		}
		if cfg.Speech.STT.Provider == name && strings.TrimSpace(cfg.Speech.STT.BaseURL) != "" {
			return cfg.Speech.STT.BaseURL
		}
	case "tts":
		if url, ok := cfg.Speech.TTS.Endpoints[name]; ok && strings.TrimSpace(url) != "" {
			return url
		}
		if cfg.Speech.TTS.Provider == name && strings.TrimSpace(cfg.Speech.TTS.BaseURL) != "" {
			return cfg.Speech.TTS.BaseURL
		}
	}
	return fallback
}

// handleVoiceSetup runs the STT/TTS selection wizard with live pricing.
func handleVoiceSetup() {
	fmt.Println(shell.PanelTitle("voice link"))
	fmt.Println(shell.PanelLine(shell.Muted("pick what hears you and what answers — locally or in the cloud")))
	fmt.Println(shell.PanelEnd())
	verifiedThisRun = map[string]bool{}

	catalog, err := speech.LoadMergedCatalog()
	if err != nil {
		wizStep(shell.StateWarn, "pricing catalog", err.Error())
	}

	reg := speech.Default()
	if reg == nil {
		wizStep(shell.StateBad, "speech engine", "not initialized")
		return
	}

	// ---- recommended chains (P9.7) ----
	// Offered before the tables, because for most people this is the whole
	// decision and the tables are the escape hatch rather than the other way
	// round. A chosen preset still walks the same key/sidecar/verify path
	// below — it pre-fills answers, it does not bypass steps.
	if applied, ok := offerSpeechPresets(reg, catalog); ok {
		commitSpeechSelection(applied.stt, applied.tts)
		return
	}

	// ---- STT selection ----
	sttNames := reg.STTNames()
	sttRows := filterCatalog(catalog, "stt", sttNames)
	printSpeechTable("SPEECH-TO-TEXT (STT) PROVIDERS", sttRows)

	choice := askNumber(shell.Prompt("which should hear you", "skip"), len(sttRows))
	var sttCfg config.SpeechSTTConfig
	if choice > 0 {
		entry := sttRows[choice-1]
		sttCfg.Provider = entry.Provider
		sttCfg.Model = entry.APIModel()
		sttCfg.BaseURL = autoAssignSidecarPort("stt", entry.Provider, cfg.Speech.STT.BaseURL)
		prepareSpeechProvider("stt", entry.Provider)
		sttCfg.Fallbacks = askFallback("STT", sttNames, entry.Provider)
		// A fallback with no key is not a fallback. Each one gets the same
		// treatment as the primary — previously they were selected by name and
		// never asked about, so the chain looked deeper than it was.
		for _, name := range sttCfg.Fallbacks {
			autoAssignSidecarPort("stt", name, "")
			prepareSpeechProvider("stt", name)
		}
		wizStep(shell.StateGood, sttCfg.Provider, "will hear you")
	}

	// ---- TTS selection ----
	ttsNames := reg.TTSNames()
	ttsRows := filterCatalog(catalog, "tts", ttsNames)
	printSpeechTable("TEXT-TO-SPEECH (TTS) PROVIDERS", ttsRows)

	choice = askNumber(shell.Prompt("which should answer you", "skip"), len(ttsRows))
	var ttsCfg config.SpeechTTSConfig
	if choice > 0 {
		entry := ttsRows[choice-1]
		ttsCfg.Provider = entry.Provider
		ttsCfg.Model = entry.APIModel()
		ttsCfg.BaseURL = autoAssignSidecarPort("tts", entry.Provider, cfg.Speech.TTS.BaseURL)
		prepareSpeechProvider("tts", entry.Provider)
		ttsCfg.Voice = askVoiceID(entry.Provider)
		ttsCfg.Fallbacks = askFallback("TTS", ttsNames, entry.Provider)
		for _, name := range ttsCfg.Fallbacks {
			autoAssignSidecarPort("tts", name, "")
			prepareSpeechProvider("tts", name)
		}
		wizStep(shell.StateGood, ttsCfg.Provider, "will answer you")
	}

	commitSpeechSelection(sttCfg, ttsCfg)
}

// commitSpeechSelection persists a chosen chain, re-initializes the engine, and
// verifies it before claiming success.
//
// Shared by the manual path and the presets so there is exactly one place that
// knows how to write this config. That matters more here than anywhere else in
// the wizard: this function is where Endpoints was dropped the second time, and
// a preset path with its own copy of the merge would have been a third.
func commitSpeechSelection(sttCfg config.SpeechSTTConfig, ttsCfg config.SpeechTTSConfig) {
	if sttCfg.Provider == "" && ttsCfg.Provider == "" {
		fmt.Println(shell.Hint("voice setup skipped — /blackbox setup runs it again"))
		return
	}

	// Field-wise merge: the wizard only owns the sections the user actually
	// selected. WakeWord config and per-section tuning (StreamChunkMs,
	// FirstByteMs) must survive a re-run — a whole-struct replace here used
	// to silently disable /wake and wipe custom phrases.
	//
	// Endpoints is the SAME bug, caught a second time. The wizard starts a
	// sidecar, discovers its port is taken, moves it to a free one and records
	// that in Endpoints — and then this assignment threw the record away, so
	// whisper-local ran happily on 28861 while the chain kept dialling 8080 and
	// reported "still not answering" about a server it had just started itself.
	// Anything the wizard does not collect has to be carried across explicitly.
	if sttCfg.Provider != "" {
		sttCfg.StreamChunkMs = cfg.Speech.STT.StreamChunkMs
		sttCfg.Endpoints = cfg.Speech.STT.Endpoints
		cfg.Speech.STT = sttCfg
	}
	if ttsCfg.Provider != "" {
		ttsCfg.FirstByteMs = cfg.Speech.TTS.FirstByteMs
		ttsCfg.Endpoints = cfg.Speech.TTS.Endpoints
		cfg.Speech.TTS = ttsCfg
	}
	if err := cfg.SavePreferences(); err != nil {
		wizStep(shell.StateBad, "preferences", "could not be saved: "+err.Error())
	}
	if err := speech.Init(speechConfigFrom(cfg.Speech)); err != nil {
		wizStep(shell.StateBad, "speech engine", "re-init failed: "+err.Error())
		return
	}

	// Verify BEFORE claiming success, which this had stopped doing: the
	// "configured" line printed first and the verification contradicted it two
	// lines later, so a run where piper never started still opened with a green
	// "Voice link configured." The comment already said to verify first; the
	// code had drifted from it.
	report, ok := verifySpeechSelection()
	printVoiceLinkSummary(report, ok)
}

// printVoiceLinkSummary closes the wizard with what was actually configured.
//
// The three facts a wizard owes you at the end — did it work, what will hear
// you, what will answer — used to arrive as three unrelated flat lines
// ("Chain verified…", "Voice link configured.", "Recorder detected: rec"),
// each a different colour, none of them naming the providers that had just been
// set up. A summary that does not state its own result is not a summary. This
// is deliberately the same shape as /blackbox status, so the screen the user
// sees at the end of setup is the screen they will check afterwards.
func printVoiceLinkSummary(report speech.StatusReport, ok bool) {
	fmt.Println(shell.PanelTitle("voice link"))
	if ok {
		fmt.Println(shell.Step(shell.StateGood, "ready",
			"every selected provider answered"))
	} else {
		fmt.Println(shell.Step(shell.StateWarn, "saved, not usable yet",
			"fix the above, then re-check with /blackbox status"))
	}

	w := shell.KVWidth("HEAR", "SPEAK", "MIC")
	fmt.Println(shell.KV("HEAR", chainOrNone(report.STTChain), w))
	fmt.Println(shell.KV("SPEAK", chainOrNone(report.TTSChain), w))
	if rec, rerr := speech.DetectRecorder(); rerr != nil {
		// A missing recorder is a warning, not a failure of the chain: the TTS
		// half works without one, and saying so beats a red line that implies
		// the whole setup failed.
		fmt.Println(shell.KV("MIC", shell.Badge(shell.StateWarn, "none detected")+
			shell.Muted("  "+rerr.Error()), w))
	} else {
		fmt.Println(shell.KV("MIC", shell.Badge(shell.StateGood, rec), w))
	}
	fmt.Println(shell.PanelEnd())
	fmt.Println(shell.Hint("/blackbox say voice link online   ·   /blackbox on   ·   /mictest"))
}

// verifySpeechSelection probes the newly selected chain and reports what it
// found.
//
// Returns the probe report as well as the verdict: the closing summary needs the
// very same chains this just resolved, and probing a second time to render them
// would be both slower and capable of disagreeing with the line above it.
func verifySpeechSelection() (speech.StatusReport, bool) {
	// Endpoint collisions first: they explain a "reachable" probe that then fails
	// every request, and no per-provider check can see them.
	reportEndpointConflicts()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report := speech.Status(ctx)
	problems := 0
	for _, group := range [][]speech.ProviderStatusRow{report.STTStatus, report.TTSStatus} {
		for _, row := range group {
			if !row.InChain || row.Healthy {
				continue
			}
			problems++
			// The full diagnosis was already printed when this provider was
			// selected a moment ago. Repeating a twelve-line block verbatim in
			// the summary buries the one thing the summary adds — the count.
			if verifiedThisRun[row.Name] {
				wizStep(shell.StateBad, row.Name, "still not answering (details above)")
				continue
			}
			wizStep(shell.StateBad, row.Name, "in your chain, but not answering")
			for _, line := range providerDetailLines(row) {
				wizDetail(line)
			}
		}
	}

	if problems == 0 {
		return report, true
	}
	wizStep(shell.StateWarn, fmt.Sprintf("%d provider(s)", problems),
		"cannot serve a request yet — no need to re-run the wizard")
	return report, false
}

// filterCatalog narrows pricing rows to registered providers of one kind.
func filterCatalog(entries []speech.PricingEntry, kind string, names []string) []speech.PricingEntry {
	var out []speech.PricingEntry
	for _, e := range entries {
		if e.Kind != kind {
			continue
		}
		for _, n := range names {
			if e.Provider == n {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

// printSpeechTable renders the pricing comparison required by the original
// plan's Layer 1 UX: price, estimated monthly cost, latency, key requirement,
// locality, and the recommended badge.
func printSpeechTable(title string, rows []speech.PricingEntry) {
	fmt.Println(shell.PanelTitle(title))
	if len(rows) == 0 {
		fmt.Println(shell.PanelLine(shell.Muted("(no registered providers)")))
		fmt.Println(shell.PanelEnd())
		return
	}

	// Seven columns, not nine. The first version carried the unit price AND the
	// monthly estimate AND separate "needs"/"where" columns, which ran past the
	// panel and wrapped — "★ best value" landed on its own line at column zero,
	// which is worse than any information it carried. The monthly figure is
	// derived from the unit price and is the number people actually decide on,
	// so the unit price goes and the rest fits.
	cells := make([][]string, 0, len(rows))
	for i, e := range rows {
		cost := shell.Value(fmt.Sprintf("$%.2f", speech.EstimateMonthlyCost(e, 2)))
		if e.Local {
			// "0.00" in a column of dollars reads as a rounding error rather
			// than as the entire point of running local.
			cost = shell.Fg(shell.HexPrimary, "free")
		}

		// Short enough that the fitter never has to cut it. "api key · cl…"
		// loses the cloud/local distinction, which is the half of this column
		// that actually decides anything.
		requires := shell.Muted("key · cloud")
		if !e.RequiresKey {
			requires = shell.Fg(shell.HexPrimary, "free · local")
		}
		// Say it in the table, not after the choice. QA picked kokoro, was
		// walked through a pull, and hit "Cannot connect to the Docker daemon"
		// as the last line of setup.
		if e.Provider == "kokoro-local" {
			if dockerAvailable() {
				requires = shell.Muted("docker")
			} else {
				requires = shell.Fg(shell.HexRectifier, "no docker")
			}
		}
		mark := ""
		if e.Recommended {
			mark = shell.Fg(shell.HexSecondary, "★")
		}
		cells = append(cells, []string{
			shell.Fg(shell.HexMuted, fmt.Sprintf("%2d)", i+1)),
			shell.Value(e.Provider),
			shell.Fg(shell.HexText, truncStr(e.Model, 22)),
			cost,
			shell.Muted(e.Latency),
			requires,
			mark,
		})
	}
	for _, l := range shell.Table(
		[]string{"", "provider", "model", "$/month", "latency", "requires", ""},
		cells) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelGap())
	fmt.Println(shell.PanelLine(shell.Muted("★ best value  ·  cost estimated at 2 hours a day")))
	fmt.Println(shell.PanelEnd())
}

// truncStr shortens s to at most n display columns, ending in an ellipsis.
//
// Rune-based, not byte-based: byte-slicing a UTF-8 string can cut a rune in
// half, and this is used on model names, provider errors, and the /say echo —
// all of which can carry non-ASCII. A truncated model name that renders as a
// replacement character reads like a corrupted config.
func truncStr(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func validName(names []string, s string) bool {
	for _, n := range names {
		if n == s {
			return true
		}
	}
	return false
}

// askNumber reads a 1..max selection; 0 = skipped, -1 = invalid.
func askNumber(prompt string, max int) int {
	raw := strings.TrimSpace(commands.AskLine(prompt))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > max {
		return -1
	}
	return n
}

// handleSayCommand synthesizes and speaks its argument through the TTS chain.
func handleSayCommand(c cmdArgs) {
	text := c.Rest
	if text == "" {
		fmt.Println("Usage: /blackbox say <text>")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// speech.Speak takes the streamed-first path (P7.2c): playback begins after
	// the provider's preroll instead of after the whole synthesis, and it falls
	// back to the buffered path by itself. The old Synthesize+PlaySpeech pair
	// bypassed streaming entirely, which made /say the one command guaranteed to
	// blow the first-byte budget — and to leave /voice-status reporting
	// "buffered" even on a build where streaming works.
	fmt.Println(shell.Muted("speaking  ") + shell.Value(truncStr(text, 60)))
	if err := speech.Speak(ctx, text); err != nil {
		color.Red("Speech failed: %v", err)
		return
	}

	// Byte count and container kind are not knowable up front on the streamed
	// path — the body is still arriving while it plays — so report what the
	// budget line actually cares about: which path served it, and how long the
	// first audio took.
	path := "buffered"
	if speech.LastSpeechStreamed() {
		path = "streamed"
	}
	fmt.Println(shell.Hint(fmt.Sprintf("%s  ·  first audio %dms",
		path, speech.LastSynthesizeLatencyMs())))
}

// handleTTSCommand toggles automatic spoken responses (Phase 2 gate).
func handleTTSCommand(c cmdArgs) {
	switch c.Lower() {
	case "on":
		speech.SetTTSEnabled(true)
		color.Green("Replies are now spoken aloud.")
	case "off":
		speech.SetTTSEnabled(false)
		// Yellow rather than green: switching a capability off is the same kind
		// of event as /blackbox wake off, and it should read the same way.
		color.Yellow("Replies are silent — /blackbox say still speaks on demand.")
	default:
		state := shell.Badge(shell.StateIdle, "silent")
		if speech.TTSEnabled() {
			state = shell.Badge(shell.StateGood, "spoken aloud")
		}
		fmt.Println(shell.KV("REPLIES", state, shell.KVWidth("REPLIES")))
		fmt.Println(shell.Hint("/blackbox tts <on|off>"))
	}
}

// handleListenCommand records one clip and prints the transcription
// (push-to-talk dev utility; the interactive voice loop arrives in Phase 2).
func handleListenCommand(c cmdArgs) {
	seconds := 8
	if !c.Empty() {
		n, err := strconv.Atoi(c.Arg(0))
		// A bad duration used to be silently ignored and replaced with 8s.
		// Say so: the user asked for a specific window.
		if err != nil || n <= 0 || n > 60 {
			color.Yellow("Duration must be a whole number of seconds between 1 and 60; using %ds.", seconds)
		} else {
			seconds = n
		}
	}

	if _, err := speech.DetectRecorder(); err != nil {
		color.Red("%v", err)
		return
	}
	fmt.Printf("Listening for up to %ds (speak now; stops after ~2s of silence with sox)...\n", seconds)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second+5*time.Second)
	defer cancel()

	clip, err := speech.RecordClip(ctx, speech.CaptureOptions{MaxDuration: time.Duration(seconds) * time.Second})
	if err != nil {
		color.Red("Capture failed: %v", err)
		return
	}

	tctx, tcancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer tcancel()
	transcript, err := speech.Transcribe(tctx, clip)
	if err != nil {
		color.Red("Transcription failed: %v", err)
		return
	}

	conf := ""
	if transcript.Confidence > 0 {
		conf = fmt.Sprintf(", confidence %.2f", transcript.Confidence)
	}
	fmt.Println("  " + shell.Fg(shell.HexSecondary, "❯ ") +
		shell.Fg(shell.HexText, transcript.Text) + shell.Muted("   "+transcript.Provider+conf))
}

// handleVoiceStatus prints chains, provider health, and recorder state.
func handleVoiceStatus() {
	fmt.Println(shell.PanelTitle("voice chain"))

	// A local sidecar sharing its port with the LLM runtime is the most common
	// reason local STT/TTS "does not work", and it is invisible in the health
	// rows below — whichever service owns the port answers, so the probe sees a
	// live socket either way.
	reportEndpointConflicts()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report := speech.Status(ctx)

	w := shell.KVWidth("HEAR", "SPEAK", "REPLIES", "MIC")
	fmt.Println(shell.KV("HEAR", chainOrNone(report.STTChain), w))
	fmt.Println(shell.KV("SPEAK", chainOrNone(report.TTSChain), w))
	if speech.TTSEnabled() {
		fmt.Println(shell.KV("REPLIES", shell.Badge(shell.StateGood, "spoken aloud"), w))
	} else {
		fmt.Println(shell.KV("REPLIES", shell.Badge(shell.StateIdle, "silent")+
			shell.Muted("  /blackbox tts on"), w))
	}
	if report.Recorder != "" {
		fmt.Println(shell.KV("MIC", shell.Badge(shell.StateGood, report.Recorder), w))
	} else {
		fmt.Println(shell.KV("MIC", shell.Badge(shell.StateBad, report.RecorderErr), w))
	}
	if report.TTSLastLatencyMs > 0 {
		path := "buffered"
		if report.TTSLastStreamed {
			path = "streamed"
		}
		state := shell.StateGood
		if report.TTSFirstByteBudgetMs > 0 && report.TTSLastLatencyMs > int64(report.TTSFirstByteBudgetMs) {
			state = shell.StateWarn
		}
		fmt.Println(shell.KV("LATENCY",
			shell.Badge(state, fmt.Sprintf("%dms to first audio", report.TTSLastLatencyMs))+
				shell.Muted(fmt.Sprintf("  budget %dms  ·  %s", report.TTSFirstByteBudgetMs, path)), w))
	}

	printStatusRows("hearing", report.STTStatus)
	printStatusRows("speech", report.TTSStatus)
	printVoiceVocabulary()
	fmt.Println(shell.PanelEnd())
}

// printVoiceVocabulary lists what can be reached by speaking.
//
// Without this the spoken command surface is undiscoverable: there is no menu to
// read when your hands are busy, and guessing at phrasing is a bad experience.
// It is called from INSIDE the voice-chain panel, between the health rows and
// the closing rule, so every line has to sit behind the gutter. It did not: a
// bare heading and a column-zero list ran straight through the frame, and the
// PanelEnd underneath then looked like a rule belonging to nothing.
func printVoiceVocabulary() {
	fmt.Println(shell.PanelGap())
	fmt.Println(shell.PanelSection("spoken commands"))
	for _, line := range voiceCommandVocabulary() {
		fmt.Println(shell.PanelLine(shell.Muted(line)))
	}
	fmt.Println(shell.PanelGap())
	for _, l := range shell.PanelWrap(
		"\"slash <command name>\" reaches any voice-enabled command directly — "+
			"e.g. \"slash provider status\" or \"slash knowledge status\".", shell.Muted) {
		fmt.Println(l)
	}

	// The two phrases that END a turn rather than being served by it. They are
	// matched as suffixes and never reach the table above, so a vocabulary list
	// built only from the routes leaves out the safety valve and the restart —
	// the two things most worth knowing you can say.
	fmt.Println(shell.PanelGap())
	fmt.Println(shell.PanelSection("said at the end of anything"))
	vw := shell.KVWidth("\"manual mode\"", "\"reboot\"")
	fmt.Println(shell.KV("\"manual mode\"",
		shell.Muted("back to the keyboard  ·  also \"stop listening\""), vw))
	fmt.Println(shell.KV("\"reboot\"",
		shell.Muted("restart the shell, coming back listening  ·  also \"please reboot\""), vw))
	// Read from the registry rather than restated here: the denied set shrank
	// when live mode arrived, and a hand-kept copy of a security policy is a
	// copy that goes stale silently.
	if denied := voiceDeniedCommandNames(); len(denied) > 0 {
		fmt.Println(shell.PanelGap())
		fmt.Println(shell.Step(shell.StateWarn, "deny-list",
			"unreachable by voice by design — these must be typed"))
		for _, l := range shell.StepDetail(strings.Join(denied, "  "), shell.Muted) {
			fmt.Println(l)
		}
	}
}

func chainOrNone(chain []string) string {
	if len(chain) == 0 {
		return shell.Badge(shell.StateWarn, "not configured") +
			shell.Muted("  /blackbox setup")
	}
	// The primary is the one that will actually answer; the rest are insurance.
	// Colouring them the same made a three-deep chain read as three equals.
	out := shell.Value(chain[0])
	for _, next := range chain[1:] {
		out += shell.Arrow() + shell.Muted(next)
	}
	return out
}

func printStatusRows(title string, rows []speech.ProviderStatusRow) {
	fmt.Println(shell.PanelGap())
	fmt.Println(shell.PanelSection(title))
	for _, line := range statusRowLines(rows) {
		fmt.Println(line)
	}
}

// Column widths for the /voice-status provider table.
//
// The health DETAIL is deliberately not a column. A provider failure is a
// sentence — a dialed URL, a TLS message, an HTTP body — and forcing one into an
// 8-character cell is exactly what produced the QA line:
//
//	whisper-local  Whisper (local sidecar)  whisper-local unreachable: Get "http://127.0.0.… key    local
//
// where the URL is cut mid-address AND every column after it is shoved out of
// alignment. The state column now holds one word and the detail goes on its own
// wrapped, indented lines below the row.
const (
	statusNameWidth    = 14
	statusDisplayWidth = 24
	statusStateWidth   = 8
	statusKeyWidth     = 7
)

// statusDetailWidth is the wrap width for the indented detail lines.
//
// It is the RENDERED budget minus what the frame costs: the panel gutter
// ("  │ ", 4 cells) plus statusDetailIndent (6). Wrapping at the budget itself
// and then adding the frame is how an 80-column terminal ended up with an
// 80-column detail line inside a 72-column allowance.
const (
	statusDetailBudget = 72
	statusDetailFrame  = 4 + len(statusDetailIndent)
	statusDetailWidth  = statusDetailBudget - statusDetailFrame
)

// statusDetailIndent hangs the detail lines under their row.
const statusDetailIndent = "      "

// statusRowLines renders one provider table as plain lines.
//
// It returns strings rather than printing so the layout — the part that broke —
// is testable without a terminal.
//
// Args:
//   - rows: provider status rows from speech.Status.
//
// Returns: the lines to print, in order.
// Complexity: O(rows × detail length).
func statusRowLines(rows []speech.ProviderStatusRow) []string {
	if len(rows) == 0 {
		return []string{shell.PanelLine(shell.Muted("(no registered providers)"))}
	}

	cells := make([][]string, 0, len(rows))
	details := make([][]string, 0, len(rows))
	for _, r := range rows {
		cells = append(cells, []string{
			shell.Value(truncStr(r.Name, statusNameWidth)),
			shell.Muted(truncStr(r.Display, statusDisplayWidth)),
			providerStateBadge(r),
			providerKeyState(r),
			locality(r.Local),
		})
		details = append(details, providerDetailLines(r))
	}

	// Interleave each row's detail lines under it. shell.Table renders the grid;
	// the details are prose and deliberately are NOT columns (see the comment on
	// the width constants).
	table := shell.Table([]string{"provider", "name", "state", "key", "where"}, cells)
	out := []string{table[0]}
	for i, line := range table[1:] {
		out = append(out, line)
		for _, detail := range details[i] {
			out = append(out, shell.PanelLine(statusDetailIndent+shell.Muted(detail)))
		}
	}
	return out
}

// providerStateBadge reduces a row to one coloured state word.
//
// "standby" and "down" are genuinely different and were once conflated: an
// out-of-chain provider is never probed, so its Healthy=false means "not being
// used", not "broken". The colours now carry that distinction too, so a broken
// chain is visible while skimming instead of requiring five words to be
// compared.
func providerStateBadge(r speech.ProviderStatusRow) string {
	switch {
	case r.Healthy:
		return shell.Badge(shell.StateGood, "healthy")
	case !r.InChain:
		return shell.Badge(shell.StateIdle, "standby")
	default:
		return shell.Badge(shell.StateBad, "down")
	}
}

// providerKeyState describes the credential situation in one word.
//
// "key" now means what it says — a key is stored. Keyless local sidecars report
// "free"; the QA line showed an unreachable whisper.cpp server as "key", which
// invited exactly the wrong diagnosis (a bad credential rather than a process
// that is not running).
func providerKeyState(r speech.ProviderStatusRow) string {
	switch {
	case !r.RequiresKey:
		return "free"
	case r.HasKey:
		return "key"
	default:
		return "no key"
	}
}

// providerDetailLines returns the wrapped explanation printed under a row:
// the full health detail, plus a start-it hint when a local sidecar that the
// active chain depends on is down.
func providerDetailLines(r speech.ProviderStatusRow) []string {
	if r.Healthy {
		// A HEALTHY local sidecar still gets its address and route printed:
		// "healthy" on the wrong port is the confusing case, and knowing which
		// route answered is how the user confirms it is the service they meant.
		if r.Endpoint == "" {
			return nil
		}
		where := "endpoint: " + r.Endpoint
		if r.Route != "" {
			where += "  route: " + r.Route
		}
		return []string{where}
	}

	var lines []string
	if r.Endpoint != "" {
		// Which address, and which route answered. Both are the first questions
		// when a local sidecar misbehaves, and both used to be invisible.
		where := "endpoint: " + r.Endpoint
		if r.Route != "" {
			where += "  route: " + r.Route
		}
		lines = append(lines, where)
	}

	detail := strings.TrimSpace(r.HealthDetail)
	carriesGuidance := false
	if detail != "" && detail != "standby" {
		// PRESERVE existing line structure. A local sidecar's diagnosis is
		// already formatted — statement, cause, indented commands — and running
		// it through the word wrapper collapsed all of that into a paragraph
		// with shell commands buried mid-sentence: "free port: (it currently
		// holds port 5000) Then start the sidecar on a free port: python3 -m
		// piper.http_server ... And point Helix at it: /config tts-url <url>".
		// Only genuinely single-line details get wrapped.
		lines = append(lines, reflowDetail(detail, statusDetailWidth)...)
		carriesGuidance = strings.Contains(detail, "\n")
	}

	// The start-it hint is a FALLBACK, not an addition. When the diagnosis
	// already carries a launch command, appending the static hint printed the
	// same advice twice in slightly different words.
	if r.InChain && r.Local && !carriesGuidance {
		lines = append(lines, localSidecarHints[r.Name]...)
	}
	return lines
}

// localSidecarHints maps a local provider to the command that starts it.
//
// Sidecars are user-managed by design (ADR-002) — Helix never launches one — so
// an accurate status plus the exact command IS the fix here. Wording tracks the
// sidecar section of scripts/edge-setup.sh and docs/edge_deployment.md §5.1;
// keep the three in sync.
//
// Each command sits alone on its line and is printed verbatim (never wrapped),
// because these are meant to be copy-pasted.
var localSidecarHints = map[string][]string{
	"whisper-local": {
		"start it (whisper.cpp — docs/edge_deployment.md §5.1):",
		"  ./build/bin/whisper-server -m models/ggml-base.en.bin --port 8081",
		"then point Helix at it (8080 is llama-server's default port too):",
		"  /config stt-url http://127.0.0.1:8081",
		"Helix speaks whisper.cpp's native /inference route directly.",
	},
	"piper-local": {
		"start it (Piper — docs/edge_deployment.md §5.1):",
		// The canonical form, from internal/speech, with the absolute model
		// path. The bare filename this used to print exists in no working
		// directory, so the command was unrunnable as shown.
		"  " + speech.PiperStartCmd("5001"),
		"then point Helix at it (macOS AirPlay Receiver owns port 5000):",
		"  /config tts-url http://127.0.0.1:5001",
	},
	"kokoro-local": {
		"start it (Kokoro-FastAPI — docs/edge_deployment.md §5.1):",
		"  docker run -p 8880:8880 ghcr.io/remsky/kokoro-fastapi-cpu",
	},
}

// wrapText breaks s into lines of at most width columns on word boundaries.
//
// A word longer than width is left whole rather than split: a URL cut in half is
// the failure this exists to prevent, so overflowing one line is the lesser
// evil.
//
// Args:
//   - s: the text to wrap.
//   - width: the maximum line width in runes.
//
// Returns: the wrapped lines (nil for blank input).
// Complexity: O(len(s)).
func wrapText(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	if width <= 0 {
		return []string{strings.Join(words, " ")}
	}

	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	return append(lines, cur)
}

func locality(local bool) string {
	if local {
		return "local"
	}
	return "cloud"
}

// reflowDetail renders a health detail for the status block.
//
// Multi-line details come from the local-sidecar diagnosis, which is already
// laid out deliberately: a statement, then the cause, then indented commands to
// copy. Those must survive verbatim — a command that has been word-wrapped into
// a sentence cannot be pasted. Single-line details are prose and do get wrapped.
func reflowDetail(detail string, width int) []string {
	if !strings.Contains(detail, "\n") {
		return wrapText(detail, width)
	}

	var out []string
	for _, line := range strings.Split(detail, "\n") {
		trimmedRight := strings.TrimRight(line, " \t")
		if strings.TrimSpace(trimmedRight) == "" {
			continue
		}
		// Long prose lines still wrap, but only when they carry no command:
		// indentation is the marker the diagnosis uses for "this is a command".
		if len(trimmedRight) > width && !strings.HasPrefix(line, " ") {
			out = append(out, wrapText(trimmedRight, width)...)
			continue
		}
		out = append(out, trimmedRight)
	}
	return out
}

// -------------------------------------------------------
// PROVIDER READINESS
// -------------------------------------------------------

// prepareSpeechProvider makes one selected provider actually usable: it settles
// the credential, then proves the choice with a live probe.
//
// Both halves were missing. The wizard prompted for a key unconditionally —
// re-typing one already stored — and never checked whether what you typed
// worked, so an expired or mistyped key was accepted silently and surfaced later
// as a failed /say. Fallbacks were not asked about at all, which made a chain
// look deeper than it was: a fallback with no key is not a fallback.
func prepareSpeechProvider(kind, provider string) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return
	}
	reg := speech.Default()
	if reg == nil {
		return
	}

	requiresKey, hasKey, ok := speechCredentialState(reg, kind, provider)
	if !ok {
		return
	}

	if requiresKey {
		if !settleSpeechKey(kind, provider, hasKey) {
			return
		}
	}
	if verifySpeechProvider(kind, provider, requiresKey) {
		return
	}

	// A local sidecar that is not answering is usually not installed or not
	// started — both of which Helix can now do. Printing the command and moving
	// on was the step that let voice setup complete five times without ever
	// producing speech.
	if !requiresKey {
		if offerSidecarSetup(kind, provider) {
			verifySpeechProvider(kind, provider, requiresKey)
		}
	}
}

// speechCredentialState reports whether a provider needs a key and whether one
// is stored.
func speechCredentialState(reg *speech.Registry, kind, provider string) (requiresKey, hasKey, ok bool) {
	switch kind {
	case "stt":
		p, found := reg.STTProvider(provider)
		if !found {
			return false, false, false
		}
		return p.RequiresAPIKey(), reg.Keys().Has(speech.STTKeyPrefix + provider), true
	case "tts":
		p, found := reg.TTSProvider(provider)
		if !found {
			return false, false, false
		}
		return p.RequiresAPIKey(), reg.Keys().Has(speech.TTSKeyPrefix + provider), true
	}
	return false, false, false
}

// settleSpeechKey reuses a stored key or takes a new one, reporting whether the
// provider now has a credential to try.
func settleSpeechKey(kind, provider string, hasKey bool) bool {
	if hasKey {
		// A saved key is used, not negotiated over. It was already accepted
		// once, and re-asking on every wizard run is how a setup flow starts
		// feeling like an interrogation.
		wizStep(shell.StateGood, provider, "using the API key you already saved")
		return true
	}
	// Before asking, look at what the user has already typed elsewhere. The AI
	// providers and the speech providers keep separate keystores on purpose
	// (speech works with no LLM configured), but they are not separate
	// ACCOUNTS: someone who pasted an OpenAI key on the provider screen was
	// being asked for the same key again three screens later, in the same
	// wizard, on the same run.
	if adoptAIKeyForSpeech(kind, provider) {
		return true
	}

	key := strings.TrimSpace(commands.AskLine(fmt.Sprintf("API key for %s", provider)))
	if key == "" {
		if hasKey {
			wizStep(shell.StateGood, provider, "nothing entered — keeping the saved key")
			return true
		}
		wizStep(shell.StateWarn, provider, "no key entered — it will fail until one is set")
		return false
	}
	if !confirmKeyForProvider(kind+"."+provider, key) {
		wizStep(shell.StateWarn, provider, "key not stored — it will fail until one is set")
		return hasKey
	}

	var err error
	if kind == "stt" {
		err = speech.SaveSTTKey(provider, key)
	} else {
		err = speech.SaveTTSKey(provider, key)
	}
	if err != nil {
		wizStep(shell.StateBad, provider, fmt.Sprintf("key storage failed: %v", err))
		return false
	}
	return true
}

// verifySpeechProvider probes the provider and says whether it can serve a
// request, so a bad credential or an absent sidecar is caught here rather than
// at the first spoken word.
// adoptAIKeyForSpeech copies a key the user already gave the AI side.
//
// Only for the same vendor name — this is credential REUSE, never credential
// guessing, so nothing is copied across providers. The key is stored in the
// speech keystore so later runs need no adoption, and the user is told it
// happened: silently spending a credential somewhere new is not something to
// do quietly.
//
// Args: kind ("stt"|"tts"), provider: the speech provider needing a key.
// Returns: whether a key was adopted.
// Complexity: O(1).
func adoptAIKeyForSpeech(kind, provider string) bool {
	if !ai.ProviderHasSavedKey(provider) {
		return false
	}
	key := strings.TrimSpace(ai.ProviderKey(provider))
	if key == "" {
		return false
	}

	var err error
	if kind == "stt" {
		err = speech.SaveSTTKey(provider, key)
	} else {
		err = speech.SaveTTSKey(provider, key)
	}
	if err != nil {
		// Not fatal: fall through and ask, rather than fail the wizard over a
		// convenience that did not work out.
		return false
	}
	wizStep(shell.StateGood, provider,
		"reusing the key you configured for the AI provider")
	return true
}

// verifiedThisRun records providers whose diagnosis was already printed during
// selection, so the closing summary can reference it instead of repeating it.
var verifiedThisRun = map[string]bool{}

func verifySpeechProvider(kind, provider string, requiresKey bool) bool {
	verifiedThisRun[provider] = true
	err := runCancellableProgressWithTimeout(
		"VERIFYING "+strings.ToUpper(provider),
		45*time.Second,
		func(ctx context.Context, progress func(string, int64, int64)) error {
			progress("VERIFYING "+strings.ToUpper(provider), 0, 0)
			reg := speech.Default()
			if reg == nil {
				return fmt.Errorf("speech engine not initialized")
			}
			if kind == "stt" {
				p, found := reg.STTProvider(provider)
				if !found {
					return fmt.Errorf("provider not registered")
				}
				return p.HealthCheck(ctx)
			}
			p, found := reg.TTSProvider(provider)
			if !found {
				return fmt.Errorf("provider not registered")
			}
			return p.HealthCheck(ctx)
		},
	)
	if err == nil {
		wizStep(shell.StateGood, provider, "verified")
		return true
	}

	// Name the likely cause rather than echoing a status code: a credential
	// problem and an absent sidecar need completely different actions.
	if requiresKey && isAuthFailure(err) {
		wizStep(shell.StateBad, provider, "rejected the API key")
		wizDetail("Check it at the provider's dashboard and re-run /blackbox setup.")
		return false
	}
	wizStep(shell.StateWarn, provider, "not answering yet")
	wizDetail(strings.TrimSpace(err.Error()))
	return false
}

// isAuthFailure reports whether an error is a rejected credential.
func isAuthFailure(err error) bool {
	if code, ok := providers.StatusCode(err); ok {
		return code == http.StatusUnauthorized || code == http.StatusForbidden ||
			code == http.StatusPaymentRequired
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "invalid api key") ||
		strings.Contains(lower, "missing api key")
}

// portOfEndpoint extracts the port from an endpoint URL (0 when absent).
func portOfEndpoint(endpoint string) int {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Port() == "" {
		return 0
	}
	n, err := strconv.Atoi(u.Port())
	if err != nil {
		return 0
	}
	return n
}

// sidecarSpec describes a local speech sidecar Helix can address.
type sidecarSpec struct {
	// Default is the upstream stock endpoint.
	Default string

	// ConfigKey is the /config key that repoints it.
	ConfigKey string

	// Launch renders the command that starts it on a given port.
	Launch func(port int) string
}

// sidecarSpecs is the table of local speech sidecars.
//
// Launch commands are DERIVED from the same argument builders Helix executes
// (voiceSidecars), never written out a second time. They had been duplicated,
// and duplicated tables drift: this one still said
// "whisper-server -m models/ggml-base.en.bin" — a relative path to a model
// Helix no longer defaults to — while the code actually ran
// "-m ~/.helix/whisper-models/ggml-small.en.bin". A user following the printed
// command would start a server pointed at nothing.
func sidecarSpecs() map[string]sidecarSpec {
	specs := map[string]sidecarSpec{
		"whisper-local": {Default: whisperDefaultEndpoint, ConfigKey: "stt-url"},
		"piper-local":   {Default: piperDefaultEndpoint, ConfigKey: "tts-url"},
		"kokoro-local":  {Default: kokoroDefaultEndpoint, ConfigKey: "tts-url"},
		"csm-local":     {Default: speech.CSMDefaultEndpoint, ConfigKey: "tts-url"},
	}

	runnable := voiceSidecars()
	for name, spec := range specs {
		if sc, ok := runnable[name]; ok {
			binary, args := sc.Binaries[0], sc.Args
			spec.Launch = func(port int) string {
				return binary + " " + strings.Join(args(port), " ")
			}
		} else {
			spec.Launch = func(int) string { return "" }
		}
		specs[name] = spec
	}
	return specs
}

// autoAssignSidecarPort resolves the endpoint a local sidecar will use, moving
// it off an occupied port WITHOUT asking.
//
// Helix does not launch these sidecars, so it cannot make the port change by
// itself — but it can make the decision, which is the part that was being handed
// back to the user. Offering a choice between "move the sidecar" and "move the
// brain" is a question with no wrong answer and no information the user has that
// Helix does not; it just costs them a decision. So Helix picks a port that is
// verifiably free, records it, and prints ONE command to run.
//
// The stock port is kept whenever it is free, so a sidecar launched with no flags
// still works. It only moves when something else genuinely holds the port —
// which on macOS is always true for piper's default 5000, where AirPlay Receiver
// lives.
//
// Returns the endpoint to store for this provider ("" = keep the default).
func autoAssignSidecarPort(kind, provider, configured string) string {
	spec, ok := sidecarSpecs()[provider]
	if !ok {
		return configured // cloud provider: no local port to manage
	}

	endpoint := strings.TrimSpace(configured)
	if endpoint == "" {
		endpoint = spec.Default
	}
	current := portOfEndpoint(endpoint)
	if current <= 0 || edge.PortAvailable(current) {
		// Free: nothing to move. The sidecar simply is not running yet.
		return configured
	}

	// The thing on that port may be THIS provider, already running and healthy.
	//
	// Without this check Helix read its own server as a squatter, moved to a new
	// port, and started a SECOND copy — two whisper-servers each holding a
	// 465 MB model, and the original orphaned. "Occupied" is not the question;
	// "occupied by something else" is.
	if sidecarAnswersAt(kind, provider, endpoint) {
		wizStep(shell.StateGood, provider,
			fmt.Sprintf("already running on port %d — keeping it", current))
		return configured
	}

	port, isPreferred := edge.FreePortAvoiding(provider, current, reservedSidecarPorts(provider))
	if isPreferred || port == current {
		return configured // nothing better found; leave the config alone
	}

	assigned := edge.ReplacePort(endpoint, port)
	held := fmt.Sprintf("port %d is already %s", current, edge.PortOccupant(current))
	if occupant := knownOccupant(current); occupant != "" {
		// Naming the usual culprit turns a puzzling collision into a known one.
		// On macOS this is nearly always AirPlay Receiver squatting on 5000.
		held += ", normally " + occupant + " on this platform"
	}
	wizStep(shell.StateWarn, provider, held)
	wizStep(shell.StateGood, provider,
		fmt.Sprintf("moved to port %d — verified free just now", port))
	fmt.Println(shell.StepCommand(spec.Launch(port)))

	// Persist immediately so the endpoint survives even if the wizard is
	// interrupted before its final save.
	//
	// The PROVIDER is set alongside the URL, and that is not incidental:
	// registerBuiltins only hands a configured BaseURL to the provider named as
	// active, so rebuilding with just the URL set left the adapter on its stock
	// endpoint. The wizard then verified the port it had moved away from and
	// printed advice for it — the reassignment appeared to have no effect.
	switch kind {
	case "stt":
		cfg.Speech.STT.Provider = provider
		cfg.Speech.STT.BaseURL = assigned
	case "tts":
		cfg.Speech.TTS.Provider = provider
		cfg.Speech.TTS.BaseURL = assigned
	}
	if err := cfg.SavePreferences(); err != nil {
		wizStep(shell.StateWarn, provider, fmt.Sprintf("could not save the new endpoint: %v", err))
	}
	if err := speech.Init(speechConfigFrom(cfg.Speech)); err != nil {
		wizStep(shell.StateWarn, provider, fmt.Sprintf("speech engine rebuild failed: %v", err))
	}
	return assigned
}

// knownOccupant names the service that habitually owns a port on this platform.
func knownOccupant(port int) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	switch port {
	case 5000, 7000:
		return "macOS AirPlay Receiver"
	}
	return ""
}

// sidecarAnswersAt reports whether the given provider is already serving at an
// endpoint.
//
// It rebuilds a throwaway adapter pointed at that address rather than asking the
// registry, because the registry's instance may still carry the OLD endpoint at
// this point in the wizard — which is precisely the confusion that produced
// duplicate servers.
func sidecarAnswersAt(kind, provider, endpoint string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch provider {
	case "whisper-local":
		return speech.NewWhisperLocalSTT("", endpoint).HealthCheck(ctx) == nil
	case "piper-local":
		return speech.NewPiperTTS(endpoint).HealthCheck(ctx) == nil
	case "kokoro-local":
		return speech.NewKokoroLocalTTS("", "", endpoint).HealthCheck(ctx) == nil
	}
	_ = kind
	return false
}
