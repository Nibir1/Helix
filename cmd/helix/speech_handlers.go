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

	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/speech"

	"helix/internal/providers"
	"net/http"

	"github.com/fatih/color"
	"helix/internal/edge"
	"net/url"
)

// speechConfigFrom maps the persisted config section onto the speech package
// selection.
func speechConfigFrom(sc config.SpeechConfig) speech.Config {
	return speech.Config{
		STT: speech.STTConfig{
			Provider:      sc.STT.Provider,
			Model:         sc.STT.Model,
			BaseURL:       sc.STT.BaseURL,
			Fallbacks:     sc.STT.Fallbacks,
			StreamChunkMs: sc.STT.StreamChunkMs,
		},
		TTS: speech.TTSConfig{
			Provider:    sc.TTS.Provider,
			Model:       sc.TTS.Model,
			Voice:       sc.TTS.Voice,
			BaseURL:     sc.TTS.BaseURL,
			Fallbacks:   sc.TTS.Fallbacks,
			FirstByteMs: sc.TTS.FirstByteMs,
		},
	}
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
func askVoiceID(provider string) string {
	if voices, ok := knownVoices[provider]; ok {
		color.Cyan("  Voices for %s: %s", provider, strings.Join(voices, ", "))
	}
	voice := strings.TrimSpace(commands.AskLine(
		"Voice id/name (blank for provider default — NOT the model name)"))
	if voice == "" {
		return ""
	}

	// Catch the exact mistake the old prompt invited, while it is still free
	// to correct, rather than at the first paid synthesis.
	if valid, ok := knownVoices[provider]; ok && !validName(valid, voice) &&
		!strings.HasPrefix(valid[0], "<") {
		color.Yellow("%q is not a known %s voice (valid: %s).",
			voice, provider, strings.Join(valid, ", "))
		if !commands.AskForConfirmation("Use it anyway?") {
			color.Cyan("Using the provider default voice.")
			return ""
		}
	}
	return voice
}

// askFallback prompts for a fallback PROVIDER, listing the valid names up
// front. The old prompt named them only in the rejection message, after the
// answer was already wrong.
func askFallback(kind string, names []string, primary string) []string {
	options := make([]string, 0, len(names))
	for _, n := range names {
		if n != primary {
			options = append(options, n)
		}
	}
	if len(options) == 0 {
		return nil
	}

	color.Cyan("  Fallback %s providers: %s", kind, strings.Join(options, ", "))
	fallback := strings.TrimSpace(commands.AskLine(
		fmt.Sprintf("Fallback %s provider (blank for none — a provider name, not a model)", kind)))
	if fallback == "" {
		return nil
	}
	if !validName(names, fallback) {
		color.Yellow("Unknown fallback %q ignored (valid: %s)", fallback, strings.Join(options, ", "))
		return nil
	}
	if fallback == primary {
		color.Yellow("Fallback %q is the same as the primary — ignored.", fallback)
		return nil
	}
	return []string{fallback}
}

// handleVoiceSetup runs the STT/TTS selection wizard with live pricing.
func handleVoiceSetup() {
	color.Cyan("⚡ HELIX VOICE LINK CONFIGURATION")

	catalog, err := speech.LoadMergedCatalog()
	if err != nil {
		color.Yellow("Pricing catalog notice: %v", err)
	}

	reg := speech.Default()
	if reg == nil {
		color.Red("Speech engine not initialized.")
		return
	}

	// ---- STT selection ----
	sttNames := reg.STTNames()
	sttRows := filterCatalog(catalog, "stt", sttNames)
	printSpeechTable("SPEECH-TO-TEXT (STT) PROVIDERS", sttRows)

	choice := askNumber("Select STT provider number (blank to skip)", len(sttRows))
	var sttCfg config.SpeechSTTConfig
	if choice > 0 {
		entry := sttRows[choice-1]
		sttCfg.Provider = entry.Provider
		sttCfg.Model = entry.APIModel()
		prepareSpeechProvider("stt", entry.Provider)
		sttCfg.Fallbacks = askFallback("STT", sttNames, entry.Provider)
		// A fallback with no key is not a fallback. Each one gets the same
		// treatment as the primary — previously they were selected by name and
		// never asked about, so the chain looked deeper than it was.
		for _, name := range sttCfg.Fallbacks {
			prepareSpeechProvider("stt", name)
		}
		color.Green("STT: %s", sttCfg.Provider)
	}

	// ---- TTS selection ----
	ttsNames := reg.TTSNames()
	ttsRows := filterCatalog(catalog, "tts", ttsNames)
	printSpeechTable("TEXT-TO-SPEECH (TTS) PROVIDERS", ttsRows)

	choice = askNumber("Select TTS provider number (blank to skip)", len(ttsRows))
	var ttsCfg config.SpeechTTSConfig
	if choice > 0 {
		entry := ttsRows[choice-1]
		ttsCfg.Provider = entry.Provider
		ttsCfg.Model = entry.APIModel()
		prepareSpeechProvider("tts", entry.Provider)
		ttsCfg.Voice = askVoiceID(entry.Provider)
		ttsCfg.Fallbacks = askFallback("TTS", ttsNames, entry.Provider)
		for _, name := range ttsCfg.Fallbacks {
			prepareSpeechProvider("tts", name)
		}
		color.Green("TTS: %s", ttsCfg.Provider)
	}

	if sttCfg.Provider == "" && ttsCfg.Provider == "" {
		color.Yellow("Voice setup skipped. Re-run /voice-setup anytime.")
		return
	}

	// Field-wise merge: the wizard only owns the sections the user actually
	// selected. WakeWord config and per-section tuning (StreamChunkMs,
	// FirstByteMs) must survive a re-run — a whole-struct replace here used
	// to silently disable /wake and wipe custom phrases.
	if sttCfg.Provider != "" {
		sttCfg.StreamChunkMs = cfg.Speech.STT.StreamChunkMs
		cfg.Speech.STT = sttCfg
	}
	if ttsCfg.Provider != "" {
		ttsCfg.FirstByteMs = cfg.Speech.TTS.FirstByteMs
		cfg.Speech.TTS = ttsCfg
	}
	if err := cfg.SavePreferences(); err != nil {
		color.Red("Failed to save preferences: %v", err)
	}
	if err := speech.Init(speechConfigFrom(cfg.Speech)); err != nil {
		color.Red("Speech engine re-init failed: %v", err)
		return
	}

	color.Green("Voice link configured.")

	// Verify BEFORE claiming success. The wizard used to print "configured" and
	// stop, so a selection that could never work — a sidecar that is not running,
	// or a port owned by something else — only surfaced later as a failed /say,
	// by which point the wizard looked like it had succeeded.
	verifySpeechSelection()

	if rec, rerr := speech.DetectRecorder(); rerr != nil {
		color.Yellow("Microphone note: %v", rerr)
	} else {
		fmt.Printf("Recorder detected: %s\n", rec)
	}
	fmt.Println("Try: /say Voice link online    |    /listen 5")
}

// verifySpeechSelection probes the newly selected chain and reports what it found.
func verifySpeechSelection() {
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
			color.Red("%s is in your chain but not answering.", row.Name)
			for _, line := range providerDetailLines(row) {
				color.Yellow("  %s", line)
			}
		}
	}

	if problems == 0 {
		color.Green("Chain verified: every selected provider answered.")
		return
	}
	suggestFreeSidecarPorts()
	color.Yellow("%d selected provider(s) cannot serve a request yet.", problems)
	color.Yellow("Fix the above, then re-check with /voice-status — no need to re-run the wizard.")
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
	fmt.Println()
	color.Cyan("%s", title)
	fmt.Printf("  %-3s %-14s %-24s %-16s %-12s %-10s %-6s %-6s\n",
		"#", "Provider", "Model", "Price", "Est $/mo@2h/d", "Latency", "Key", "Local")
	for i, e := range rows {
		badge := ""
		if e.Recommended {
			badge = "  ★ recommended"
		}
		fmt.Printf("  %-3d %-14s %-24s %-16s %-12.2f %-10s %-6t %-6t%s\n",
			i+1, e.Provider, truncStr(e.Model, 24), speech.FormatUnit(e),
			speech.EstimateMonthlyCost(e, 2), e.Latency, e.RequiresKey, e.Local, badge)
	}
	if len(rows) == 0 {
		fmt.Println("  (no registered providers)")
	}
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
		fmt.Println("Usage: /say <text>")
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
	fmt.Printf("[voice] %s\n", truncStr(text, 60))
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
	fmt.Printf("spoken (%s, first audio %dms)\n", path, speech.LastSynthesizeLatencyMs())
}

