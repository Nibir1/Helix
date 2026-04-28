// internal/ai/openai.go

package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProviderType defines which AI backend Helix is using
type ProviderType string

const (
	ProviderLocal  ProviderType = "local"
	ProviderOpenAI ProviderType = "openai"
)

// Default OpenAI model for Helix
// You can change this if you want a different model later.
// const defaultOpenAIModel = "gpt-4o"
const defaultOpenAIModel = "gpt-4o"

var (
	currentProvider ProviderType = ProviderLocal
	openAIAPIKey    string
)

// SetProvider switches the active AI provider (local vs OpenAI)
func SetProvider(provider ProviderType) {
	currentProvider = provider
}

// GetProvider returns the currently active AI provider
func GetProvider() ProviderType {
	return currentProvider
}

// ConfigureOpenAIKey sets the in-memory OpenAI API key
func ConfigureOpenAIKey(key string) {
	openAIAPIKey = strings.TrimSpace(key)
}

// HasOpenAIKey returns true if an API key is configured in memory
func HasOpenAIKey() bool {
	return strings.TrimSpace(openAIAPIKey) != ""
}

// GetOpenAIKeyPath returns the path where we store the API key
func GetOpenAIKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback to current directory if home can't be resolved
		return ".openai.key"
	}
	return filepath.Join(home, ".helix", "openai.key")
}

// LoadOpenAIKeyFromDisk loads the key from disk (returns "" if not present)
func LoadOpenAIKeyFromDisk() (string, error) {
	path := GetOpenAIKeyPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read OpenAI key file: %w", err)
	}

	key := strings.TrimSpace(string(data))
	return key, nil
}

// SaveOpenAIKeyToDisk writes the key to disk, overwriting any existing value
func SaveOpenAIKeyToDisk(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("cannot save empty OpenAI API key")
	}

	path := GetOpenAIKeyPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// File should only contain ONE key
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		return fmt.Errorf("failed to write OpenAI key file: %w", err)
	}

	return nil
}

// runWithOpenAI sends the prompt to OpenAI's Chat Completions endpoint
func runWithOpenAI(prompt string, cfg ModelConfig) (string, error) {
	if strings.TrimSpace(openAIAPIKey) == "" {
		return "", fmt.Errorf("OpenAI API key not configured")
	}

	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	reqBody := struct {
		Model       string        `json:"model"`
		Messages    []chatMessage `json:"messages"`
		Temperature float32       `json:"temperature,omitempty"`
		MaxTokens   int           `json:"max_tokens,omitempty"`
	}{
		Model: defaultOpenAIModel,
		Messages: []chatMessage{
			{
				Role:    "user",
				Content: prompt,
			},
		},
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal OpenAI request: %w", err)
	}

	req, err := http.NewRequest(
		"POST",
		"https://api.openai.com/v1/chat/completions",
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create OpenAI request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+openAIAPIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 45 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("OpenAI request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("OpenAI API error: %s", resp.Status)
	}

	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return "", fmt.Errorf("failed to decode OpenAI response: %w", err)
	}

	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("OpenAI returned no choices")
	}

	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	//color.Cyan("Using OpenAI model: %s", defaultOpenAIModel)

	return content, nil
}
