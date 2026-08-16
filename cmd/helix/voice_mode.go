// cmd/helix/voice_mode.go
// Purpose: Voice/manual mode switching (BlackBox Phase 2, ADR-008) and the
// per-turn voice capture loop. Voice mode replaces the typed line with a
// record→transcribe cycle; every transcript flows through the SAME pipeline
// as typed input, stamped Channel=voice so the Voice Risk Policy applies.
// Mic failures degrade gracefully: one typed turn is offered rather than
// bricking the shell on mic-less machines.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"helix/internal/audio"
	"helix/internal/commands"
	"helix/internal/input"
	"helix/internal/speech"
	"helix/internal/utils"
	"helix/internal/wakeword"

	"github.com/fatih/color"
)

var (
	voiceModeActive bool
	voicePrompter   *VoicePrompter
	ttyPrompter     commands.Prompter
)

// initVoiceMode wires prompters and restores the persisted mode. Called once
// from main after speech.Init and agent construction.
func initVoiceMode() {
	ttyPrompter = commands.ActivePrompter()
	voicePrompter = NewVoicePrompter()

	if cfg.UserPrefs.VoiceMode {
		// Refuse to strand the user in voice mode without a recorder.
		if _, err := speech.DetectRecorder(); err == nil {
			enterVoiceMode(false)
		} else {
			color.Yellow("Voice mode skipped at startup: %v", err)
			cfg.UserPrefs.VoiceMode = false
			_ = cfg.SavePreferences()
		}
	}
}

func enterVoiceMode(persist bool) {
	voiceModeActive = true
	commands.SetPrompter(voicePrompter)
	if persist {
		cfg.UserPrefs.VoiceMode = true
		_ = cfg.SavePreferences()
	}
	audio.PlayAlert()
	color.Cyan("VOICE MODE — speak after the chime; say %q or type /manual to switch back.", "manual mode")
}

func exitVoiceMode(persist bool) {
	voiceModeActive = false
	if ttyPrompter != nil {
		commands.SetPrompter(ttyPrompter)
	}
	if persist {
		cfg.UserPrefs.VoiceMode = false
		_ = cfg.SavePreferences()
	}
	color.Cyan("TEXT MODE — keyboard input restored.")
}

// handleVoiceCommand: /voice [off]
func handleVoiceCommand(raw string) {
	arg := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, "/voice")))
	if arg == "off" {
		exitVoiceMode(true)
		return
	}
	if voiceModeActive {
		fmt.Println("Already in voice mode.")
		return
	}
	if _, err := speech.DetectRecorder(); err != nil {
		color.Red("Cannot enter voice mode: %v", err)
		return
	}
	if reg := speech.Default(); reg == nil || len(reg.STTChain()) == 0 {
		color.Yellow("No STT provider configured — run /voice-setup first. Entering voice mode anyway is refused.")
		return
	}
	enterVoiceMode(true)
}

// handleManualCommand: /manual — instant fallback to typing (safety valve).
func handleManualCommand() {
	if !voiceModeActive {
		fmt.Println("Already in text mode.")
		return
	}
	exitVoiceMode(true)
}

// voiceTurn performs one capture→transcribe cycle and returns the stamped
// input event. The transcript is echoed like a typed line would be. The
// capture registers with the interrupt manager so Ctrl+C cancels recording
// instead of killing Helix.
func voiceTurn() (input.InputEvent, error) {
	audio.PlayAlert() // ready cue

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	unreg := utils.RegisterOperation(cancel)
	defer unreg()
	defer cancel()

	clip, err := speech.RecordClip(ctx, speech.CaptureOptions{MaxDuration: 12 * time.Second})
	if err != nil {
		return input.InputEvent{}, fmt.Errorf("capture: %w", err)
	}

	tctx, tcancel := context.WithTimeout(ctx, 60*time.Second)
	defer tcancel()
	transcript, err := speech.Transcribe(tctx, clip)
	if err != nil {
		return input.InputEvent{}, fmt.Errorf("transcribe: %w", err)
	}
	text := strings.TrimSpace(transcript.Text)
	if text == "" {
		return input.InputEvent{}, fmt.Errorf("empty transcript")
	}

	conf := fmt.Sprintf(", confidence %.2f", transcript.Confidence)
	if transcript.Confidence <= 0 {
		conf = ""
	}
	fmt.Printf("[heard] %q (via %s%s)\n", text, transcript.Provider, conf)

	// Hands-free kill switches (ADR-005 wake controls): recognized before
	// dispatch. The /manual slash form also works when spoken as "/manual".
	if isVoiceKillPhrase(text) {
		exitVoiceMode(true)
		return input.InputEvent{}, errVoiceStopped
	}

	return input.InputEvent{
		Text:    text,
		Channel: input.ChannelVoice,
		Meta: map[string]any{
			"stt_provider":   transcript.Provider,
			"stt_confidence": transcript.Confidence,
			"stt_language":   transcript.Language,
		},
	}, nil
}

