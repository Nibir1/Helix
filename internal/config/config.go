// internal/config/config.go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/speech"
)

// Config holds runtime configuration and paths for Helix.
type Config struct {
	ModelDir              string                 `json:"model_dir"`
	ModelFile             string                 `json:"model_file"`
	HistoryPath           string                 `json:"history_path"`
	ConfigPath            string                 `json:"config_path"`
	OpenAIKeyPath         string                 `json:"openai_key_path"`
	Provider              string                 `json:"provider"`
	ProviderModel         string                 `json:"provider_model"`
	CustomProviderBaseURL string                 `json:"custom_provider_base_url"`
	UserPrefs             UserPrefs              `json:"user_preferences"`
	Speech                SpeechConfig           `json:"speech"`
	LLM                   LLMConfig              `json:"llm"`
	Daemon                DaemonConfig           `json:"daemon"`
	Vision                VisionConfig           `json:"vision"`
	Ambient               AmbientConfig          `json:"ambient"`
	Companion             CompanionConfig        `json:"companion"`
	VoiceLog              VoiceLogConfig         `json:"voice_log"`
	Update                UpdateConfig           `json:"update"`
	ModelConfig           ai.ModelConfig         `json:"model_config"`
	ExecuteConfig         commands.ExecuteConfig `json:"execute_config"`
}

// UserPrefs holds user preferences.
type UserPrefs struct {
	AutoConfirm  bool   `json:"auto_confirm"`
	ColorMode    string `json:"color_mode"`
	TypingEffect bool   `json:"typing_effect"`
	TypewriteAll bool   `json:"typewrite_all"`
	DefaultMode  string `json:"default_mode"`
	SafeMode     bool   `json:"safe_mode"`
	UserName     string `json:"user_name"`
	DebugMode    bool   `json:"debug_mode"`
	VoiceMode    bool   `json:"voice_mode"`   // BlackBox Phase 2: default input channel
	AgenticMode  bool   `json:"agentic_mode"` // iterative plan→act→observe→replan harness

	// AgenticSteps bounds harness iterations (0 → the agent's built-in
	// default). Persisted so /agentic steps <n> survives a restart.
	AgenticSteps int `json:"agentic_steps,omitempty"`

	// Permission is the approval posture: plan | cautious | ask | auto. It
	// replaces DefaultMode, which was written to config from the start and
	// never read by anything.
	Permission string `json:"permission,omitempty"`
}

// SpeechSTTConfig selects the speech-to-text provider chain (BlackBox §7).
type SpeechSTTConfig struct {
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	BaseURL   string   `json:"base_url"`
	Fallbacks []string `json:"fallbacks"`
	// Endpoints holds per-provider endpoint overrides, keyed by provider name.
	//
	// BaseURL applies only to the PRIMARY provider, which quietly made local
	// sidecars unusable as fallbacks: whisper-local picked as a fallback got its
	// reassigned port written into BaseURL — a field that belongs to whoever is
	// primary — so the probe still dialled the stale default and reported a
	// server that had actually started fine as "did not come up".
	Endpoints map[string]string `json:"endpoints,omitempty"`
	// StreamChunkMs is the streaming-STT capture chunk length (0 → 300ms).
	StreamChunkMs int `json:"stream_chunk_ms"`
}

