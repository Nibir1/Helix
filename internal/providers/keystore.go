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
	case "custom":
		return "CUSTOM_API_KEY"
	// Speech providers share the vendor account key of their chat sibling
	// (BlackBox internal/speech namespacing, ADR-005-era conventions).
	case "stt.openai", "tts.openai":
		return "OPENAI_API_KEY"
	case "stt.deepgram", "tts.deepgram":
		return "DEEPGRAM_API_KEY"
	case "stt.elevenlabs", "tts.elevenlabs":
		return "ELEVENLABS_API_KEY"
	default:
		return strings.ToUpper(strings.ReplaceAll(provider, ".", "_")) + "_API_KEY"
	}
}
