// internal/providers/llamacpp/llamacpp.go
//
// Purpose: llama.cpp local provider, spoken to over `llama-server`'s
// OpenAI-compatible HTTP API (BlackBox P11.4).
//
// Architecture: this is the ADR-002 sidecar pattern applied to the LLM itself.
// `llama-server` is a user-managed external process — Helix never links a GGUF
// runtime, never downloads weights, and never installs it (same contract as
// whisper.cpp/Piper/Kokoro, P7.7). The core stays CGO-free.
//
// Why a second local runtime alongside Ollama: on boards where Ollama is
// unsupported — notably the first-gen Jetson Nano, whose JetPack 4.6 is frozen
// at CUDA 10.2 / Maxwell 5.3 (see docs/edge_deployment.md §5) — a hand-built
// llama.cpp is the only local-LLM path. Registering it as a first-class
// provider makes it a valid target for the Phase 11 offline failover chain.
package llamacpp

import (
	"os"
	"strings"

	"helix/internal/providers"
	openaicompatible "helix/internal/providers/openai_compatible"
)

const (
	// Name is the registry key for this provider.
	Name = "llamacpp"

	// DisplayName is the human-readable label shown by /provider and /doctor.
	DisplayName = "llama.cpp (llama-server)"

	// DefaultBaseURL is llama-server's stock OpenAI-compatible endpoint, i.e.
	// what `llama-server -m model.gguf --port 8080` serves.
	DefaultBaseURL = "http://127.0.0.1:8080/v1"

	// DefaultModel is a stable UI label, not a routing key: llama-server serves
	// whichever GGUF it was launched with and ignores the requested model name.
	// ListModels reports the real loaded model.
	DefaultModel = "local-gguf"

	// URLEnv overrides the endpoint without editing config.json.
	URLEnv = "HELIX_LLAMACPP_URL"
)

// BaseURL resolves the endpoint by precedence: explicit config, then the
// URLEnv override, then llama-server's default port.
//
// Args:
//   - configured: the value from config.json (may be empty).
//
// Returns: a normalized base URL ending in /v1.
// Complexity: O(len(url)).
func BaseURL(configured string) string {
	if s := strings.TrimSpace(configured); s != "" {
		return normalize(s)
	}
	if s := strings.TrimSpace(os.Getenv(URLEnv)); s != "" {
		return normalize(s)
	}
	return DefaultBaseURL
}

// normalize appends the OpenAI-compatible /v1 prefix when the user supplied a
// bare host:port. That omission is the single most common llama-server
// misconfiguration, and it fails as an opaque 404 rather than a clear error —
// so absorb it here instead of making the user diagnose it.
func normalize(u string) string {
	u = strings.TrimSuffix(strings.TrimSpace(u), "/")
	if strings.HasSuffix(u, "/v1") {
		return u
	}
	return u + "/v1"
}

// New builds the llama.cpp provider.
//
// Local: true is load-bearing, not cosmetic — it exempts the provider from the
// API-key requirement and marks it as an offline-capable brain for the Phase 11
// failover chain and the planner's longer local-model timeouts.
//
// Args:
//   - baseURL: configured endpoint ("" → env override → default).
//   - client: the shared retrying HTTP client.
//
// Returns: a registry-ready provider.
// Complexity: O(1).
func New(baseURL string, client *providers.HTTPClient) *openaicompatible.Provider {
	return openaicompatible.New(openaicompatible.Config{
		Name:         Name,
		DisplayName:  DisplayName,
		BaseURL:      BaseURL(baseURL),
		DefaultModel: DefaultModel,
		Local:        true,
	}, client)
}

// Diagnosis classifies why an endpoint health check failed.
type Diagnosis int

const (
	// DiagnosisUnreachable: nothing is listening on the port.
	DiagnosisUnreachable Diagnosis = iota

	// DiagnosisForeignServer: something answered, but it is not llama-server.
	DiagnosisForeignServer
)

// Diagnose classifies a failed health check and returns operator guidance.
//
// The distinction matters more than it looks. Port 8080 is llama-server's
// default AND one of the most commonly occupied ports on a developer machine —
// so "not reachable" is actively misleading when some unrelated dev service is
// sitting there answering HTTP. The user reads "not reachable", assumes the
// port is free, starts llama-server on it, and gets a bind conflict instead of
// an explanation.
//
// An HTTP status in the error means a server replied; anything else (dial
// refused, DNS, timeout) means nobody is home.
//
// Args:
//   - err: the error returned by HealthCheck.
//   - url: the endpoint that was probed.
//
// Returns: the classification and a multi-line hint.
// Complexity: O(len(err)).
func Diagnose(err error, url string) (Diagnosis, string) {
	if err != nil && strings.Contains(err.Error(), "HTTP ") {
		return DiagnosisForeignServer, "Something IS listening on " + url +
			", but it did not answer as llama-server.\n" +
			"  That port is probably in use by another service. Either stop it, or\n" +
			"  run llama-server on a free port and point Helix at it:\n" +
			"    llama-server -m model.gguf --port 8081\n" +
			"    export HELIX_LLAMACPP_URL=http://127.0.0.1:8081"
	}
	return DiagnosisUnreachable, "Nothing is listening on " + url + ".\n" +
		"  Start llama-server first:\n" +
		"    llama-server -m /path/to/model.gguf --port 8080"
}
