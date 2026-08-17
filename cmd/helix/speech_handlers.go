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

	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/speech"

	"helix/internal/audio"

	"github.com/fatih/color"
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
		if entry.RequiresKey {
			key := strings.TrimSpace(commands.AskLine(fmt.Sprintf("API key for %s", entry.Provider)))
			switch {
			case key == "":
				color.Yellow("No key entered — %s will fail until a key is set.", entry.Provider)
			case !confirmKeyForProvider("stt."+entry.Provider, key):
				color.Yellow("Key not stored — %s will fail until a key is set.", entry.Provider)
			default:
				if err := speech.SaveSTTKey(entry.Provider, key); err != nil {
					color.Red("Key storage failed: %v", err)
				}
			}
		}
		sttCfg.Fallbacks = askFallback("STT", sttNames, entry.Provider)
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
		if entry.RequiresKey {
			key := strings.TrimSpace(commands.AskLine(fmt.Sprintf("API key for %s", entry.Provider)))
			switch {
			case key == "":
				color.Yellow("No key entered — %s will fail until a key is set.", entry.Provider)
			case !confirmKeyForProvider("tts."+entry.Provider, key):
				color.Yellow("Key not stored — %s will fail until a key is set.", entry.Provider)
			default:
				if err := speech.SaveTTSKey(entry.Provider, key); err != nil {
					color.Red("Key storage failed: %v", err)
				}
			}
		}
		ttsCfg.Voice = askVoiceID(entry.Provider)
		ttsCfg.Fallbacks = askFallback("TTS", ttsNames, entry.Provider)
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
	fmt.Println("Try: /say Voice link online    |    /listen 5")
	if rec, rerr := speech.DetectRecorder(); rerr != nil {
		color.Yellow("Microphone note: %v", rerr)
	} else {
		fmt.Printf("Recorder detected: %s\n", rec)
	}
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

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
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
func handleSayCommand(input string) {
	text := strings.TrimSpace(strings.TrimPrefix(input, "/say"))
	if text == "" {
		fmt.Println("Usage: /say <text>")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt_, err := speech.Synthesize(ctx, text)
	if err != nil {
		color.Red("Speech synthesis failed: %v", err)
		return
	}

	fmt.Printf("[voice] %s (%s, %d bytes)\n", truncStr(text, 60), fmt_.Kind, len(fmt_.Bytes))
	if err := audio.PlaySpeech(audio.SpeechFormat{
		Kind:       string(fmt_.Kind),
		SampleRate: fmt_.SampleRate,
		Channels:   fmt_.Channels,
		Data:       fmt_.Bytes,
	}, 1.0); err != nil {
		color.Yellow("Audio output: %v (synthesis succeeded)", err)
		return
	}
	fmt.Println("spoken.")
}

// handleTTSCommand toggles automatic spoken responses (Phase 2 gate).
func handleTTSCommand(input string) {
	arg := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(input, "/tts")))
	switch arg {
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
func handleListenCommand(input string) {
	seconds := 8
	if fields := strings.Fields(input); len(fields) > 1 {
		if n, err := strconv.Atoi(fields[1]); err == nil && n > 0 && n <= 60 {
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
	for _, r := range rows {
		state := "standby"
		switch {
		case r.Healthy:
			state = "healthy"
		case r.HealthDetail != "" && r.HealthDetail != "standby":
			state = truncStr(r.HealthDetail, 48)
		}
		key := "-"
		if r.HasKey {
			key = "key"
		}
		fmt.Printf("  %-14s %-24s %-8s %-6s %s\n", r.Name, r.Display, state, key, locality(r.Local))
	}
}

func locality(local bool) string {
	if local {
		return "local"
	}
	return "cloud"
}
