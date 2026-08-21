// internal/speech/adapter_openai_stt.go
// Purpose: OpenAI-compatible STT adapter. Serves triple duty: the "openai"
// provider (Whisper API), "groq" (same wire format), and "whisper-local"
// (whisper.cpp's own server — ADR-002 sidecar pattern, no API key, local base
// URL).
//
// whisper-local needs route discovery, and that is not a nicety. whisper.cpp's
// server serves transcription at **/inference**, not /v1/audio/transcriptions:
// the OpenAI path only exists if the operator launched it with
// `--inference-path /v1/audio/transcriptions`. This adapter previously spoke
// only the OpenAI route, so against a stock `whisper-server` every
// transcription came back HTTP 404 — the local STT provider was unusable as
// shipped. It now tries both and remembers which one answered.
package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"helix/internal/providers"
)

const (
	openaiDefaultSTTBaseURL = "https://api.openai.com/v1"
	openaiDefaultSTTModel   = "whisper-1"

	// whisperLocalDefaultURL is whisper.cpp's own default bind address. Note
	// that llama-server ALSO defaults to 8080; they cannot both be there, which
	// is why edgeLocalPortConflicts() reports the clash instead of leaving the
	// user to discover it as a 404.
	whisperLocalDefaultURL = "http://127.0.0.1:8080"

	// openaiTranscribeRoute is the OpenAI-compatible transcription path.
	openaiTranscribeRoute = "/v1/audio/transcriptions"

	// whisperNativeRoute is whisper.cpp server's stock transcription path.
	whisperNativeRoute = "/inference"
)

// openaiSTT implements STTProvider against any OpenAI-compatible audio
// transcription endpoint.
type openaiSTT struct {
	name    string
	display string

	// origin is the server root WITHOUT a path (e.g. http://127.0.0.1:8080).
	// Routes are joined onto it, so a base URL given with or without /v1 works
	// the same way.
	origin string

	model string
	key   string
	local bool

	// routes are the transcription paths to try, in order. Cloud providers have
	// exactly one; whisper-local has two (see the package note).
	routes []string

	// route caches the path that last answered, so route discovery costs at
	// most one extra request per process rather than one per utterance.
	routeMu sync.Mutex
	route   string
}

// NewOpenAISTT builds the cloud OpenAI Whisper adapter.
func NewOpenAISTT(model, baseURL string) STTProvider {
	if model == "" {
		model = openaiDefaultSTTModel
	}
	if baseURL == "" {
		baseURL = openaiDefaultSTTBaseURL
	}
	return &openaiSTT{
		name:    "openai",
		display: "OpenAI Whisper",
		origin:  serverOrigin(baseURL),
		routes:  []string{routeSuffix(baseURL, openaiTranscribeRoute)},
		model:   model,
		local:   false,
	}
}

const (
	groqDefaultSTTBaseURL = "https://api.groq.com/openai/v1"
	groqDefaultSTTModel   = "whisper-large-v3-turbo"
)

// NewGroqSTT builds the Groq Whisper adapter (OpenAI-compatible audio API).
// At ~$0.04/hour with large-v3-turbo accuracy and ~200x-real-time inference,
// this is the price/performance default for cloud transcription.
func NewGroqSTT(model, baseURL string) STTProvider {
	if model == "" {
		model = groqDefaultSTTModel
	}
	if baseURL == "" {
		baseURL = groqDefaultSTTBaseURL
	}
	return &openaiSTT{
		name:    "groq",
		display: "Groq Whisper Turbo",
		origin:  serverOrigin(baseURL),
		routes:  []string{routeSuffix(baseURL, openaiTranscribeRoute)},
		model:   model,
		local:   false,
	}
}

// NewWhisperLocalSTT builds the local whisper.cpp sidecar adapter.
//
// Both transcription routes are offered because both are real: an operator who
// launched whisper-server with `--inference-path /v1/audio/transcriptions` gets
// the OpenAI path, and a stock launch gets /inference. Trying the OpenAI route
// first keeps the behavior of an OpenAI-shaped sidecar (Faster-Whisper server,
// LocalAI, Speaches) unchanged.
func NewWhisperLocalSTT(model, baseURL string) STTProvider {
	if baseURL == "" {
		baseURL = whisperLocalDefaultURL
	}
	origin := serverOrigin(baseURL)
	return &openaiSTT{
		name:    "whisper-local",
		display: "Whisper (local sidecar)",
		origin:  origin,
		// If the user pinned an explicit path (".../v1"), honor it first, then
		// fall back to whisper.cpp's native route on the same origin.
		routes: dedupeRoutes(
			routeSuffix(baseURL, openaiTranscribeRoute),
			openaiTranscribeRoute,
			whisperNativeRoute,
		),
		model: model, // empty = use the server's loaded model
		local: true,
	}
}

