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
	"fmt"
	"strings"
	"time"

	"helix/internal/audio"
	"helix/internal/commands"
	"helix/internal/input"
	"helix/internal/speech"
	"helix/internal/utils"

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