// SpeechTTSConfig selects the text-to-speech provider chain.
type SpeechTTSConfig struct {
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	Voice     string   `json:"voice"`
	BaseURL   string   `json:"base_url"`
	Fallbacks []string `json:"fallbacks"`
	// Endpoints holds per-provider endpoint overrides. See SpeechSTTConfig.
	Endpoints map[string]string `json:"endpoints,omitempty"`
	// FirstByteMs is the TTS first-byte latency budget (0 → 800ms).
	FirstByteMs int `json:"first_byte_ms"`

	// ContextTurns is how many recent conversation turns are retained and sent
	// to a context-conditioned voice (CSM-1B). 0 disables it, which is the
	// default and the behavior of every other provider.
	//
	// Retention is memory-only and bounded — see internal/speech/conversation.go.
	// It is off by default because it means holding recent AUDIO in memory for
	// longer than a single turn, which is a privacy-relevant change even though
	// nothing reaches disk.
	ContextTurns int `json:"context_turns,omitempty"`

	// ContextMaxBytes caps retained audio (0 → 4 MiB).
	ContextMaxBytes int `json:"context_max_bytes,omitempty"`

	// BargeIn allows speaking over a reply to stop it, checked in the silence
	// between sentences.
	//
	// Off by default on purpose. The probe is AEC-free precisely because it only
	// listens while the speaker is idle, which buys two limitations worth
	// knowing before enabling it: you cannot cut in mid-sentence (a long one
	// plays to its end), and a loud room can stop a reply that nobody meant to
	// interrupt. Ctrl+C stops a reply instantly and needs none of this.
	BargeIn bool `json:"barge_in,omitempty"`
}

// SpeechWakeConfig controls wake-word listening (BlackBox §7).
type SpeechWakeConfig struct {
	Enabled           bool   `json:"enabled"`
	Engine            string `json:"engine"`             // "energy" (default) | "sidecar"
	SidecarURL        string `json:"sidecar_url"`        // sidecar engine endpoint
	Phrase            string `json:"phrase"`             // default "hey helix"
	SensitivityPreset string `json:"sensitivity_preset"` // strict | balanced | loose
	CooldownS         int    `json:"cooldown_s"`
	ChunkMs           int    `json:"chunk_ms"` // scanner chunk length
}

// SpeechConfig is the speech subsystem section of ~/.helix/config.json.
type SpeechConfig struct {
	STT      SpeechSTTConfig  `json:"stt"`
	TTS      SpeechTTSConfig  `json:"tts"`
	WakeWord SpeechWakeConfig `json:"wake_word"`
}

// Runtime converts the persisted section into the speech package's runtime
// config.
//
// This lives here, once, because it existed twice and the copies drifted: the
// interactive shell converted Endpoints and the daemon's inline copy did not,
// so a sidecar the wizard had moved to a free port was dialled correctly by
// `helix` and at its stale default by `helix daemon` — the third appearance of
// the same Endpoints-dropped-at-the-boundary bug the dev log records twice.
// Two callers, one conversion; a field added to the config now reaches both or
// neither.
func (sc SpeechConfig) Runtime() speech.Config {
	return speech.Config{
		STT: speech.STTConfig{
			Provider:      sc.STT.Provider,
			Model:         sc.STT.Model,
			BaseURL:       sc.STT.BaseURL,
			Endpoints:     sc.STT.Endpoints,
			Fallbacks:     sc.STT.Fallbacks,
			StreamChunkMs: sc.STT.StreamChunkMs,
		},
		TTS: speech.TTSConfig{
			Provider:    sc.TTS.Provider,
			Model:       sc.TTS.Model,
			Voice:       sc.TTS.Voice,
			BaseURL:     sc.TTS.BaseURL,
			Endpoints:   sc.TTS.Endpoints,
			Fallbacks:   sc.TTS.Fallbacks,
			FirstByteMs: sc.TTS.FirstByteMs,
		},
	}
}

// ContextEnabled reports whether conversational context retention is on.
func (sc SpeechConfig) ContextEnabled() bool { return sc.TTS.ContextTurns > 0 }