// serverOrigin reduces a base URL to scheme://host[:port], dropping any path.
// Routes are absolute, so keeping a path would produce /v1/v1/... .
func serverOrigin(raw string) string {
	raw = strings.TrimSuffix(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	return u.Scheme + "://" + u.Host
}

// routeSuffix turns a configured base URL plus a default route into the
// absolute path to request.
//
// A base URL that already carries a path prefix ("http://host:9000/api/v1")
// keeps it: that prefix is the operator telling Helix where the API lives, and
// discarding it is how a reverse-proxied sidecar becomes unreachable.
func routeSuffix(raw, route string) string {
	raw = strings.TrimSuffix(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil {
		return route
	}
	prefix := strings.TrimSuffix(u.Path, "/")
	switch {
	case prefix == "":
		return route
	case prefix == "/v1" && strings.HasPrefix(route, "/v1/"):
		// The caller already spelled /v1 into the route; do not double it.
		return route
	default:
		return prefix + strings.TrimPrefix(route, "/v1")
	}
}

// dedupeRoutes preserves order and drops repeats and blanks.
func dedupeRoutes(routes ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

func (p *openaiSTT) Name() string         { return p.name }
func (p *openaiSTT) DisplayName() string  { return p.display }
func (p *openaiSTT) SetAPIKey(key string) { p.key = key }
func (p *openaiSTT) RequiresAPIKey() bool { return !p.local }
func (p *openaiSTT) IsLocal() bool        { return p.local }
func (p *openaiSTT) DefaultModel() string { return p.model }

// Transcribe POSTs the clip as multipart/form-data to the transcription route.
//
// On a multi-route provider (whisper-local) an HTTP 404 means "wrong route on a
// live server", so the next candidate is tried; any other failure is a real
// error and is returned immediately. The winning route is cached.
func (p *openaiSTT) Transcribe(ctx context.Context, audio AudioFormat) (Transcript, error) {
	if p.RequiresAPIKey() && p.key == "" {
		return Transcript{}, fmt.Errorf("%s: missing API key", p.name)
	}
	if len(audio.Bytes) == 0 {
		return Transcript{}, fmt.Errorf("%s: empty audio clip", p.name)
	}

	body, contentType, err := p.buildForm(audio)
	if err != nil {
		return Transcript{}, err
	}

	headers := map[string]string{}
	if p.key != "" {
		headers["Authorization"] = "Bearer " + p.key
	}

	var lastErr error
	for _, route := range p.candidateRoutes() {
		data, err := sharedClient.DoRaw(ctx, http.MethodPost,
			p.origin+route, headers, contentType, body)
		if err != nil {
			lastErr = err
			// Only a 404 is evidence of a wrong route. A 401, 413, or 500 is a
			// real answer from the right endpoint and must not be masked by
			// retrying somewhere else.
			if providers.IsNotFound(err) && len(p.routes) > 1 {
				p.forgetRoute(route)
				continue
			}
			return Transcript{}, fmt.Errorf("%s: %w", p.name, err)
		}

		out, perr := parseTranscription(data)
		if perr != nil {
			lastErr = perr
			// A 200 that is not a transcription means something else lives here
			// (llama-server on the shared 8080 default answers 200 to plenty of
			// paths). Treat it like a wrong route rather than a transcript.
			if len(p.routes) > 1 {
				p.forgetRoute(route)
				continue
			}
			return Transcript{}, fmt.Errorf("%s: %w", p.name, perr)
		}

		p.rememberRoute(route)
		out.Provider = p.name
		return out, nil
	}

	if lastErr == nil {
		lastErr = errors.New("no transcription route responded")
	}
	return Transcript{}, fmt.Errorf("%s: %w", p.name, lastErr)
}

// buildForm renders the multipart body. whisper.cpp's /inference and the OpenAI
// route accept the same `file` field, so one body serves both.
func (p *openaiSTT) buildForm(audio AudioFormat) ([]byte, string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", "clip."+string(audio.Kind))
	if err != nil {
		return nil, "", fmt.Errorf("%s: build form: %w", p.name, err)
	}
	if _, err := fw.Write(audio.Bytes); err != nil {
		return nil, "", fmt.Errorf("%s: write form: %w", p.name, err)
	}
	if p.model != "" {
		_ = w.WriteField("model", p.model)
	}
	// whisper.cpp defaults /inference to `json`, but says so only in its docs;
	// asking explicitly makes the response shape the same on both routes.
	_ = w.WriteField("response_format", "json")
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("%s: close form: %w", p.name, err)
	}
	return body.Bytes(), w.FormDataContentType(), nil
}

// parseTranscription decodes a transcription response.
//
// It requires a `text` field rather than accepting any JSON: a 200 with no text
// is not a transcript, and returning it as an empty utterance is how a foreign
// service on the port turns into silent, undiagnosable failure.
func parseTranscription(data []byte) (Transcript, error) {
	var out struct {
		Text     string `json:"text"`
		Language string `json:"language,omitempty"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return Transcript{}, fmt.Errorf("parse response: %w", err)
	}
	if strings.TrimSpace(out.Text) == "" {
		return Transcript{}, errors.New("response carried no transcription text")
	}
	return Transcript{Text: out.Text, Language: out.Language}, nil
}

// candidateRoutes returns the cached winner first, then the rest.
func (p *openaiSTT) candidateRoutes() []string {
	p.routeMu.Lock()
	cached := p.route
	p.routeMu.Unlock()

	if cached == "" {
		return p.routes
	}
	out := make([]string, 0, len(p.routes))
	out = append(out, cached)
	for _, r := range p.routes {
		if r != cached {
			out = append(out, r)
		}
	}
	return out
}

func (p *openaiSTT) rememberRoute(route string) {
	p.routeMu.Lock()
	p.route = route
	p.routeMu.Unlock()
}

func (p *openaiSTT) forgetRoute(route string) {
	p.routeMu.Lock()
	if p.route == route {
		p.route = ""
	}
	p.routeMu.Unlock()
}

// ActiveRoute reports the transcription path that last answered ("" before the
// first success). /voice-status prints it, because "which route is this sidecar
// actually on?" is the first question when local STT misbehaves.
func (p *openaiSTT) ActiveRoute() string {
	p.routeMu.Lock()
	defer p.routeMu.Unlock()
	return p.route
}

// Endpoint reports the server origin, for diagnostics and conflict detection.
func (p *openaiSTT) Endpoint() string { return p.origin }

// HealthCheck proves the endpoint can actually transcribe.
//
// For a cloud provider, listing models is enough — a 200 there means the key
// works and the service is up. For a LOCAL sidecar it is not: the previous
// implementation treated any HTTP response, 404 included, as healthy, so a
// completely unrelated service on the shared port 8080 default reported
// "reachable" in /voice-status while every utterance failed. whisper.cpp has no
// /models route at all, so there was nothing else for that probe to find.
//
// Instead the local path sends a fraction of a second of silence through the
// real transcription route. It is the only probe that distinguishes "whisper is
// here" from "something is here", and it doubles as route discovery. Silence
// legitimately transcribes to nothing, so an empty result counts as success —
// what is being tested is the route, not the words.
func (p *openaiSTT) HealthCheck(ctx context.Context) error {
	if !p.local {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.origin+"/v1/models", nil)
		if err != nil {
			return err
		}
		if p.key != "" {
			req.Header.Set("Authorization", "Bearer "+p.key)
		}
		resp, err := sharedClient.RawClient().Do(req)
		if err != nil {
			return fmt.Errorf("%s unreachable: %w", p.name, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("%s health: HTTP %d", p.name, resp.StatusCode)
		}
		return nil
	}

	body, contentType, err := p.buildForm(AudioFormat{
		Kind: KindWAV, SampleRate: 16000, Channels: 1, Bytes: silentWAV(16000, 1, 200),
	})
	if err != nil {
		return err
	}

	var lastErr error
	for _, route := range p.candidateRoutes() {
		data, derr := sharedClient.DoRaw(ctx, http.MethodPost,
			p.origin+route, nil, contentType, body)
		if derr != nil {
			lastErr = derr
			continue
		}
		// Silence yields an empty transcript, which is a correct answer here —
		// only the SHAPE has to be right.
		var probe struct {
			Text *string `json:"text"`
		}
		if jerr := json.Unmarshal(data, &probe); jerr != nil || probe.Text == nil {
			lastErr = fmt.Errorf("%s answered on %s but not as a transcription service", p.origin, route)
			continue
		}
		p.rememberRoute(route)
		return nil
	}

	if lastErr == nil {
		lastErr = errors.New("no transcription route responded")
	}
	return fmt.Errorf("%s: %w", p.name, lastErr)
}
