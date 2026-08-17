// internal/providers/keystore.go
// Purpose: Secure file-based API key storage with 0600 permissions.
package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeyStore stores API keys in ~/.helix/secrets.json.
type KeyStore struct {
	path string
}

// NewKeyStore creates the default keystore.
func NewKeyStore() (*KeyStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}

	path := filepath.Join(home, ".helix", "secrets.json")
	return NewKeyStoreAt(path)
}

// NewKeyStoreAt creates a keystore at a specific path.
func NewKeyStoreAt(path string) (*KeyStore, error) {
	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create keystore directory: %w", err)
	}

	return &KeyStore{path: path}, nil
}

// Get returns a key from environment or keystore.
func (k *KeyStore) Get(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))

	if env := os.Getenv(k.envName(provider)); strings.TrimSpace(env) != "" {
		return strings.TrimSpace(env)
	}

	m, err := k.load()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(m[provider])
}

// Set saves a key to the keystore.
func (k *KeyStore) Set(provider, key string) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	key = strings.TrimSpace(key)

	if provider == "" {
		return fmt.Errorf("provider name is empty")
	}

	m, err := k.load()
	if err != nil {
		return err
	}

	if key == "" {
		delete(m, provider)
	} else {
		m[provider] = key
	}

	return k.save(m)
}

// Has reports whether a key exists.
func (k *KeyStore) Has(provider string) bool {
	return k.Get(provider) != ""
}

func (k *KeyStore) load() (map[string]string, error) {
	data, err := os.ReadFile(k.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}

		return nil, fmt.Errorf("read keystore: %w", err)
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse keystore: %w", err)
	}

	return m, nil
}

func (k *KeyStore) save(m map[string]string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal keystore: %w", err)
	}

	if err := os.WriteFile(k.path, data, 0o600); err != nil {
		return fmt.Errorf("write keystore: %w", err)
	}

	return nil
}

func (k *KeyStore) envName(provider string) string {
	switch provider {
	case "openai":
		return "OPENAI_API_KEY"
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "kimi":
		return "KIMI_API_KEY"
	case "qwen":
		return "QWEN_API_KEY"
	case "glm":
		return "GLM_API_KEY"
	case "xai":
		return "XAI_API_KEY"
	case "custom":
		return "CUSTOM_API_KEY"
	// Speech providers share the vendor account key of their chat sibling
	// (BlackBox internal/speech namespacing, ADR-005-era conventions).
	case "stt.openai", "tts.openai":
		return "OPENAI_API_KEY"
	case "stt.groq", "tts.groq":
		return "GROQ_API_KEY"
	case "stt.deepgram", "tts.deepgram":
		return "DEEPGRAM_API_KEY"
	case "stt.elevenlabs", "tts.elevenlabs":
		return "ELEVENLABS_API_KEY"
	default:
		return strings.ToUpper(strings.ReplaceAll(provider, ".", "_")) + "_API_KEY"
	}
}

// --- Misdirected-key detection ---------------------------------------------

// keyPrefixOwners maps an UNAMBIGUOUS key prefix to the provider that issues
// it. Order matters: longer, more specific prefixes come first.
//
// Deliberately absent: a bare "sk-". OpenAI, DeepSeek, Kimi and Qwen all use
// it, so it identifies nothing and would produce false alarms.
var keyPrefixOwners = []struct{ Prefix, Owner string }{
	{"sk-ant-", "anthropic"},
	{"xai-", "xai"},
	{"gsk_", "groq"},
}

// MisdirectedKey reports that a key was plainly issued by a DIFFERENT provider
// than the one it is being stored under.
//
// The check is deliberately negative-only: it never asserts what a valid key
// for a provider looks like — vendors change formats, and a positive rule would
// start rejecting good keys. It only fires when a prefix unambiguously belongs
// to somebody else, so unknown formats always pass.
//
// This exists because of a real incident: an `xai-` key (xAI, which serves
// Grok) was pasted into Helix's `groq` slot — GroqCloud, a different company
// one letter away — and the mistake only surfaced later as an auth failure on
// every transcription.
//
// Args:
//   - provider: registry name, optionally "stt."/"tts." namespaced.
//   - key: the pasted secret.
//
// Returns: the issuing provider and true when the key belongs elsewhere.
// Complexity: O(len(prefixes)).
func MisdirectedKey(provider, key string) (owner string, misdirected bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false
	}

	// Speech keys are namespaced but share the vendor account (see envName).
	base := strings.ToLower(strings.TrimSpace(provider))
	base = strings.TrimPrefix(strings.TrimPrefix(base, "stt."), "tts.")

	for _, e := range keyPrefixOwners {
		if !strings.HasPrefix(key, e.Prefix) {
			continue
		}
		if e.Owner == base {
			return e.Owner, false
		}
		return e.Owner, true
	}
	return "", false
}

// KeyOwnerHint returns a one-line explanation for a misdirected key, including
// the name collision that causes the most common mistake.
func KeyOwnerHint(provider, owner string) string {
	switch {
	case owner == "xai" && strings.Contains(provider, "groq"):
		return "xAI (which serves Grok) and Groq are different companies — " +
			"Groq keys come from console.groq.com, xAI keys from console.x.ai."
	case owner == "groq" && strings.Contains(provider, "xai"):
		return "Groq (GroqCloud) and xAI (Grok) are different companies — " +
			"xAI keys come from console.x.ai, Groq keys from console.groq.com."
	default:
		return "That key looks like it was issued by " + owner + "."
	}
}