// handleTTSCommand toggles automatic spoken responses (Phase 2 gate).
func handleTTSCommand(c cmdArgs) {
	switch c.Lower() {
	case "on":
		speech.SetTTSEnabled(true)
		color.Green("Automatic spoken responses enabled.")
	case "off":
		speech.SetTTSEnabled(false)
		color.Green("Automatic spoken responses disabled (/say still speaks).")
	default:
		state := "off"
		if speech.TTSEnabled() {
			state = "on"
		}
		fmt.Printf("Usage: /tts <on|off>   (currently: %s)\n", state)
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
	fmt.Printf("[heard] %q (via %s%s)\n", transcript.Text, transcript.Provider, conf)
}

// handleVoiceStatus prints chains, provider health, and recorder state.
func handleVoiceStatus() {
	color.Cyan("⚡ HELIX VOICE STATUS")

	// A local sidecar sharing its port with the LLM runtime is the most common
	// reason local STT/TTS "does not work", and it is invisible in the health
	// rows below — whichever service owns the port answers, so the probe sees a
	// live socket either way.
	reportEndpointConflicts()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report := speech.Status(ctx)

	fmt.Printf("STT chain: %s\n", chainOrNone(report.STTChain))
	fmt.Printf("TTS chain: %s\n", chainOrNone(report.TTSChain))
	if speech.TTSEnabled() {
		fmt.Println("Automatic spoken responses: on")
	} else {
		fmt.Println("Automatic spoken responses: off")
	}
	if report.Recorder != "" {
		fmt.Printf("Recorder: %s\n", report.Recorder)
	} else {
		color.Yellow("Recorder: %s", report.RecorderErr)
	}
	if report.TTSLastLatencyMs > 0 {
		path := "buffered — full synthesis before playback"
		if report.TTSLastStreamed {
			path = "streamed"
		}
		fmt.Printf("Last TTS time-to-first-audio: %dms (budget %dms) [%s]\n",
			report.TTSLastLatencyMs, report.TTSFirstByteBudgetMs, path)
	}

	printStatusRows("STT PROVIDERS", report.STTStatus)
	printStatusRows("TTS PROVIDERS", report.TTSStatus)
	printVoiceVocabulary()
}

// printVoiceVocabulary lists what can be reached by speaking.
//
// Without this the spoken command surface is undiscoverable: there is no menu to
// read when your hands are busy, and guessing at phrasing is a bad experience.
func printVoiceVocabulary() {
	fmt.Println()
	color.Cyan("SPOKEN COMMANDS")
	for _, line := range voiceCommandVocabulary() {
		fmt.Println("  " + line)
	}
	fmt.Println()
	color.Cyan("  Also: \"slash <command name>\" reaches any voice-enabled command directly,")
	color.Cyan("  e.g. \"slash provider status\" or \"slash knowledge status\".")
	color.Yellow("  Destructive commands are unreachable by voice by design — /purge, /commit,")
	color.Yellow("  /permissions, /config, /hooks and the RAG resets must be typed.")
}

func chainOrNone(chain []string) string {
	if len(chain) == 0 {
		return "(none — run /voice-setup)"
	}
	return strings.Join(chain, " → ")
}

func printStatusRows(title string, rows []speech.ProviderStatusRow) {
	fmt.Println()
	color.Cyan("%s", title)
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

// statusDetailWidth is the wrap width for the indented detail lines — narrow
// enough to survive an 80-column terminal with the indent.
const statusDetailWidth = 72

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
		return []string{"  (no registered providers)"}
	}

	out := []string{fmt.Sprintf("  %-*s %-*s %-*s %-*s %s",
		statusNameWidth, "PROVIDER", statusDisplayWidth, "NAME",
		statusStateWidth, "STATE", statusKeyWidth, "KEY", "WHERE")}

	for _, r := range rows {
		out = append(out, fmt.Sprintf("  %-*s %-*s %-*s %-*s %s",
			statusNameWidth, truncStr(r.Name, statusNameWidth),
			statusDisplayWidth, truncStr(r.Display, statusDisplayWidth),
			statusStateWidth, providerState(r),
			statusKeyWidth, providerKeyState(r),
			locality(r.Local)))
		for _, detail := range providerDetailLines(r) {
			out = append(out, statusDetailIndent+detail)
		}
	}
	return out
}

