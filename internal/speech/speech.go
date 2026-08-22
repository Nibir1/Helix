// internal/speech/speech.go
// Purpose: Package facade — builds the registry with all builtin adapters,
// holds the process-wide selection (mirroring the ai/providers.go pattern),
// and exposes Transcribe/Synthesize/Speak plus the TTS runtime toggle that
// Phase 2's automatic spoken responses will gate on.
package speech

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"helix/internal/providers"
)

// sharedClient is the process-wide HTTP client for all speech adapters.
var sharedClient = providers.NewHTTPClient(60 * time.Second)

// probeClient is sharedClient without retries, for local health probes. See
// HTTPClient.WithoutRetries for why retrying a loopback probe destroys the
// diagnosis it was meant to produce.
var probeClient = sharedClient.WithoutRetries()

var (
	mu         sync.RWMutex
	registry   *Registry
	ttsEnabled = true

	// lastSynthMs records the most recent TTS latency surfaced in
	// /voice-status against the configured budget.
	//
	// Its meaning depends on the path, and lastSpeechStreamed says which:
	//   streamed  — true TIME-TO-FIRST-AUDIO (request → first sample), P7.2c
	//   buffered  — full synthesis round trip, which for that path IS the
	//               first-audio time, since nothing plays until it completes
	// Both are therefore comparable against first_byte_ms.
	lastSynthMs atomic.Int64

	// lastSpeechStreamed reports whether the last spoken chunk used the
	// streaming path, so the status line can label the number honestly rather
	// than leaving the user to guess which it measured.
	lastSpeechStreamed atomic.Bool
)

// Init builds the global registry: all builtin adapters registered, keys
// hydrated from the keystore, and the active selection applied. Safe to call
// again (e.g. after /voice-setup changes): it rebuilds from scratch.
func Init(cfg Config) error {
	keys, err := providers.NewKeyStore()
	if err != nil {
		return fmt.Errorf("speech keystore: %w", err)
	}

	reg := NewRegistry(keys, sharedClient)
	registerBuiltins(reg, cfg)

	mu.Lock()
	registry = reg
	mu.Unlock()
	return nil
}

// registerBuiltins instantiates every builtin adapter. The ACTIVE provider
// receives the user's model/baseURL/voice overrides; fallbacks use defaults.
func registerBuiltins(reg *Registry, cfg Config) {
	// --- STT ---
	var sttModel, sttBase string
	if cfg.STT.Provider == "openai" {
		sttModel, sttBase = cfg.STT.Model, cfg.STT.BaseURL
	}
	reg.RegisterSTT(NewOpenAISTT(sttModel, sttBase))

	if cfg.STT.Provider == "groq" {
		sttModel, sttBase = cfg.STT.Model, cfg.STT.BaseURL
	} else {
		sttModel, sttBase = "", ""
	}
	reg.RegisterSTT(NewGroqSTT(sttModel, sttBase))

	if cfg.STT.Provider == "deepgram" {
		sttModel, sttBase = cfg.STT.Model, cfg.STT.BaseURL
	} else {
		sttModel, sttBase = "", ""
	}
	reg.RegisterSTT(NewDeepgramStreamingSTT(sttModel, sttBase))

	if cfg.STT.Provider == "whisper-local" {
		sttModel, sttBase = cfg.STT.Model, cfg.STT.BaseURL
	} else {
		sttModel, sttBase = "", ""
	}
	reg.RegisterSTT(NewWhisperLocalSTT(sttModel, sttBase))

	// --- TTS ---
	var ttsModel, ttsVoice, ttsBase string
	if cfg.TTS.Provider == "openai" {
		ttsModel, ttsVoice, ttsBase = cfg.TTS.Model, cfg.TTS.Voice, cfg.TTS.BaseURL
	}
	reg.RegisterTTS(NewOpenAITTS(ttsModel, ttsVoice, ttsBase))

	if cfg.TTS.Provider == "deepgram" {
		ttsModel, ttsBase = cfg.TTS.Model, cfg.TTS.BaseURL
	} else {
		ttsModel, ttsBase = "", ""
	}
	reg.RegisterTTS(NewDeepgramTTS(ttsModel, ttsBase))

	if cfg.TTS.Provider == "elevenlabs" {
		ttsModel, ttsVoice, ttsBase = cfg.TTS.Model, cfg.TTS.Voice, cfg.TTS.BaseURL
	} else {
		ttsModel, ttsVoice, ttsBase = "", "", ""
	}
	reg.RegisterTTS(NewElevenLabsTTS(ttsModel, ttsVoice, ttsBase))

	if cfg.TTS.Provider == "kokoro-local" {
		ttsModel, ttsVoice, ttsBase = cfg.TTS.Model, cfg.TTS.Voice, cfg.TTS.BaseURL
	} else {
		ttsModel, ttsVoice, ttsBase = "", "", ""
	}
	reg.RegisterTTS(NewKokoroLocalTTS(ttsModel, ttsVoice, ttsBase))

	if cfg.TTS.Provider == "piper-local" {
		ttsBase = cfg.TTS.BaseURL
	} else {
		ttsBase = ""
	}
	reg.RegisterTTS(NewPiperTTS(ttsBase))

	reg.SetConfig(cfg)
}

