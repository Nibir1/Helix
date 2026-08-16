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
	"time"

	"helix/internal/audio"
	"helix/internal/providers"
)

// sharedClient is the process-wide HTTP client for all speech adapters.
var sharedClient = providers.NewHTTPClient(60 * time.Second)

var (
	mu         sync.RWMutex
	registry   *Registry
	ttsEnabled = true
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

	if cfg.STT.Provider == "deepgram" {
		sttModel, sttBase = cfg.STT.Model, cfg.STT.BaseURL
	} else {
		sttModel, sttBase = "", ""
	}
	reg.RegisterSTT(NewDeepgramSTT(sttModel, sttBase))

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

	if cfg.TTS.Provider == "elevenlabs" {
		ttsModel, ttsVoice, ttsBase = cfg.TTS.Model, cfg.TTS.Voice, cfg.TTS.BaseURL
	} else {
		ttsModel, ttsVoice, ttsBase = "", "", ""
	}
	reg.RegisterTTS(NewElevenLabsTTS(ttsModel, ttsVoice, ttsBase))

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

// Transcribe runs the global STT failover chain.
func Transcribe(ctx context.Context, clip AudioFormat) (Transcript, error) {
	reg := Default()
	if reg == nil {
		return Transcript{}, fmt.Errorf("speech not initialized")
	}
	return reg.Transcribe(ctx, clip)
}

// Synthesize runs the global TTS failover chain with the configured voice.
func Synthesize(ctx context.Context, text string) (AudioFormat, error) {
	reg := Default()
	if reg == nil {
		return AudioFormat{}, fmt.Errorf("speech not initialized")
	}
	cfg := reg.ActiveConfig()
	opts := SynthesisOptions{Voice: cfg.TTS.Voice}
	return reg.Synthesize(ctx, text, opts)
}

// Speak synthesizes text and plays it through the Helix speaker stack
// (audio.PlaySpeech, ADR-007). Explicit invocations (/say) bypass the TTS
// runtime toggle; automatic spoken responses in Phase 2 gate on TTSEnabled.
func Speak(ctx context.Context, text string) error {
	fmt_, err := Synthesize(ctx, text)
	if err != nil {
		return err
	}
	return audio.PlaySpeech(audio.SpeechFormat{
		Kind:       string(fmt_.Kind),
		SampleRate: fmt_.SampleRate,
		Channels:   fmt_.Channels,
		Data:       fmt_.Bytes,
	}, 1.0)
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

// ProviderStatusRow is one line of the /voice-status report.
type ProviderStatusRow struct {
	Name         string
	Display      string
	Local        bool
	RequiresKey  bool
	HasKey       bool
	Healthy      bool
	HealthDetail string
}

// StatusReport summarizes the speech subsystem for /voice-status.
type StatusReport struct {
	STTChain    []string
	TTSChain    []string
	STTStatus   []ProviderStatusRow
	TTSStatus   []ProviderStatusRow
	TTSEnabled  bool
	Recorder    string
	RecorderErr string
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
		report.STTStatus = append(report.STTStatus, ProviderStatusRow{
			Name: name, Display: p.DisplayName(), Local: p.IsLocal(),
			RequiresKey: p.RequiresAPIKey(), HasKey: !p.RequiresAPIKey() || reg.Keys().Has(STTKeyPrefix+name),
			Healthy: healthy, HealthDetail: detail,
		})
	}

	for _, name := range reg.TTSNames() {
		p, _ := reg.TTSProvider(name)
		inChain := contains(report.TTSChain, name)
		healthy, detail := probe(ctx, inChain, p.HealthCheck)
		if !inChain {
			healthy, detail = false, "standby"
		}
		report.TTSStatus = append(report.TTSStatus, ProviderStatusRow{
			Name: name, Display: p.DisplayName(), Local: p.IsLocal(),
			RequiresKey: p.RequiresAPIKey(), HasKey: !p.RequiresAPIKey() || reg.Keys().Has(TTSKeyPrefix+name),
			Healthy: healthy, HealthDetail: detail,
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