// LLMFallbackConfig controls automatic cloud→local language-model failover
// (BlackBox P11.2). It is the brain's counterpart to the speech chain's
// local-first degradation.
type LLMFallbackConfig struct {
	// Enabled is a pointer because this is the project's first setting whose
	// default is TRUE: with a plain bool, an absent `llm` section and an
	// explicit `"enabled": false` are both the zero value, so a user could not
	// turn the feature off. nil means "not specified" → default true.
	Enabled  *bool  `json:"enabled,omitempty"`
	Provider string `json:"provider"` // local runtime: "ollama" (default) | "llamacpp"
	Model    string `json:"model"`    // "" → the provider's default model

	// Threshold is how many consecutive availability failures trip the switch
	// (0 → 2).
	Threshold int `json:"threshold"`

	// RetryAfterS is how long to stay local before probing the cloud provider
	// again (0 → 120s).
	RetryAfterS int `json:"retry_after_s"`

	// EnsureReady lets `helix daemon` PULL the fallback model at startup when
	// it is missing (P11.3). Default false: a model pull is a multi-gigabyte
	// download, and guardrail §12 #1 makes downloads consent-gated. When false
	// the daemon still VERIFIES the model and journals a warning if absent —
	// the useful half of the check, without the surprise download.
	EnsureReady bool `json:"ensure_ready"`
}

// LLMConfig groups language-model runtime settings (BlackBox Phase 11 §7).
type LLMConfig struct {
	// LlamaCppURL is a user-managed llama-server OpenAI-compatible base URL
	// ("" → HELIX_LLAMACPP_URL → http://127.0.0.1:8080/v1).
	LlamaCppURL string `json:"llamacpp_url"`

	Fallback LLMFallbackConfig `json:"fallback"`
}

// LLMDefaults returns the default language-model resilience settings.
//
// Fallback is enabled by default, which is safe because arming it is not the
// same as using it: the breaker health-checks the local provider before every
// switch, so on a machine with no Ollama and no llama-server it never engages
// and behavior is byte-identical to having it off.
func LLMDefaults() LLMConfig {
	on := true
	return LLMConfig{
		Fallback: LLMFallbackConfig{
			Enabled:     &on,
			Provider:    "ollama",
			Threshold:   2,
			RetryAfterS: 120,
			EnsureReady: false,
		},
	}
}

// AIFallback converts the persisted section into the ai package's failover
// settings, applying defaults for unset numeric fields. Both the interactive
// shell and the daemon build their fallback from here so the two paths cannot
// drift apart.
func (cfg *Config) AIFallback() ai.LocalFallback {
	f := cfg.LLM.Fallback
	provider := f.Provider
	if provider == "" {
		provider = LLMDefaults().Fallback.Provider
	}
	retry := time.Duration(f.RetryAfterS) * time.Second
	if f.RetryAfterS <= 0 {
		retry = time.Duration(LLMDefaults().Fallback.RetryAfterS) * time.Second
	}
	return ai.LocalFallback{
		Enabled:    f.FallbackEnabled(),
		Provider:   provider,
		Model:      f.Model,
		Threshold:  f.Threshold,
		RetryAfter: retry,
	}
}

// FallbackEnabled reports the effective enable flag (unset → true).
func (f LLMFallbackConfig) FallbackEnabled() bool {
	if f.Enabled == nil {
		return true
	}
	return *f.Enabled
}

// mergeLLM layers a partial `llm` section from the config file over the
// defaults, field-wise — the same discipline mergeWakeWord established after an
// empty section silently wiped the wake-word defaults.
func mergeLLM(dst *LLMConfig, src LLMConfig) {
	if src.LlamaCppURL != "" {
		dst.LlamaCppURL = src.LlamaCppURL
	}
	if src.Fallback.Enabled != nil {
		v := *src.Fallback.Enabled
		dst.Fallback.Enabled = &v
	}
	if src.Fallback.Provider != "" {
		dst.Fallback.Provider = src.Fallback.Provider
	}
	if src.Fallback.Model != "" {
		dst.Fallback.Model = src.Fallback.Model
	}
	if src.Fallback.Threshold > 0 {
		dst.Fallback.Threshold = src.Fallback.Threshold
	}
	if src.Fallback.RetryAfterS > 0 {
		dst.Fallback.RetryAfterS = src.Fallback.RetryAfterS
	}
	dst.Fallback.EnsureReady = src.Fallback.EnsureReady
}

