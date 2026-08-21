// cmd/helix/local_endpoints.go
// Purpose: collect the local sidecar endpoints this session is configured to
// use, so /doctor and /voice-status can name a port collision instead of
// leaving it to be discovered as a 404.
//
// The stock ports genuinely overlap: llama-server defaults to 8080 and
// whisper.cpp's server also defaults to 8080. Helix keeps both upstream
// defaults — changing one would break the other's out-of-the-box launch — and
// reports the clash when a configuration actually has both.
package main

import (
	"strings"

	"helix/internal/ai"
	"helix/internal/edge"
	"helix/internal/providers/llamacpp"

	"github.com/fatih/color"
)

// localSidecarEndpoints returns every local service this configuration points
// at, whether or not it is currently selected.
func localSidecarEndpoints() []edge.Endpoint {
	var out []edge.Endpoint

	// The offline brain. Included whenever llama.cpp is either the active
	// provider or the configured failover target: a collision that only bites
	// during an outage is the worst kind.
	llamaActive := ai.ActiveProviderName() == llamacpp.Name ||
		strings.EqualFold(cfg.LLM.Fallback.Provider, llamacpp.Name)
	out = append(out, edge.Endpoint{
		Service: "llama.cpp",
		Role:    "LLM",
		URL:     llamacpp.BaseURL(cfg.LLM.LlamaCppURL),
		Active:  llamaActive,
	})

	// Local STT.
	if url := localSTTURL(); url != "" {
		out = append(out, edge.Endpoint{
			Service: "whisper-local",
			Role:    "STT",
			URL:     url,
			Active: cfg.Speech.STT.Provider == "whisper-local" ||
				containsFold(cfg.Speech.STT.Fallbacks, "whisper-local"),
		})
	}

	// Local TTS.
	if url := localTTSURL("piper-local", piperDefaultEndpoint); url != "" {
		out = append(out, edge.Endpoint{
			Service: "piper-local",
			Role:    "TTS",
			URL:     url,
			Active: cfg.Speech.TTS.Provider == "piper-local" ||
				containsFold(cfg.Speech.TTS.Fallbacks, "piper-local"),
		})
	}
	if url := localTTSURL("kokoro-local", kokoroDefaultEndpoint); url != "" {
		out = append(out, edge.Endpoint{
			Service: "kokoro-local",
			Role:    "TTS",
			URL:     url,
			Active: cfg.Speech.TTS.Provider == "kokoro-local" ||
				containsFold(cfg.Speech.TTS.Fallbacks, "kokoro-local"),
		})
	}
	return out
}

// Stock endpoints for the local speech sidecars, mirrored here because the
// adapters keep them unexported. A drift test pins them to the adapters.
const (
	whisperDefaultEndpoint = "http://127.0.0.1:8080"
	piperDefaultEndpoint   = "http://127.0.0.1:5000"
	kokoroDefaultEndpoint  = "http://127.0.0.1:8880/v1"
)

func localSTTURL() string {
	if cfg.Speech.STT.Provider == "whisper-local" && strings.TrimSpace(cfg.Speech.STT.BaseURL) != "" {
		return cfg.Speech.STT.BaseURL
	}
	return whisperDefaultEndpoint
}

func localTTSURL(provider, fallback string) string {
	if cfg.Speech.TTS.Provider == provider && strings.TrimSpace(cfg.Speech.TTS.BaseURL) != "" {
		return cfg.Speech.TTS.BaseURL
	}
	return fallback
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), want) {
			return true
		}
	}
	return false
}

// reportEndpointConflicts prints any address claimed by more than one local
// service. Returns the number of conflicts that involve an active service.
func reportEndpointConflicts() int {
	conflicts := edge.FindConflicts(localSidecarEndpoints())
	active := 0
	for _, c := range conflicts {
		if c.Involves() {
			active++
			color.Red("Endpoint conflict: %s", c.Describe())
			color.Red("  → One process owns a port. Whichever service is running there will")
			color.Red("    answer the other's requests with a 404, which reads as \"broken\".")
			for _, line := range conflictFix(c) {
				color.Yellow("    %s", line)
			}
			continue
		}
		color.Yellow("Endpoint overlap: %s", c.Describe())
		color.Yellow("  → Harmless while neither is selected, but they cannot both run there.")
	}
	return active
}

// conflictFix suggests the concrete move for the services involved.
func conflictFix(c Conflict) []string {
	has := func(name string) bool {
		for _, e := range c.Endpoints {
			if e.Service == name {
				return true
			}
		}
		return false
	}
	switch {
	case has("llama.cpp") && has("whisper-local"):
		return []string{
			"Move one of them. For example, keep llama.cpp on 8080 and run whisper on 8081:",
			"  whisper-server -m model.bin --port 8081 --inference-path /v1/audio/transcriptions",
			"  then set speech.stt.base_url to http://127.0.0.1:8081",
			"Or move the brain instead:",
			"  llama-server -m model.gguf --port 8081",
			"  export HELIX_LLAMACPP_URL=http://127.0.0.1:8081",
		}
	default:
		return []string{
			"Give one of them a different --port and update its base_url in ~/.helix/config.json.",
		}
	}
}

// Conflict is aliased so this file reads without the package qualifier.
type Conflict = edge.Conflict
