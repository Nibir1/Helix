// internal/speech/registry.go
// Purpose: Speech provider registry with health-gated automatic failover —
// the capability the LLM registry never had, delivered for speech first
// (roadmap §6 Phase 1, P1.2). Keys are namespaced "stt.<name>"/"tts.<name>"
// in the shared ~/.helix/secrets.json keystore.
package speech

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"helix/internal/providers"
)

// STTConfig selects the active speech-to-text provider and its fallback chain.
type STTConfig struct {
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	BaseURL   string   `json:"base_url"` // overrides the provider default (tests, proxies)
	Fallbacks []string `json:"fallbacks"`
}

// TTSConfig selects the active text-to-speech provider and its fallback chain.
type TTSConfig struct {
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	Voice     string   `json:"voice"`
	BaseURL   string   `json:"base_url"`
	Fallbacks []string `json:"fallbacks"`
}

// Config is the full speech subsystem selection.
type Config struct {
	STT STTConfig `json:"stt"`
	TTS TTSConfig `json:"tts"`
}

// KeyPrefixes for keystore namespacing.
const (
	STTKeyPrefix = "stt."
	TTSKeyPrefix = "tts."
)

// Registry holds all registered speech providers plus the active selection.
type Registry struct {
	mu     sync.RWMutex
	stt    map[string]STTProvider
	tts    map[string]TTSProvider
	keys   *providers.KeyStore
	client *providers.HTTPClient
	cfg    Config
}

// NewRegistry creates a speech registry over the shared keystore and HTTP
// client (same construction pattern as providers.NewRegistry).
func NewRegistry(keys *providers.KeyStore, client *providers.HTTPClient) *Registry {
	return &Registry{
		stt:    make(map[string]STTProvider),
		tts:    make(map[string]TTSProvider),
		keys:   keys,
		client: client,
	}
}

// Keys returns the shared keystore.
func (r *Registry) Keys() *providers.KeyStore { return r.keys }

// Client returns the shared HTTP client.
func (r *Registry) Client() *providers.HTTPClient { return r.client }

// RegisterSTT adds an STT provider and hydrates its API key from the keystore.
func (r *Registry) RegisterSTT(p STTProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.stt[p.Name()] = p
	if key := r.keys.Get(STTKeyPrefix + p.Name()); key != "" {
		p.SetAPIKey(key)
	}
}

// RegisterTTS adds a TTS provider and hydrates its API key from the keystore.
func (r *Registry) RegisterTTS(p TTSProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tts[p.Name()] = p
	if key := r.keys.Get(TTSKeyPrefix + p.Name()); key != "" {
		p.SetAPIKey(key)
	}
}

// STTNames returns the sorted names of registered STT providers.
func (r *Registry) STTNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.stt))
	for name := range r.stt {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TTSNames returns the sorted names of registered TTS providers.
func (r *Registry) TTSNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tts))
	for name := range r.tts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// STTProvider returns a registered provider by name.
func (r *Registry) STTProvider(name string) (STTProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.stt[name]
	return p, ok
}

// TTSProvider returns a registered provider by name.
func (r *Registry) TTSProvider(name string) (TTSProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.tts[name]
	return p, ok
}

// SetConfig stores the active selection (primary + fallbacks).
func (r *Registry) SetConfig(cfg Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg = cfg
}

// ActiveConfig returns the stored selection.
func (r *Registry) ActiveConfig() Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

// SetSTTKey persists an STT provider key and applies it to the live instance.
func (r *Registry) SetSTTKey(name, key string) error {
	if _, ok := r.STTProvider(name); !ok {
		return fmt.Errorf("STT provider %q is not registered", name)
	}
	if err := r.keys.Set(STTKeyPrefix+name, key); err != nil {
		return err
	}
	if p, ok := r.STTProvider(name); ok {
		p.SetAPIKey(key)
	}
	return nil
}

// SetTTSKey persists a TTS provider key and applies it to the live instance.
func (r *Registry) SetTTSKey(name, key string) error {
	if _, ok := r.TTSProvider(name); !ok {
		return fmt.Errorf("TTS provider %q is not registered", name)
	}
	if err := r.keys.Set(TTSKeyPrefix+name, key); err != nil {
		return err
	}
	if p, ok := r.TTSProvider(name); ok {
		p.SetAPIKey(key)
	}
	return nil
}

// STTChain resolves the configured failover chain to registered providers:
// primary first, then fallbacks; unregistered names are skipped.
func (r *Registry) STTChain() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chain := make([]string, 0, 1+len(r.cfg.STT.Fallbacks))
	appendIf := func(name string) {
		if name == "" {
			return
		}
		if _, ok := r.stt[name]; ok && !contains(chain, name) {
			chain = append(chain, name)
		}
	}
	appendIf(r.cfg.STT.Provider)
	for _, f := range r.cfg.STT.Fallbacks {
		appendIf(f)
	}
	return chain
}

// TTSChain resolves the TTS failover chain the same way as STTChain.
func (r *Registry) TTSChain() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	chain := make([]string, 0, 1+len(r.cfg.TTS.Fallbacks))
	appendIf := func(name string) {
		if name == "" {
			return
		}
		if _, ok := r.tts[name]; ok && !contains(chain, name) {
			chain = append(chain, name)
		}
	}
	appendIf(r.cfg.TTS.Provider)
	for _, f := range r.cfg.TTS.Fallbacks {
		appendIf(f)
	}
	return chain
}

// Transcribe runs the STT failover chain: the first provider that succeeds
// wins; every failure is collected so the user sees why the chain collapsed
// when all providers fail. Context cancellation aborts immediately.
func (r *Registry) Transcribe(ctx context.Context, audio AudioFormat) (Transcript, error) {
	chain := r.STTChain()
	if len(chain) == 0 {
		return Transcript{}, errors.New("no STT provider configured — run /voice-setup")
	}

	var errs []error
	for _, name := range chain {
		if err := ctx.Err(); err != nil {
			return Transcript{}, err
		}

		p, _ := r.STTProvider(name)
		t, err := p.Transcribe(ctx, audio)
		if err == nil {
			return t, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", name, err))
	}

	return Transcript{}, fmt.Errorf("all STT providers failed: %w", errors.Join(errs...))
}

// Synthesize runs the TTS failover chain, mirroring Transcribe.
func (r *Registry) Synthesize(ctx context.Context, text string, opts SynthesisOptions) (AudioFormat, error) {
	chain := r.TTSChain()
	if len(chain) == 0 {
		return AudioFormat{}, errors.New("no TTS provider configured — run /voice-setup")
	}

	var errs []error
	for _, name := range chain {
		if err := ctx.Err(); err != nil {
			return AudioFormat{}, err
		}

		p, _ := r.TTSProvider(name)
		audio, err := p.Synthesize(ctx, text, opts)
		if err == nil {
			return audio, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", name, err))
	}

	return AudioFormat{}, fmt.Errorf("all TTS providers failed: %w", errors.Join(errs...))
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