// DaemonConfig controls the BlackBox Phase 4 Living AI service (§7). The
// interaction journal is always on in v1 (safe and /purge-able); Autostart is
// honored by `helix daemon install` (RunAtLoad / KeepAlive / Restart=on-failure).
type DaemonConfig struct {
	Autostart        bool `json:"autostart"`          // service auto-start hint
	SessionTurns     int  `json:"session_turns"`      // conversation ring-buffer size (0 → default)
	BreakReminderMin int  `json:"break_reminder_min"` // focus-break reminder cadence; 0 = off (default)
}

// VisionConfig controls BlackBox Phase 5 opt-in camera perception (§7).
// Enabled defaults to false — strict opt-in is a privacy guarantee.
type VisionConfig struct {
	Enabled          bool   `json:"enabled"`             // master opt-in switch
	Provider         string `json:"provider"`            // dedicated vision LLM ("" → active chat provider)
	Model            string `json:"model"`               // dedicated vision MODEL ("" → the provider's usual choice)
	MaxFramesPerTurn int    `json:"max_frames_per_turn"` // default 1
}

// AmbientConfig controls BlackBox Phase 6 (optional) auditory awareness (§7).
// Disabled by default; ResponseMode is "vocal" | "log" | "ignore".
type AmbientConfig struct {
	Enabled      bool            `json:"enabled"`
	Sensitivity  float64         `json:"sensitivity"` // 0..1; 0 → package default (0.5)
	ResponseMode string          `json:"response_mode"`
	Categories   map[string]bool `json:"categories"` // e.g. {"loud_noise": true}
}

// CompanionConfig controls Helix's initiative in live mode (/blackbox on):
// the periodic look at the scene, and whether Helix may speak about it without
// being asked.
//
// Every field is a cost or a courtesy control. IntervalS bounds how often a
// vision model runs at all; ChangeThreshold decides whether a captured frame is
// different enough to be worth a model call (an unchanged room costs nothing);
// CooldownS bounds how often Helix may volunteer a remark once it has one.
type CompanionConfig struct {
	Enabled         bool    `json:"enabled"`          // speak up unprompted in live mode
	IntervalS       int     `json:"interval_s"`       // seconds between scene looks; 0 → default
	CooldownS       int     `json:"cooldown_s"`       // minimum gap between spoken remarks; 0 → default
	ChangeThreshold float64 `json:"change_threshold"` // 0..1 frame difference needed to spend a model call
}

// CompanionDefaults are tuned for a present-but-not-exhausting companion on a
// LOCAL vision model, where each look costs seconds of compute rather than
// money. Both numbers are deliberately conservative relative to how fast a
// camera could be sampled: the limit that matters is not the camera, it is how
// often a person wants to be spoken to.
func CompanionDefaults() CompanionConfig {
	return CompanionConfig{
		Enabled:         true,
		IntervalS:       20,
		CooldownS:       45,
		ChangeThreshold: 0.08,
	}
}

// VoiceLogConfig controls the opt-in voice interaction log (BlackBox P2.8).
//
// Enabled is false by default and that default is a privacy guarantee, not a
// convenience: with it off, no directory and no file exist at all (threat V5).
// Turning it on stores what Helix HEARD and SAID as text — never audio, since
// captured clips are deleted the moment they are read.
//
// The zero value is therefore the correct default, which is why LoadPreferences
// copies this section wholesale instead of merging field-wise: an absent
// section must mean "off", and there is no default worth protecting from an
// empty one.
type VoiceLogConfig struct {
	Enabled bool `json:"enabled"`

	// MaxBytes bounds the active log file before it rotates (0 → 1 MiB), and
	// KeepFiles is how many rotated generations survive (0 → 3). Both exist
	// because an always-on assistant on a Raspberry Pi shares its filesystem
	// with everything else on the board.
	MaxBytes  int64 `json:"max_bytes,omitempty"`
	KeepFiles int   `json:"keep_files,omitempty"`
}

