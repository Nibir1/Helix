// internal/speech/speech.go
// Purpose: Package facade — builds the registry with all builtin adapters,
// holds the process-wide selection (mirroring the ai/providers.go pattern),
// and exposes Transcribe/Synthesize/Speak plus the TTS runtime toggle that
// Phase 2's automatic spoken responses will gate on.
package speech

import (
	"context"
	"fmt"
	"strings"
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
// again (e.g. after /blackbox setup changes): it rebuilds from scratch.
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

// sttEndpointFor resolves a provider's endpoint override.
//
// Per-provider Endpoints win, then BaseURL for the primary. The order matters:
// a sidecar moved to a free port must keep that port whether it is the primary
// or a fallback, and before Endpoints existed a fallback simply could not be
// moved — its reassigned URL landed in BaseURL, which belongs to whoever is
// primary, so the sidecar stayed unreachable AND the primary's endpoint was
// overwritten with a localhost address.
func sttEndpointFor(cfg Config, provider string) string {
	if url, ok := cfg.STT.Endpoints[provider]; ok && strings.TrimSpace(url) != "" {
		return url
	}
	if cfg.STT.Provider == provider {
		return cfg.STT.BaseURL
	}
	return ""
}

// newPiperProvider chooses how Piper will be reached.
//
// The native binary wins when it is present: no interpreter, no server, no port
// — and therefore none of the macOS AirPlay-on-5000 collision the HTTP path
// spends a wizard step avoiding. The Python server remains supported because
// people already run it, and because the standalone binaries are frozen at the
// 2023.11.14 release while Piper's own development moved on.
func newPiperProvider(cfg Config) TTSProvider {
	if bin, err := FindPiperBinary(); err == nil {
		return NewPiperNativeTTS(bin, PiperVoicePath())
	}
	return NewPiperTTS(ttsEndpointFor(cfg, "piper-local"))
}

// ttsEndpointFor is the TTS counterpart of sttEndpointFor.
func ttsEndpointFor(cfg Config, provider string) string {
	if url, ok := cfg.TTS.Endpoints[provider]; ok && strings.TrimSpace(url) != "" {
		return url
	}
	if cfg.TTS.Provider == provider {
		return cfg.TTS.BaseURL
	}
	return ""
}

// registerBuiltins instantiates every builtin adapter. Model/voice overrides
// belong to the ACTIVE provider; endpoints are resolved per provider so a local
// sidecar keeps its port in either role.
func registerBuiltins(reg *Registry, cfg Config) {
	// --- STT ---
	sttModel := func(provider string) string {
		if cfg.STT.Provider == provider {
			return cfg.STT.Model
		}
		return ""
	}
	reg.RegisterSTT(NewOpenAISTT(sttModel("openai"), sttEndpointFor(cfg, "openai")))
	reg.RegisterSTT(NewGroqSTT(sttModel("groq"), sttEndpointFor(cfg, "groq")))
	reg.RegisterSTT(NewDeepgramStreamingSTT(sttModel("deepgram"), sttEndpointFor(cfg, "deepgram")))
	reg.RegisterSTT(NewWhisperLocalSTT(sttModel("whisper-local"), sttEndpointFor(cfg, "whisper-local")))

	// --- TTS ---
	ttsModel := func(provider string) string {
		if cfg.TTS.Provider == provider {
			return cfg.TTS.Model
		}
		return ""
	}
	ttsVoice := func(provider string) string {
		if cfg.TTS.Provider == provider {
			return cfg.TTS.Voice
		}
		return ""
	}
	reg.RegisterTTS(NewOpenAITTS(ttsModel("openai"), ttsVoice("openai"), ttsEndpointFor(cfg, "openai")))
	reg.RegisterTTS(NewDeepgramTTS(ttsModel("deepgram"), ttsEndpointFor(cfg, "deepgram")))
	reg.RegisterTTS(NewElevenLabsTTS(ttsModel("elevenlabs"), ttsVoice("elevenlabs"), ttsEndpointFor(cfg, "elevenlabs")))
	reg.RegisterTTS(NewKokoroLocalTTS(ttsModel("kokoro-local"), ttsVoice("kokoro-local"), ttsEndpointFor(cfg, "kokoro-local")))
	reg.RegisterTTS(NewCSMLocalTTS(ttsModel("csm-local"), ttsVoice("csm-local"), ttsEndpointFor(cfg, "csm-local")))
	// Piper: prefer the interpreter-free native binary, fall back to the Python
	// HTTP server. Same provider NAME either way, so every preset, pricing row
	// and failover chain that already names "piper-local" keeps working — the
	// transport is an implementation detail the user did not ask to care about.
	reg.RegisterTTS(newPiperProvider(cfg))

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

// conversation is the package-level context store, nil unless a context-capable
// provider is configured with a turn budget.
//
// Package-level rather than threaded through every caller for the same reason
// the offline flag is: the voice loop, the daemon and /blackbox say all speak
// through the same entry points, and a parameter would have to cross all of them
// to serve one provider.
var conversation *ConversationContext

// EnableConversationContext turns on retention of recent turns for providers
// whose prosody is conditioned on them (CSM-1B).
//
// turns <= 0 disables it and drops whatever was held. Retention is memory-only
// and bounded; see internal/speech/conversation.go for why those two properties
// are the design rather than a detail.
func EnableConversationContext(turns, maxBytes int) {
	mu.Lock()
	defer mu.Unlock()
	if turns <= 0 {
		if conversation != nil {
			conversation.Clear()
		}
		conversation = nil
		return
	}
	conversation = NewConversationContext(turns, maxBytes)
}

// RecordUserTurn adds what the user said, with the audio that was captured.
func RecordUserTurn(text string, audio AudioFormat) {
	mu.RLock()
	c := conversation
	mu.RUnlock()
	c.Append(ConversationTurn{Speaker: SpeakerUser, Text: text, Audio: audio})
}

// RecordAssistantTurn adds what Helix said, with the audio it produced.
func RecordAssistantTurn(text string, audio AudioFormat) {
	mu.RLock()
	c := conversation
	mu.RUnlock()
	c.Append(ConversationTurn{Speaker: SpeakerAssistant, Text: text, Audio: audio})
}

// ClearConversationContext drops retained turns — called when live mode ends.
func ClearConversationContext() {
	mu.RLock()
	c := conversation
	mu.RUnlock()
	c.Clear()
}

// ConversationStats reports retention for status output.
func ConversationStats() (turns, bytes int) {
	mu.RLock()
	c := conversation
	mu.RUnlock()
	return c.Len(), c.Bytes()
}

// ContextCapable is implemented by TTS providers whose prosody is conditioned on
// prior turns AND which can report what the server actually did with them.
//
// An interface rather than a type switch because the honest answer is
// provider-specific: only the adapter knows whether its sidecar acknowledged the
// context it was sent, and only it can distinguish "used" from "silently
// dropped".
type ContextCapable interface {
	ContextStatus() (honored, ignored, rejected bool)
}

// ContextReport is what the status surface needs to describe conversational
// context WITHOUT overstating it.
//
// The distinction that matters is Honored vs Ignored. Retention being on says
// only that Helix is holding turns; it says nothing about whether the voice is
// actually conditioned on them, because an unpatched csm.rs accepts a context
// field and silently discards it (serde ignores unknown fields). Reporting
// "context: on" in that case would claim a capability the user is not getting,
// which is the same class of bug as the camera reporting "ready" on a host that
// could never capture a frame.
type ContextReport struct {
	// Enabled is retention: Helix is holding recent turns in memory.
	Enabled bool
	Turns   int
	Bytes   int

	// Provider names the context-capable voice in the chain, empty when none is
	// configured — retention with nothing to use it is worth showing as such.
	Provider string

	// Attempted records that context was actually sent at least once. Before the
	// first spoken reply nothing is known yet, and "unknown" must not render as
	// either success or failure.
	Attempted bool

	// Honored: the server acknowledged the context it was sent.
	// Ignored: it answered without acknowledging — an unpatched sidecar.
	// Rejected: it refused the field outright and Helix stopped sending it.
	Honored  bool
	Ignored  bool
	Rejected bool
}

// ConversationReport describes conversational context for status output.
func ConversationReport() ContextReport {
	turns, bytes := ConversationStats()

	mu.RLock()
	enabled := conversation != nil
	reg := registry
	mu.RUnlock()

	rep := ContextReport{Enabled: enabled, Turns: turns, Bytes: bytes}
	if reg == nil {
		return rep
	}

	// The whole chain, not just the primary: a context-capable voice sitting
	// behind a fallback still shapes what the user hears when the primary fails.
	for _, name := range reg.TTSChain() {
		p, ok := reg.TTSProvider(name)
		if !ok {
			continue
		}
		cc, ok := p.(ContextCapable)
		if !ok {
			continue
		}
		honored, ignored, rejected := cc.ContextStatus()
		rep.Provider = name
		rep.Honored, rep.Ignored, rep.Rejected = honored, ignored, rejected
		rep.Attempted = honored || ignored || rejected
		break
	}
	return rep
}

// currentContext returns the turns to condition on.
func currentContext() []ConversationTurn {
	mu.RLock()
	c := conversation
	mu.RUnlock()
	if c == nil {
		return nil
	}
	return c.Recent(c.maxTurns)
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
	return reg.Synthesize(ctx, text, SynthesisOptions{
		Voice:   cfg.TTS.Voice,
		Context: currentContext(),
	})
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
