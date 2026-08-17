// cmd/helix/eyes.go
// Purpose: BlackBox Phase 5 opt-in camera perception UX — /eyes on|off|status,
// the "turn off your eyes" spoken privacy kill switch, and the metadata-only
// frame journal. Threat V4 is load-bearing: pixel bytes never reach disk, so
// the journal records only kind/provider/count/timestamp.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"helix/internal/ai"

	"github.com/fatih/color"
)

// handleEyesCommand implements /eyes <on|off|status>.
func handleEyesCommand(raw string) {
	arg := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, "/eyes")))
	switch arg {
	case "on", "enable":
		if !visionAvailable() {
			color.Yellow("No vision-capable model is configured (active model or vision.provider) — set one first.")
			return
		}
		setVisionEnabled(true)
	case "off", "disable":
		setVisionEnabled(false)
	case "", "status":
		state := "off"
		if cfg.Vision.Enabled {
			state = "on"
		}
		fmt.Printf("Eyes: %s (frames per turn: %d)\n", state, visionMaxFrames())
	default:
		color.Yellow("Usage: /eyes <on|off|status>")
	}
}

func visionMaxFrames() int {
	if cfg.Vision.MaxFramesPerTurn <= 0 {
		return 1
	}
	return cfg.Vision.MaxFramesPerTurn
}

// visionAvailable reports whether a vision path exists: the dedicated
// vision.provider if configured, else the active chat provider.
func visionAvailable() bool {
	if cfg.Vision.Provider != "" {
		return ai.ProviderVisionCapable(cfg.Vision.Provider)
	}
	return ai.VisionCapable()
}

// setVisionEnabled flips the opt-in and confirms vocally + in the journal.
// Deactivation is immediate and never gated on hardware (privacy, threat V4).
func setVisionEnabled(on bool) {
	cfg.Vision.Enabled = on
	_ = cfg.SavePreferences()
	if on {
		color.Green("Eyes ENABLED — frames are captured in memory only, never written to disk.")
		if agentCore != nil && agentCore.OnSpeak != nil {
			agentCore.OnSpeak("Eyes on.")
		}
		journalVisionEvent("enabled", "", 0)
	} else {
		color.Yellow("Eyes DISABLED — camera perception is off.")
		if agentCore != nil && agentCore.OnSpeak != nil {
			agentCore.OnSpeak("Eyes off.")
		}
		journalVisionEvent("disabled", "", 0)
	}
}

// isEyesOffPhrase matches the spoken privacy kill switch ("turn off your eyes"
// = /eyes off, without leaving voice mode).
func isEyesOffPhrase(text string) bool {
	t := strings.ToLower(strings.TrimRight(strings.TrimSpace(text), ".!?"))
	switch t {
	case "turn off your eyes", "eyes off", "turn your eyes off", "disable your eyes":
		return true
	}
	return false
}

// journalVisionEvent appends metadata-only frame events to
// ~/.helix/journal/vision.jsonl (0600). Provider + count + timestamp only —
// pixel bytes never reach disk (threat V4).
func journalVisionEvent(kind, provider string, count int) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".helix", "journal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "vision.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	line, err := json.Marshal(map[string]any{
		"ts":       time.Now().UTC().Format(time.RFC3339),
		"kind":     kind,     // enabled | disabled | frame
		"provider": provider, // vision LLM (frame events only)
		"count":    count,    // frames in the batch (frame events only)
	})
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
}