// Default returns the global registry (nil before Init).
func Default() *Registry {
	mu.RLock()
	defer mu.RUnlock()
	return registry
}

// StreamingSTT returns the primary STT provider when it supports streaming
// transcription, or false before Init / for batch-only providers.
func StreamingSTT() (StreamingSTTProvider, bool) {
	reg := Default()
	if reg == nil {
		return nil, false
	}
	return reg.StreamingSTT()
}

// Transcribe runs the global STT failover chain.
func Transcribe(ctx context.Context, clip AudioFormat) (Transcript, error) {
	reg := Default()
	if reg == nil {
		return Transcript{}, fmt.Errorf("speech not initialized")
	}
	return reg.Transcribe(ctx, clip)
}

// Synthesize runs the global TTS failover chain with the configured voice,
// recording the round-trip latency for the /voice-status budget line.
//
// This is the REPLY-LEVEL entry point: every call here claims the metric. Code
// that synthesizes a fragment of an already-started reply must use
// synthesizeChain instead — see the note there.
func Synthesize(ctx context.Context, text string) (AudioFormat, error) {
	start := time.Now()
	audio, err := synthesizeChain(ctx, text)
	lastSynthMs.Store(time.Since(start).Milliseconds())
	lastSpeechStreamed.Store(false)
	return audio, err
}

// synthesizeChain runs the TTS failover chain WITHOUT touching the
// /voice-status latency metric.
//
// The metric answers one question — "how long until the user heard the FIRST
// word of this reply?" — which is a property of the reply, not of every request
// made while it plays. SpeakStream streams sentence 1 (recording true
// time-to-first-audio) and then pipelines sentences 2..N behind the audio that
// is already playing. While those pipelined calls also wrote the metric, a
// reply that streamed perfectly reported the LAST sentence's full synthesis
// time labeled "buffered": the QA line
//
//	Last TTS time-to-first-audio: 1185ms (budget 800ms) [buffered — ...]
//
// on a reply whose first word had in fact arrived in ~150ms. Pipelined
// sentences go through this path so only the reply-level call records.
func synthesizeChain(ctx context.Context, text string) (AudioFormat, error) {
	reg := Default()
	if reg == nil {
		return AudioFormat{}, fmt.Errorf("speech not initialized")
	}
	cfg := reg.ActiveConfig()
	return reg.Synthesize(ctx, text, SynthesisOptions{Voice: cfg.TTS.Voice})
}

// LastSynthesizeLatencyMs returns the most recent TTS latency in milliseconds
// (0 before the first synthesis). On the streaming path this is true
// time-to-first-audio; on the buffered path it is the synthesis round trip,
// which for that path amounts to the same thing.
func LastSynthesizeLatencyMs() int64 { return lastSynthMs.Load() }

// LastSpeechStreamed reports whether the last spoken chunk was streamed.
func LastSpeechStreamed() bool { return lastSpeechStreamed.Load() }

// Speak synthesizes text and plays it through the Helix speaker stack
// (audio.PlaySpeech, ADR-007). Explicit invocations (/say) bypass the TTS
// runtime toggle; automatic spoken responses in Phase 2 gate on TTSEnabled.
func Speak(ctx context.Context, text string) error {
	// speakOnce streams when the provider supports it and falls back to the
	// buffered path otherwise (P7.2c), and is context-aware throughout (P12.5),
	// so /say and daemon `remote say` get both low latency and interruptibility.
	return speakOnce(ctx, text)
}

// SetTTSEnabled toggles automatic spoken responses (runtime only, like /audio).
func SetTTSEnabled(on bool) {
	mu.Lock()
	ttsEnabled = on
	mu.Unlock()
}

// TTSEnabled reports the automatic-response toggle state.
func TTSEnabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return ttsEnabled
}

// SetOfflineMode toggles local-first degradation for the global registry
// (Phase 4, P4.10): the daemon flips it on when connectivity drops so STT/TTS
// fail over to local sidecars. No-op before Init.
func SetOfflineMode(on bool) {
	if reg := Default(); reg != nil {
		reg.SetOffline(on)
	}
}

// ProviderStatusRow is one line of the /voice-status report.
type ProviderStatusRow struct {
	Name    string
	Display string
	Local   bool
	// RequiresKey reports whether the provider needs an API key at all.
	RequiresKey bool
	// HasKey reports whether a key is actually STORED for this provider.
	//
	// Strictly that, and nothing else: it used to be
	// `!RequiresAPIKey() || keys.Has(...)`, which made every keyless local
	// sidecar report "key" in /voice-status — a whisper.cpp server that needs no
	// credential at all looked like it had one. Whether a key is needed is
	// RequiresKey's job; renderers combine the two.
	HasKey bool
	// InChain reports whether the provider is in the ACTIVE failover chain.
	// Out-of-chain providers are not probed, so their Healthy=false means
	// "standby", not "down" — a distinction the status line has to make, and the
	// gate on whether a down local sidecar is worth a start-it hint.
	InChain      bool
	Healthy      bool
	HealthDetail string

	// Endpoint is the local service's address ("" for cloud providers). The
	// first question when a local sidecar misbehaves is "which address is this
	// even talking to", and the answer used to live only in config.json.
	Endpoint string

	// Route is the path that last answered, for sidecars that discover their
	// route (whisper.cpp serves /inference, an OpenAI-shaped server serves
	// /v1/audio/transcriptions). Empty until a call succeeds.
	Route string
}

