// internal/speech/adapter_openai_tts.go
// Purpose: OpenAI text-to-speech adapter. Always requests WAV output so
// playback stays pure-Go (no MP3 decode dependency; ADR-007).
package speech

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// openaiDefaultTTSModel is gpt-4o-mini-tts: cheaper than tts-1 (~$12/1M chars
// vs $15) with better prosody and streaming support — the current
// price/performance default for cloud speech output.
const openaiDefaultTTSModel = "gpt-4o-mini-tts"

// openaiTTS implements TTSProvider against POST /audio/speech. Also serves
// OpenAI-compatible local sidecars (Kokoro-FastAPI) via the local flag.
type openaiTTS struct {
	name    string
	display string
	baseURL string
	model   string
	voice   string
	key     string
	local   bool
}

// NewOpenAITTS builds the cloud OpenAI TTS adapter.
func NewOpenAITTS(model, voice, baseURL string) TTSProvider {
	if model == "" {
		model = openaiDefaultTTSModel
	}
	if voice == "" {
		voice = "alloy"
	}
	if baseURL == "" {
		baseURL = openaiDefaultSTTBaseURL
	}
	return &openaiTTS{name: "openai", display: "OpenAI TTS", baseURL: baseURL, model: model, voice: voice}
}

const kokoroLocalDefaultURL = "http://127.0.0.1:8880/v1"

// NewKokoroLocalTTS builds the Kokoro local sidecar adapter (ADR-002).
// Kokoro-FastAPI exposes the OpenAI /audio/speech contract on port 8880;
// Kokoro-82M gives near-cloud voice quality, free and offline, and runs on a
// Raspberry Pi 5 at ~1x real time.
func NewKokoroLocalTTS(model, voice, baseURL string) TTSProvider {
	if model == "" {
		model = "kokoro"
	}
	if voice == "" {
		voice = "af_heart"
	}
	if baseURL == "" {
		baseURL = kokoroLocalDefaultURL
	}
	return &openaiTTS{
		name:    "kokoro-local",
		display: "Kokoro (local sidecar)",
		baseURL: baseURL,
		model:   model,
		voice:   voice,
		local:   true,
	}
}

func (p *openaiTTS) Name() string         { return p.name }
func (p *openaiTTS) DisplayName() string  { return p.display }
func (p *openaiTTS) SetAPIKey(key string) { p.key = key }
func (p *openaiTTS) RequiresAPIKey() bool { return !p.local }
func (p *openaiTTS) IsLocal() bool        { return p.local }
func (p *openaiTTS) DefaultModel() string { return p.model }

// Synthesize returns WAV bytes for the text.
func (p *openaiTTS) Synthesize(ctx context.Context, text string, opts SynthesisOptions) (AudioFormat, error) {
	if p.RequiresAPIKey() && p.key == "" {
		return AudioFormat{}, fmt.Errorf("%s: missing API key", p.name)
	}
	if text == "" {
		return AudioFormat{}, fmt.Errorf("%s: empty text", p.name)
	}

	voice := p.voice
	if opts.Voice != "" {
		voice = opts.Voice
	}
	speed := opts.Speed
	if speed <= 0 {
		speed = 1.0
	}

	payload, err := json.Marshal(map[string]any{
		"model":           p.model,
		"input":           text,
		"voice":           voice,
		"speed":           speed,
		"response_format": "wav",
	})
	if err != nil {
		return AudioFormat{}, err
	}

	headers := map[string]string{}
	if p.key != "" {
		headers["Authorization"] = "Bearer " + p.key
	}
	data, err := sharedClient.DoRaw(ctx, http.MethodPost,
		p.baseURL+"/audio/speech", headers, "application/json", payload)
	if err != nil {
		return AudioFormat{}, fmt.Errorf("%s: %w", p.name, err)
	}
	if len(data) < 44 || string(data[:4]) != "RIFF" {
		return AudioFormat{}, fmt.Errorf("%s: unexpected non-WAV response (%d bytes)", p.name, len(data))
	}

	rate, channels, err := wavHeaderInfo(data)
	if err != nil {
		return AudioFormat{}, fmt.Errorf("%s: %w", p.name, err)
	}
	return AudioFormat{Kind: KindWAV, SampleRate: rate, Channels: channels, Bytes: data}, nil
}

// HealthCheck verifies the key against /models. For the local sidecar ANY
// HTTP response proves reachability (server versions differ in routes).
func (p *openaiTTS) HealthCheck(ctx context.Context) error {
	if p.local {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
		if err != nil {
			return err
		}
		resp, err := sharedClient.RawClient().Do(req)
		if err != nil {
			return fmt.Errorf("%s unreachable: %w", p.name, err)
		}
		defer func() { _ = resp.Body.Close() }()
		return nil
	}
	data, err := sharedClient.DoRaw(ctx, http.MethodGet, p.baseURL+"/models",
		map[string]string{"Authorization": "Bearer " + p.key}, "", nil)
	if err != nil {
		return fmt.Errorf("%s: %w", p.name, err)
	}
	_ = data
	return nil
}

// openaiPCMSampleRate is the sample rate OpenAI's `pcm` response format emits:
// 24 kHz, 16-bit signed little-endian, mono. Fixed by the API contract, which
// is what makes headerless streaming safe — there is nothing to parse.
const openaiPCMSampleRate = 24000

// SynthesizeStream begins synthesis and hands back the audio body unread, so
// playback can start while generation is still running (P7.2c).
//
// It requests `pcm` rather than the buffered path's `wav`: a WAV container
// would require parsing a header out of a stream whose first bytes may not have
// arrived, whereas raw PCM at a contract-fixed rate can be played from byte one.
//
// Args:
//   - ctx: cancels the request and, through the body, playback.
//   - text: the utterance.
//   - opts: voice/speed overrides.
//
// Returns: the open audio body, or an error before any audio was produced —
// which is the caller's cue to fall back to the buffered path.
// Complexity: O(1) request; the body streams.
func (p *openaiTTS) SynthesizeStream(
	ctx context.Context, text string, opts SynthesisOptions,
) (StreamedAudio, error) {
	if p.RequiresAPIKey() && p.key == "" {
		return StreamedAudio{}, fmt.Errorf("%s: missing API key", p.name)
	}
	if text == "" {
		return StreamedAudio{}, fmt.Errorf("%s: empty text", p.name)
	}

	voice := p.voice
	if opts.Voice != "" {
		voice = opts.Voice
	}
	speed := opts.Speed
	if speed <= 0 {
		speed = 1.0
	}

	headers := map[string]string{}
	if p.key != "" {
		headers["Authorization"] = "Bearer " + p.key
	}

	resp, err := sharedClient.DoRequest(ctx, http.MethodPost, p.baseURL+"/audio/speech", headers,
		map[string]any{
			"model":           p.model,
			"input":           text,
			"voice":           voice,
			"speed":           speed,
			"response_format": "pcm",
		})
	if err != nil {
		return StreamedAudio{}, fmt.Errorf("%s: %w", p.name, err)
	}

	return StreamedAudio{
		SampleRate: openaiPCMSampleRate,
		Channels:   1,
		Body:       resp.Body,
	}, nil
}
