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
func handleEyesCommand(c cmdArgs) {
	switch c.Lower() {
	case "on", "enable":
		if !visionAvailable() {
			for _, line := range visionUnavailableHelp() {
				color.Yellow("%s", line)
			}
			return
		}
		setVisionEnabled(true)
	case "off", "disable":
		setVisionEnabled(false)
	case "look", "describe", "see", "what do you see":
		describeWhatIsSeen("")
	case "", "status":
		state := "off"
		if cfg.Vision.Enabled {
			state = "on"
		}
		fmt.Printf("Eyes: %s (frames per turn: %d)\n", state, visionMaxFrames())
		// Say whether it CAN be turned on. Reporting only the toggle meant
		// /eyes status looked fine on a setup where /eyes on could never succeed
		// — the same gap /wake status had against its own banner.
		if visionAvailable() {
			color.Green("Vision model: %s — ready", visionRouteDescription())
		} else {
			color.Yellow("Vision model: %s — cannot see; /eyes on will refuse", visionRouteDescription())
		}
	default:
		// "/eyes look <question>" — anything after the verb is the question.
		if sub := c.Sub(); sub == "look" || sub == "describe" || sub == "see" {
			describeWhatIsSeen(c.From(1))
			return
		}
		color.Yellow("Usage: /eyes <on|off|status|look [question]>")
	}
}

// describeWhatIsSeen captures one frame and answers a question about it.
//
// This is what makes the camera reachable by keyboard. The conversational vision
// path only fires on a deictic utterance ("what is THIS?"), and it used to
// additionally require the voice channel — so a typed session could turn /eyes
// on and then have no way whatsoever to use it.
func describeWhatIsSeen(question string) {
	if agentCore == nil {
		color.Red("The agent is not available in this session.")
		return
	}
	if !cfg.Vision.Enabled {
		color.Yellow("Eyes are off. Run /eyes on first.")
		return
	}
	if !visionAvailable() {
		for _, line := range visionUnavailableHelp() {
			color.Yellow("%s", line)
		}
		return
	}
	// Say what is about to happen before the shutter: a camera that activates
	// without a word is exactly the behavior the /eyes opt-in exists to avoid.
	color.Cyan("Capturing one frame (memory only) via %s…", visionRouteDescription())
	if err := agentCore.DescribeFrame(question); err != nil {
		color.Red("Vision failed: %v", err)
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

// visionRouteDescription names the provider/model that vision would actually
// use, so the user can tell the dedicated-provider route from the active-chat
// route at a glance.
func visionRouteDescription() string {
	if cfg.Vision.Provider != "" {
		return fmt.Sprintf("%s (vision.provider)", cfg.Vision.Provider)
	}
	provider, model := ai.ActiveProviderName(), ai.ActiveModel()
	if provider == "" {
		return "no chat provider selected"
	}
	if model == "" {
		return fmt.Sprintf("%s (no model selected)", provider)
	}
	return fmt.Sprintf("%s / %s (active chat provider)", provider, model)
}

// visionUnavailableHelp explains a refused /eyes on and how to fix it.
//
// The old single line — "No vision-capable model is configured (active model or
// vision.provider) — set one first." — is accurate and useless: it names neither
// which model was rejected, nor which of the nine registered providers would
// qualify, nor where the setting lives. Helix knows all three.
//
// Args: none.
// Returns: the lines to print, in order.
// Complexity: O(providers).
func visionUnavailableHelp() []string {
	lines := []string{
		fmt.Sprintf("Camera vision needs a multimodal model. %s cannot process images.",
			visionRouteDescription()),
	}

	capable := ai.VisionCapableProviders()
	if len(capable) == 0 {
		// Naming an empty list would be worse than saying so plainly.
		return append(lines,
			"None of the registered providers offers a vision-capable default model.",
			"Switch to a multimodal model with /provider use <name> then /model <id>",
			"(e.g. gpt-4o, a claude-* model, or an Ollama llava/gemma3 build).")
	}
	return append(lines,
		"Vision-capable providers registered here: "+strings.Join(capable, ", "),
		"Either select one for chat:      /provider use <name>",
		"or route ONLY frames to one by setting \"provider\" under \"vision\"",
		"in ~/.helix/config.json — chat stays on your current model.")
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
