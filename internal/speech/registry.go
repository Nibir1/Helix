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
	"strings"
	"sync"

	"helix/internal/providers"
)

// STTConfig selects the active speech-to-text provider and its fallback chain.
type STTConfig struct {
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	BaseURL   string   `json:"base_url"` // overrides the provider default (tests, proxies)
	Fallbacks []string `json:"fallbacks"`
	// Endpoints are per-provider endpoint overrides, keyed by provider name.
	// BaseURL only ever reached the PRIMARY provider, so a local sidecar picked
	// as a FALLBACK could not be moved off its default port.
	Endpoints map[string]string `json:"endpoints,omitempty"`
	// StreamChunkMs is the streaming capture chunk length (0 → 300ms).
	StreamChunkMs int `json:"stream_chunk_ms"`
}

// TTSConfig selects the active text-to-speech provider and its fallback chain.
type TTSConfig struct {
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	Voice     string   `json:"voice"`
	BaseURL   string   `json:"base_url"`
	Fallbacks []string `json:"fallbacks"`
	// Endpoints are per-provider endpoint overrides. See STTConfig.
	Endpoints map[string]string `json:"endpoints,omitempty"`
	// FirstByteMs is the TTS first-byte latency budget (0 → 800ms).
	FirstByteMs int `json:"first_byte_ms"`
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
	mu      sync.RWMutex
	stt     map[string]STTProvider
	tts     map[string]TTSProvider
	keys    *providers.KeyStore
	client  *providers.HTTPClient
	cfg     Config
	offline bool // Phase 4 graceful degradation: local-first chain ordering

	// lastSTT/lastTTS record the most recent failover-chain outcome so callers
	// can report degradation WITHOUT probing anything. The interactive shell's
	// per-turn status line needs this: it runs in the hot loop, where a health
	// check would add a round trip to every turn just to print one word.
	lastSTT ChainHealth
	lastTTS ChainHealth
}

// ChainHealth is the outcome of the most recent run of a failover chain.
type ChainHealth struct {
	// Attempted reports whether the chain has run at least once. The zero value
	// is therefore "unused", not "broken" — the state every session starts in.
	Attempted bool

	// OK reports whether some provider in the chain answered.
	OK bool

	// Used is the provider that answered ("" when none did).
	Used string

	// Failed lists, in chain order, the providers that errored before one
	// answered. Empty on a clean primary-only call.
	Failed []string
}

// Degraded reports whether the last call needed more than its primary provider,
// or failed outright.
func (h ChainHealth) Degraded() bool {
	return h.Attempted && (!h.OK || len(h.Failed) > 0)
}

// Reason returns a short explanation of the degradation, or "" when the chain is
// clean or has not run. Kept to one clause: it goes inside a one-line status.
func (h ChainHealth) Reason() string {
	if !h.Degraded() {
		return ""
	}
	if !h.OK {
		if len(h.Failed) == 0 {
			return "no provider configured"
		}
		return "chain failed: " + strings.Join(h.Failed, ", ")
	}
	return "fallback: " + strings.Join(h.Failed, ", ") + " down, using " + h.Used
}

// LastSTTHealth returns the most recent STT chain outcome.
func (r *Registry) LastSTTHealth() ChainHealth {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastSTT
}

// LastTTSHealth returns the most recent TTS chain outcome.
func (r *Registry) LastTTSHealth() ChainHealth {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastTTS
}

// recordSTTHealth stores one STT chain outcome.
func (r *Registry) recordSTTHealth(h ChainHealth) {
	r.mu.Lock()
	r.lastSTT = h
	r.mu.Unlock()
}