// errVoiceStopped signals a kill phrase ended voice mode (the mode line
// already announced it; the main loop treats this as a quiet continue).
var errVoiceStopped = fmt.Errorf("voice stopped by kill phrase")

// isVoiceKillPhrase matches the documented sleep/stop phrases plus the
// natural "manual mode" utterance.
func isVoiceKillPhrase(text string) bool {
	t := strings.ToLower(strings.TrimRight(strings.TrimSpace(text), ".!?"))
	switch t {
	case "stop listening", "go to sleep", "sleep", "manual mode",
		"switch to manual mode", "i want to type", "stop voice":
		return true
	}
	return false
}

// wakeIdleWindow is the ADR-005 §5 lockout window: between turns NOTHING is
// transcribed; only wake scoring runs, for at most this long before the
// shell falls back to push-to-talk turns.
const wakeIdleWindow = 60 * time.Second

// wakeListenUntilArmed blocks in chunk-scanning wake detection. Returns
// true when a wake event fires (caller runs another voice turn), false on
// window expiry, disabled config, or scanner failure (fall back to
// push-to-talk turns).
func wakeListenUntilArmed() bool {
	if speech.Default() == nil || !cfg.Speech.WakeWord.Enabled {
		return false
	}
	if _, err := speech.DetectRecorder(); err != nil {
		return false
	}

	preset := wakeword.Preset(cfg.Speech.WakeWord.SensitivityPreset)
	var detector wakeword.Detector
	switch cfg.Speech.WakeWord.Engine {
	case "sidecar":
		detector = wakeword.NewSidecarDetector(cfg.Speech.WakeWord.SidecarURL,
			cfg.Speech.WakeWord.Phrase, preset)
	default: // "energy" — the everywhere-works default (ADR-002 honesty)
		detector = wakeword.NewEnergyDetector(preset)
	}

	chunkMs := cfg.Speech.WakeWord.ChunkMs
	if chunkMs <= 0 {
		chunkMs = 1500
	}
	svc, err := wakeword.NewService(
		wakeword.NewSoXScanner(time.Duration(chunkMs)*time.Millisecond, 16000),
		detector,
		wakeword.Config{
			Phrase:   cfg.Speech.WakeWord.Phrase,
			Cooldown: time.Duration(cfg.Speech.WakeWord.CooldownS) * time.Second,
			OnError:  func(error) {},
		})
	if err != nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), wakeIdleWindow)
	unreg := utils.RegisterOperation(cancel)
	defer unreg()
	defer cancel()

	events, err := svc.Start(ctx)
	if err != nil {
		return false
	}
	defer func() { _ = svc.Stop() }()

	fmt.Println("[wake] listening for the wake phrase (nothing is transcribed until it fires)...")
	select {
	case ev := <-events:
		logWakeEvent(ev)
		audio.PlayAlert()
		return true
	case <-ctx.Done():
		return false
	}
}

// logWakeEvent appends one wake event to the local metrics journal
// (~/.helix/metrics/wake.jsonl, local only, never transmitted).
func logWakeEvent(ev wakeword.WakeEvent) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".helix", "metrics")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "wake.jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	line, err := json.Marshal(map[string]any{
		"ts":     ev.DetectedAt.UTC().Format(time.RFC3339),
		"score":  ev.Score,
		"phrase": ev.Phrase,
	})
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
}
