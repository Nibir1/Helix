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
			if key == "" {
				color.Yellow("No key entered — %s will fail until a key is set.", entry.Provider)
			} else if err := speech.SaveSTTKey(entry.Provider, key); err != nil {
				color.Red("Key storage failed: %v", err)
			}
		}
		if fallback := strings.TrimSpace(commands.AskLine("Fallback STT provider (blank for none)")); fallback != "" {
			if validName(sttNames, fallback) {
				sttCfg.Fallbacks = []string{fallback}
			} else {
				color.Yellow("Unknown fallback %q ignored (registered: %s)", fallback, strings.Join(sttNames, ", "))
			}
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
		if entry.RequiresKey {
			key := strings.TrimSpace(commands.AskLine(fmt.Sprintf("API key for %s", entry.Provider)))
			if key == "" {
				color.Yellow("No key entered — %s will fail until a key is set.", entry.Provider)
			} else if err := speech.SaveTTSKey(entry.Provider, key); err != nil {
				color.Red("Key storage failed: %v", err)
			}
		}
		if voice := strings.TrimSpace(commands.AskLine("Voice id/name (blank for provider default)")); voice != "" {
			ttsCfg.Voice = voice
		}
		if fallback := strings.TrimSpace(commands.AskLine("Fallback TTS provider (blank for none)")); fallback != "" {
			if validName(ttsNames, fallback) {
				ttsCfg.Fallbacks = []string{fallback}
			} else {
				color.Yellow("Unknown fallback %q ignored (registered: %s)", fallback, strings.Join(ttsNames, ", "))
			}
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
		fmt.Printf("Last TTS latency: %dms (budget %dms)\n",
			report.TTSLastLatencyMs, report.TTSFirstByteBudgetMs)
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