// recordTTSHealth stores one TTS chain outcome.
func (r *Registry) recordTTSHealth(h ChainHealth) {
	r.mu.Lock()
	r.lastTTS = h
	r.mu.Unlock()
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

// SetOffline toggles local-first degradation (Phase 4, P4.10): when true the
// STT/TTS failover chains prefer local sidecars over cloud providers so the
// daemon keeps transcribing/speaking without internet; false restores the
// configured order.
func (r *Registry) SetOffline(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.offline = on
}

// Offline reports the degradation state.
func (r *Registry) Offline() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.offline
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
	if r.offline {
		localFirst(chain, func(name string) bool {
			p, ok := r.stt[name]
			return ok && p.IsLocal()
		})
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
	if r.offline {
		localFirst(chain, func(name string) bool {
			p, ok := r.tts[name]
			return ok && p.IsLocal()
		})
	}
	return chain
}

// localFirst reorders names so entries where isLocal reports true come first,
// preserving relative order within each group.
func localFirst(names []string, isLocal func(string) bool) {
	var local, remote []string
	for _, n := range names {
		if isLocal(n) {
			local = append(local, n)
		} else {
			remote = append(remote, n)
		}
	}
	copy(names, append(local, remote...))
}

// StreamingSTT returns the primary STT provider when it supports streaming
// transcription (Deepgram WebSocket). The voice turn uses it to show live
// partials; callers fall back to batch capture when this reports false.
func (r *Registry) StreamingSTT() (StreamingSTTProvider, bool) {
	chain := r.STTChain()
	if len(chain) == 0 {
		return nil, false
	}
	p, _ := r.STTProvider(chain[0])
	s, ok := p.(StreamingSTTProvider)
	return s, ok
}

// Transcribe runs the STT failover chain: the first provider that succeeds
// wins; every failure is collected so the user sees why the chain collapsed
// when all providers fail. Context cancellation aborts immediately.
func (r *Registry) Transcribe(ctx context.Context, audio AudioFormat) (Transcript, error) {
	chain := r.STTChain()
	if len(chain) == 0 {
		r.recordSTTHealth(ChainHealth{Attempted: true})
		return Transcript{}, errors.New("no STT provider configured — run /blackbox setup")
	}

	var errs []error
	var failed []string
	for _, name := range chain {
		if err := ctx.Err(); err != nil {
			// A cancelled turn says nothing about provider health, so the last
			// outcome is left as it was rather than recorded as a failure.
			return Transcript{}, err
		}

		p, _ := r.STTProvider(name)
		t, err := p.Transcribe(ctx, audio)
		if err == nil {
			// A provider that returns empty text heard no words — treat it as
			// a retryable failure so fallbacks get a chance, and so the voice
			// loop can re-arm the mic instead of dispatching "" to the agent.
			if strings.TrimSpace(t.Text) == "" {
				errs = append(errs, fmt.Errorf("%s: %w", name, ErrEmptyTranscript))
				failed = append(failed, name)
				continue
			}
			t.Text = OneLine(t.Text)
			r.recordSTTHealth(ChainHealth{Attempted: true, OK: true, Used: name, Failed: failed})
			return t, nil
		}
		errs = append(errs, labelProviderErr(name, err))
		failed = append(failed, name)
	}

	r.recordSTTHealth(ChainHealth{Attempted: true, Failed: failed})
	return Transcript{}, fmt.Errorf("all STT providers failed: %w", errors.Join(errs...))
}

// Synthesize runs the TTS failover chain, mirroring Transcribe.
func (r *Registry) Synthesize(ctx context.Context, text string, opts SynthesisOptions) (AudioFormat, error) {
	// Utterance-scoped order: the provider that spoke the last sentence first,
	// and anything that already failed this reply left out. See tts_pin.go.
	chain := r.chainFor(ctx)
	if len(chain) == 0 {
		r.recordTTSHealth(ChainHealth{Attempted: true})
		return AudioFormat{}, errors.New("no TTS provider configured — run /blackbox setup")
	}

	var errs []error
	var failed []string
	for _, name := range chain {
		if err := ctx.Err(); err != nil {
			// Barge-in and Ctrl+C cancel here; that is not a provider failure.
			return AudioFormat{}, err
		}

		p, _ := r.TTSProvider(name)
		audio, err := p.Synthesize(ctx, text, opts)
		if err == nil {
			spokeWith(ctx, name)
			r.recordTTSHealth(ChainHealth{Attempted: true, OK: true, Used: name, Failed: failed})
			return audio, nil
		}
		retire(ctx, name)
		errs = append(errs, labelProviderErr(name, err))
		failed = append(failed, name)
	}

	r.recordTTSHealth(ChainHealth{Attempted: true, Failed: failed})
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

// SynthesizeStream starts streamed synthesis on the first chain provider that
// supports it, so playback can begin before generation finishes (P7.2c).
//
// It walks the SAME failover chain as Synthesize, skipping providers that do
// not implement StreamingTTSProvider. A provider that fails before returning a
// body is recorded and the walk continues; if no provider yields a stream the
// caller falls back to the buffered path, so streaming can only ever be faster,
// never a new way to be silent.
//
// Args: ctx, text, opts as Synthesize.
// Returns: the open stream and the provider name, or an error.
// Complexity: O(len(chain)) request attempts.
func (r *Registry) SynthesizeStream(
	ctx context.Context, text string, opts SynthesisOptions,
) (StreamedAudio, string, error) {
	chain := r.chainFor(ctx)
	if len(chain) == 0 {
		return StreamedAudio{}, "", errors.New("no TTS provider configured — run /blackbox setup")
	}

	var errs []error
	var failed []string
	for _, name := range chain {
		if err := ctx.Err(); err != nil {
			return StreamedAudio{}, "", err
		}

		p, ok := r.TTSProvider(name)
		if !ok {
			continue
		}
		sp, ok := p.(StreamingTTSProvider)
		if !ok {
			continue // buffered-only provider; not an error, so not "failed"
		}

		stream, err := sp.SynthesizeStream(ctx, text, opts)
		if err == nil {
			spokeWith(ctx, name)
			r.recordTTSHealth(ChainHealth{Attempted: true, OK: true, Used: name, Failed: failed})
			return stream, name, nil
		}
		// NOT retired here. A streaming failure only means this provider cannot
		// STREAM; speakOnce falls straight through to the buffered path, where
		// the same provider may well succeed. Retiring it on that basis would
		// change the voice over a capability difference rather than an outage.
		errs = append(errs, labelProviderErr(name, err))
		failed = append(failed, name)
	}

	// Deliberately no failure recorded here: every failing exit from this
	// function is followed by the buffered Synthesize path, whose outcome is the
	// authoritative one for "could Helix speak?".

	if len(errs) == 0 {
		return StreamedAudio{}, "", errNoStreamingTTS
	}
	return StreamedAudio{}, "", fmt.Errorf("streaming TTS unavailable: %w", errors.Join(errs...))
}

// errNoStreamingTTS marks "no provider in the chain streams" — an expected,
// non-error condition that simply selects the buffered path.
var errNoStreamingTTS = errors.New("no streaming TTS provider in chain")

// labelProviderErr ensures a chain error names its provider exactly once.
//
// Every shipped adapter already self-labels, so an unconditional prefix here
// produced "piper-local: piper-local: HTTP 403" in the failover message. But
// dropping the prefix outright would leave an adapter that forgets to label
// itself anonymous in a multi-provider failure — the one place the name matters
// most. Prefixing only when it is absent gets both.
func labelProviderErr(name string, err error) error {
	if err == nil {
		return nil
	}
	if strings.HasPrefix(err.Error(), name) {
		return err
	}
	return fmt.Errorf("%s: %w", name, err)
}