// Interval, Cooldown, and Threshold resolve a possibly-zero field to its
// default, so callers never have to repeat the fallback.
func (c CompanionConfig) Interval() time.Duration {
	if c.IntervalS <= 0 {
		return time.Duration(CompanionDefaults().IntervalS) * time.Second
	}
	return time.Duration(c.IntervalS) * time.Second
}

func (c CompanionConfig) Cooldown() time.Duration {
	if c.CooldownS <= 0 {
		return time.Duration(CompanionDefaults().CooldownS) * time.Second
	}
	return time.Duration(c.CooldownS) * time.Second
}

func (c CompanionConfig) Threshold() float64 {
	if c.ChangeThreshold <= 0 || c.ChangeThreshold > 1 {
		return CompanionDefaults().ChangeThreshold
	}
	return c.ChangeThreshold
}

// DefaultConfig returns sane default paths for Helix.
func DefaultConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	modelDir := os.Getenv("HELIX_MODEL_DIR")
	if modelDir == "" {
		modelDir = filepath.Join(home, ".helix", "models")
	}
	configDir := filepath.Join(home, ".helix")
	openAIKeyPath := filepath.Join(configDir, "openai_api_key")
	modelFile := filepath.Join(modelDir, "gemma-4-e2b-it-Q4_K_M.gguf")
	cfg := &Config{
		ModelDir:              modelDir,
		ModelFile:             modelFile,
		HistoryPath:           filepath.Join(home, ".helix_history"),
		ConfigPath:            filepath.Join(configDir, "config.json"),
		OpenAIKeyPath:         openAIKeyPath,
		Provider:              "",
		ProviderModel:         "",
		CustomProviderBaseURL: "",
		UserPrefs: UserPrefs{
			AutoConfirm:  false,
			ColorMode:    "auto",
			TypingEffect: true,
			TypewriteAll: false, // Phase 15: Default to off (AI only)
			DefaultMode:  "ask",
			SafeMode:     true,
			UserName:     "",
			DebugMode:    false,
		},
		LLM:           LLMDefaults(),
		ModelConfig:   ai.DefaultModelConfig(),
		ExecuteConfig: commands.DefaultExecuteConfig(),
		Companion:     CompanionDefaults(),
		Update:        UpdateDefaults(),
	}
	_ = cfg.LoadPreferences()
	return cfg, nil
}

// EnsureModelDir ensures that the model directory exists.
func (cfg *Config) EnsureModelDir() error {
	return os.MkdirAll(cfg.ModelDir, 0o755)
}

// EnsureConfigDir ensures that the config directory exists.
func (cfg *Config) EnsureConfigDir() error {
	return os.MkdirAll(filepath.Dir(cfg.ConfigPath), 0o755)
}

