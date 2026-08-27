// cmd/helix/local_endpoints.go
// Purpose: collect the local sidecar endpoints this session is configured to
// use, so /doctor and /blackbox status can name a port collision instead of
// leaving it to be discovered as a 404.
//
// The stock ports genuinely overlap: llama-server defaults to 8080 and
// whisper.cpp's server also defaults to 8080. Helix keeps both upstream
// defaults — changing one would break the other's out-of-the-box launch — and
// reports the clash when a configuration actually has both.
package main

import (
	"fmt"
	"strings"

	"helix/internal/ai"
	"helix/internal/edge"
	"helix/internal/providers/llamacpp"
	"helix/internal/shell"
	"helix/internal/speech"
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
	if url := localTTSURL("csm-local", speech.CSMDefaultEndpoint); url != "" {
		out = append(out, edge.Endpoint{
			Service: "csm-local",
			Role:    "TTS",
			URL:     url,
			Active: cfg.Speech.TTS.Provider == "csm-local" ||
				containsFold(cfg.Speech.TTS.Fallbacks, "csm-local"),
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
	// csm-local is deliberately absent here: its default is
	// speech.CSMDefaultEndpoint, exported so there is nothing to mirror.
	whisperDefaultEndpoint = "http://127.0.0.1:8080"
	piperDefaultEndpoint   = "http://127.0.0.1:5000"
	kokoroDefaultEndpoint  = "http://127.0.0.1:8880/v1"
)

// localSTTURL and localTTSURL resolve where a sidecar is ACTUALLY configured to
// answer, which is what sidecarEndpoint already knows.
//
// They used to consult only cfg.Speech.*.BaseURL — the PRIMARY provider's URL —
// and otherwise return the stock default. But the wizard does not write
// BaseURL when it moves a sidecar off a busy port: it writes the per-provider
// Endpoints map, which these never read. So after the wizard reassigned
// whisper-local to a free port and said so on screen, the conflict report two
// panels later still announced it on 8080 and warned about a collision with
// llama.cpp that no longer existed. A report whose whole job is naming the
// right port was reading the wrong field.
func localSTTURL() string {
	return sidecarEndpoint("stt", "whisper-local", whisperDefaultEndpoint)
}

func localTTSURL(provider, fallback string) string {
	return sidecarEndpoint("tts", provider, fallback)
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
//
// All three branches render the same shape — a labelled state row, then its
// explanation indented beneath. They did not: the harmless third branch printed
// bare wrapped prose with no label, so on /doctor it landed mid-panel between
// two labelled blocks as an orphaned paragraph, reading like a fragment of the
// row above it rather than a note of its own.
func reportEndpointConflicts() int {
	conflicts := edge.FindConflicts(localSidecarEndpoints())
	active := 0
	for _, c := range conflicts {
		switch {
		case c.Involves():
			// Two SELECTED services on one address: this breaks as configured.
			active++
			fmt.Println(shell.Step(shell.StateBad, "endpoint conflict", ""))
			for _, l := range shell.StepDetail(c.Describe(), shell.Value) {
				fmt.Println(l)
			}
			for _, l := range shell.StepDetail(
				"one process owns a port; the other's requests get a 404, which reads as broken",
				shell.Muted) {
				fmt.Println(l)
			}
			fmt.Println(shell.Hint("/blackbox setup moves one of them to a free port"))

		case c.Occupied:
			// One selected, one not, but something IS on the port: worth saying,
			// because the selected service cannot bind it.
			fmt.Println(shell.Step(shell.StateWarn, "endpoint overlap", ""))
			for _, l := range shell.StepDetail(c.Describe(), shell.Value) {
				fmt.Println(l)
			}
			fmt.Println(shell.Hint(fmt.Sprintf(
				"something already holds %s — /blackbox setup reassigns a free port", c.Address)))

		default:
			// Purely theoretical: shared configuration, empty port, at most one
			// selected. A red warning with a six-line remedy here was noise on a
			// machine where the port was simply free.
			fmt.Println(shell.Step(shell.StateIdle, "endpoints shared", ""))
			for _, l := range shell.StepDetail(
				c.Describe()+" — nothing is on that address right now", shell.Muted) {
				fmt.Println(l)
			}
		}
	}
	return active
}

// Conflict is aliased so this file reads without the package qualifier.
type Conflict = edge.Conflict