// providerState reduces a row to one state word.
//
// "standby" and "down" are genuinely different and were previously conflated:
// an out-of-chain provider is never probed, so its Healthy=false means "not
// being used", not "broken".
func providerState(r speech.ProviderStatusRow) string {
	switch {
	case r.Healthy:
		return "healthy"
	case !r.InChain:
		return "standby"
	default:
		return "down"
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
		"  python3 -m piper.http_server -m en_US-lessac-medium.onnx --port 5001",
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
	verifySpeechProvider(kind, provider, requiresKey)
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
		if commands.AskForConfirmation(fmt.Sprintf("%s: use the saved API key?", provider)) {
			return true
		}
	}

	key := strings.TrimSpace(commands.AskLine(fmt.Sprintf("API key for %s", provider)))
	if key == "" {
		if hasKey {
			color.Yellow("Nothing entered — keeping the saved key for %s.", provider)
			return true
		}
		color.Yellow("No key entered — %s will fail until a key is set.", provider)
		return false
	}
	if !confirmKeyForProvider(kind+"."+provider, key) {
		color.Yellow("Key not stored — %s will fail until a key is set.", provider)
		return hasKey
	}

	var err error
	if kind == "stt" {
		err = speech.SaveSTTKey(provider, key)
	} else {
		err = speech.SaveTTSKey(provider, key)
	}
	if err != nil {
		color.Red("Key storage failed: %v", err)
		return false
	}
	return true
}