// LoadPreferences loads user preferences from config file.
func (cfg *Config) LoadPreferences() error {
	data, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		return nil
	}
	var prefs Config
	if err := json.Unmarshal(data, &prefs); err != nil {
		return fmt.Errorf("error parsing config file: %w", err)
	}
	cfg.UserPrefs = prefs.UserPrefs
	if prefs.ModelConfig.MaxTokens > 0 {
		cfg.ModelConfig = prefs.ModelConfig
	}
	if prefs.Provider != "" {
		cfg.Provider = prefs.Provider
	}
	if prefs.ProviderModel != "" {
		cfg.ProviderModel = prefs.ProviderModel
	}
	if prefs.CustomProviderBaseURL != "" {
		cfg.CustomProviderBaseURL = prefs.CustomProviderBaseURL
	}
	if prefs.Speech.STT.Provider != "" || prefs.Speech.TTS.Provider != "" {
		cfg.Speech = prefs.Speech
	}
	// Wake word is ALWAYS field-wise merged (not gated on STT/TTS providers):
	// a config that sets only the wake_word section must still load, and
	// empty values in the file must never wipe the defaults — otherwise
	// enabling hands-free would silently produce a broken ""-phrase detector.
	mergeWakeWord(&cfg.Speech.WakeWord, prefs.Speech.WakeWord)
	applyWakeWordDefaults(&cfg.Speech.WakeWord)
	// Field-wise so a partial or absent `llm` section keeps the defaults
	// (fallback armed, Ollama, threshold 2).
	mergeLLM(&cfg.LLM, prefs.LLM)
	// Field-wise so unset daemon keys never clobber defaults.
	if prefs.Daemon.SessionTurns > 0 {
		cfg.Daemon.SessionTurns = prefs.Daemon.SessionTurns
	}
	if prefs.Daemon.BreakReminderMin > 0 {
		cfg.Daemon.BreakReminderMin = prefs.Daemon.BreakReminderMin
	}
	cfg.Daemon.Autostart = prefs.Daemon.Autostart
	cfg.Vision.Enabled = prefs.Vision.Enabled
	if prefs.Vision.Provider != "" {
		cfg.Vision.Provider = prefs.Vision.Provider
	}
	if prefs.Vision.Model != "" {
		cfg.Vision.Model = prefs.Vision.Model
	}
	if prefs.Vision.MaxFramesPerTurn > 0 {
		cfg.Vision.MaxFramesPerTurn = prefs.Vision.MaxFramesPerTurn
	}
	// Ambient's zero value is the correct default (disabled); copy wholesale.
	cfg.Ambient = prefs.Ambient
	// Same for the voice log: absent section means off, which is the default
	// we want. See VoiceLogConfig for why this one must NOT be field-merged.
	cfg.VoiceLog = prefs.VoiceLog
	// Companion's zero value is NOT its default (Enabled defaults to true), so a
	// config file written before this section existed must not silently mean
	// "off". Absence is detected on the whole struct, not per field.
	if prefs.Companion == (CompanionConfig{}) {
		cfg.Companion = CompanionDefaults()
	} else {
		cfg.Companion = prefs.Companion
	}
	// Same shape of problem as Companion: the update section's zero value is
	// not its default. A config written before self-update existed has an
	// absent section, and reading that as channel="" / repo="" would leave the
	// updater pointed at nothing rather than at the project.
	mergeUpdate(&cfg.Update, prefs.Update)
	return nil
}

// UpdateConfig controls how Helix updates itself (`/reboot`).
type UpdateConfig struct {
	// Channel is "auto", "github", "local" or "off".
	//
	// Default "auto": check published releases AND locally built binaries, and
	// prefer whichever is newer. It is not "github", because someone running a
	// source build in a checkout is the person most likely to have a newer
	// binary sitting in dist/, and a channel that ignored it would tell them
	// they were up to date while a build they had just made went unnoticed.
	Channel string `json:"channel"`

	// Repo is the "owner/name" releases are read from.
	//
	// Configurable so a fork can update from itself, and pinned to one host by
	// the updater regardless of what is written here — the field chooses a
	// repository, never a server.
	Repo string `json:"repo"`

	// Check controls whether /reboot looks for an update at all.
	//
	// Separate from Channel so "do not check on every restart" and "never
	// update" stay different settings: a restart is something you may want to
	// be instant, without giving up `/reboot check`.
	Check bool `json:"check"`

	// LocalPaths overrides where a locally built binary is looked for.
	LocalPaths []string `json:"local_paths,omitempty"`
}

// UpdateDefaults returns the shipped update policy.
//
// Checking is ON and installing is never automatic. The check costs one HTTPS
// request on a restart the user asked for; the install always requires a typed
// confirmation, because replacing the program someone is running is not
// something to do because they wanted a fresh process.
func UpdateDefaults() UpdateConfig {
	return UpdateConfig{
		Channel: "auto",
		Repo:    "Nibir1/Helix",
		Check:   true,
	}
}