// StatusReport summarizes the speech subsystem for /voice-status.
type StatusReport struct {
	STTChain             []string
	TTSChain             []string
	STTStatus            []ProviderStatusRow
	TTSStatus            []ProviderStatusRow
	TTSEnabled           bool
	Recorder             string
	RecorderErr          string
	TTSLastLatencyMs     int64 // 0 = never synthesized this process
	TTSFirstByteBudgetMs int
	// TTSLastStreamed reports whether TTSLastLatencyMs came from the streaming
	// path (true first-audio) or the buffered one (full synthesis).
	TTSLastStreamed bool
}

// Status collects chains, per-provider health (chain providers only, bounded
// by a short timeout per probe), and recorder availability.
func Status(ctx context.Context) StatusReport {
	report := StatusReport{TTSEnabled: TTSEnabled()}

	if rec, err := DetectRecorder(); err == nil {
		report.Recorder = rec
	} else {
		report.RecorderErr = err.Error()
	}

	reg := Default()
	if reg == nil {
		return report
	}

	report.STTChain = reg.STTChain()
	report.TTSChain = reg.TTSChain()
	report.TTSLastLatencyMs = lastSynthMs.Load()
	report.TTSLastStreamed = lastSpeechStreamed.Load()
	report.TTSFirstByteBudgetMs = reg.ActiveConfig().TTS.FirstByteMs
	if report.TTSFirstByteBudgetMs <= 0 {
		report.TTSFirstByteBudgetMs = 800
	}

	probe := func(ctx context.Context, ok bool, fn func(context.Context) error) (bool, string) {
		if !ok {
			return false, "not registered"
		}
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := fn(pctx); err != nil {
			return false, err.Error()
		}
		return true, ""
	}

	for _, name := range reg.STTNames() {
		p, _ := reg.STTProvider(name)
		inChain := contains(report.STTChain, name)
		healthy, detail := probe(ctx, inChain, p.HealthCheck)
		if !inChain {
			healthy, detail = false, "standby"
		}
		endpoint, route := localAddressing(p)
		report.STTStatus = append(report.STTStatus, ProviderStatusRow{
			Name: name, Display: p.DisplayName(), Local: p.IsLocal(),
			RequiresKey: p.RequiresAPIKey(), HasKey: reg.Keys().Has(STTKeyPrefix + name),
			InChain: inChain, Healthy: healthy, HealthDetail: detail,
			Endpoint: endpoint, Route: route,
		})
	}

	for _, name := range reg.TTSNames() {
		p, _ := reg.TTSProvider(name)
		inChain := contains(report.TTSChain, name)
		healthy, detail := probe(ctx, inChain, p.HealthCheck)
		if !inChain {
			healthy, detail = false, "standby"
		}
		endpoint, route := localAddressing(p)
		report.TTSStatus = append(report.TTSStatus, ProviderStatusRow{
			Name: name, Display: p.DisplayName(), Local: p.IsLocal(),
			RequiresKey: p.RequiresAPIKey(), HasKey: reg.Keys().Has(TTSKeyPrefix + name),
			InChain: inChain, Healthy: healthy, HealthDetail: detail,
			Endpoint: endpoint, Route: route,
		})
	}

	return report
}

// SaveSTTKey persists an STT provider key via the global registry.
func SaveSTTKey(name, key string) error {
	reg := Default()
	if reg == nil {
		return fmt.Errorf("speech not initialized")
	}
	return reg.SetSTTKey(name, key)
}

// SaveTTSKey persists a TTS provider key via the global registry.
func SaveTTSKey(name, key string) error {
	reg := Default()
	if reg == nil {
		return fmt.Errorf("speech not initialized")
	}
	return reg.SetTTSKey(name, key)
}

// endpointReporter is implemented by local sidecar adapters that can say which
// address and route they are using. Optional by design: cloud adapters have a
// fixed vendor endpoint and nothing useful to report here.
type endpointReporter interface {
	Endpoint() string
}

// routeReporter is implemented by adapters that discover their route.
type routeReporter interface {
	ActiveRoute() string
}

// localAddressing extracts the endpoint and resolved route from an adapter that
// exposes them.
func localAddressing(p any) (endpoint, route string) {
	if e, ok := p.(endpointReporter); ok {
		endpoint = e.Endpoint()
	}
	if r, ok := p.(routeReporter); ok {
		route = r.ActiveRoute()
	}
	return endpoint, route
}