// verifySpeechProvider probes the provider and says whether it can serve a
// request, so a bad credential or an absent sidecar is caught here rather than
// at the first spoken word.
func verifySpeechProvider(kind, provider string, requiresKey bool) {
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
		color.Green("  %s verified.", provider)
		return
	}

	// Name the likely cause rather than echoing a status code: a credential
	// problem and an absent sidecar need completely different actions.
	if requiresKey && isAuthFailure(err) {
		color.Red("  %s rejected the API key.", provider)
		color.Yellow("  Check it at the provider's dashboard and re-run /voice-setup.")
		return
	}
	color.Yellow("  %s could not be verified yet:", provider)
	for _, line := range strings.Split(strings.TrimSpace(err.Error()), "\n") {
		color.Yellow("    %s", strings.TrimRight(line, " \t"))
	}
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

// suggestFreeSidecarPorts offers a port that is actually free when a local
// sidecar's configured one is taken.
//
// The stock defaults collide by construction: llama.cpp and whisper.cpp both
// claim 8080, and on macOS AirPlay Receiver holds 5000 before anything else
// starts. Rather than telling the user to "pick a free port", Helix binds to
// find one and hands over the exact launch command and the /config line to
// match. The choice is deterministic per service, so re-running setup suggests
// the same number instead of asking for a relaunch on a new one each time.
func suggestFreeSidecarPorts() {
	type sidecar struct {
		service   string
		configKey string
		endpoint  string
		launch    func(port int) string
	}

	sidecars := []sidecar{
		{
			service: "whisper-local", configKey: "stt-url", endpoint: localSTTURL(),
			launch: func(port int) string {
				return fmt.Sprintf(
					"whisper-server -m models/ggml-base.en.bin --port %d", port)
			},
		},
		{
			service: "piper-local", configKey: "tts-url",
			endpoint: localTTSURL("piper-local", piperDefaultEndpoint),
			launch: func(port int) string {
				return fmt.Sprintf(
					"python3 -m piper.http_server -m en_US-lessac-medium.onnx --port %d", port)
			},
		},
	}

	var printed bool
	for _, sc := range sidecars {
		if !sidecarSelected(sc.service) {
			continue
		}
		current := portOfEndpoint(sc.endpoint)
		if current <= 0 || edge.PortAvailable(current) {
			continue // free: nothing to move, the sidecar simply is not running
		}

		port, isPreferred := edge.FreePortFor(sc.service, current)
		if isPreferred || port == current {
			continue
		}
		if !printed {
			fmt.Println()
			color.Cyan("Port %d is occupied, so that sidecar cannot bind it.", current)
			printed = true
		}
		color.Cyan("  %s — use port %d instead (checked free just now):", sc.service, port)
		color.Cyan("    %s", sc.launch(port))
		color.Cyan("    /config %s %s", sc.configKey, edge.ReplacePort(sc.endpoint, port))
	}
}

// sidecarSelected reports whether a local provider is in the configured chain.
func sidecarSelected(name string) bool {
	return cfg.Speech.STT.Provider == name || containsFold(cfg.Speech.STT.Fallbacks, name) ||
		cfg.Speech.TTS.Provider == name || containsFold(cfg.Speech.TTS.Fallbacks, name)
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