// mergeUpdate applies a config file's update section over the defaults.
func mergeUpdate(dst *UpdateConfig, src UpdateConfig) {
	if src.Channel != "" {
		dst.Channel = src.Channel
	}
	if src.Repo != "" {
		dst.Repo = src.Repo
	}
	if len(src.LocalPaths) > 0 {
		dst.LocalPaths = src.LocalPaths
	}
	// Check is a bool whose default is true, so "absent" and "false" look
	// identical in the decoded struct. Absence of the WHOLE section means
	// defaults; a section that says anything at all is taken at its word,
	// including its silence about Check.
	if src.Channel != "" || src.Repo != "" || len(src.LocalPaths) > 0 || src.Check {
		dst.Check = src.Check
	}
}

// WakeWordDefaults are the safe, everywhere-works defaults for hands-free
// detection (ADR-002 honesty: energy onset is the default engine).
func WakeWordDefaults() SpeechWakeConfig {
	return SpeechWakeConfig{
		Enabled:           false, // strict opt-in (privacy)
		Engine:            "energy",
		Phrase:            "hey helix",
		SensitivityPreset: "balanced",
		CooldownS:         2,
		ChunkMs:           1500,
	}
}

// mergeWakeWord copies non-empty wake-word fields from src into dst so a
// partial section in the file layers over the defaults instead of replacing
// them. Enabled only flips to true from the file (default is off, so an
// explicit disable needs no special case).
func mergeWakeWord(dst *SpeechWakeConfig, src SpeechWakeConfig) {
	if src.Enabled {
		dst.Enabled = true
	}
	if src.Engine != "" {
		dst.Engine = src.Engine
	}
	if src.Phrase != "" {
		dst.Phrase = src.Phrase
	}
	if src.SensitivityPreset != "" {
		dst.SensitivityPreset = src.SensitivityPreset
	}
	if src.SidecarURL != "" {
		dst.SidecarURL = src.SidecarURL
	}
	if src.CooldownS > 0 {
		dst.CooldownS = src.CooldownS
	}
	if src.ChunkMs > 0 {
		dst.ChunkMs = src.ChunkMs
	}
}

// applyWakeWordDefaults fills empty wake-word fields with the defaults.
func applyWakeWordDefaults(w *SpeechWakeConfig) {
	def := WakeWordDefaults()
	if w.Engine == "" {
		w.Engine = def.Engine
	}
	if w.Phrase == "" {
		w.Phrase = def.Phrase
	}
	if w.SensitivityPreset == "" {
		w.SensitivityPreset = def.SensitivityPreset
	}
	if w.CooldownS <= 0 {
		w.CooldownS = def.CooldownS
	}
	if w.ChunkMs <= 0 {
		w.ChunkMs = def.ChunkMs
	}
}

// SavePreferences saves user preferences to config file.
func (cfg *Config) SavePreferences() error {
	if err := cfg.EnsureConfigDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfg.ConfigPath, data, 0o644)
}

// Versioning and model metadata.
const (
	// HelixVersion is the fallback version for builds that do not stamp one.
	// Official releases override it via goreleaser ldflags
	// (-X helix/internal/config.HelixVersion={{.Version}}), but `make current`,
	// `go install` and a plain `go build` do not — so this constant is what a
	// source build reports, and it has to track the tag or /version lies about
	// which Helix you are running.
	HelixVersion  = "1.5.0"
	ModelName     = "TinyLlama-1.1B-Chat-v1.0-GGUF"
	ModelURL      = "https://huggingface.co/TheBloke/TinyLlama-1.1B-Chat-v1.0-GGUF/resolve/main/tinyllama-1.1b-chat-v1.0.Q4_0.gguf"
	ModelChecksum = "da3087fb14aede55fde6eb81a0e55e886810e43509ec82ecdc7aa5d62a03b556"
)
