// internal/llamacpp/models.go
// Purpose: llama.cpp GGUF model catalog and downloader.
package llamacpp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Model is a downloadable GGUF model.
type Model struct {
	ID          string
	DisplayName string
	URL         string
	SHA256      string
	MinRAMGB    int
}

// RecommendedModels returns curated llama.cpp models.
func RecommendedModels() []Model {
	return []Model{
		{
			ID:          "tinyllama-1-1b",
			DisplayName: "TinyLlama 1.1B Q4",
			URL:         "https://huggingface.co/TheBloke/TinyLlama-1.1B-Chat-v1.0-GGUF/resolve/main/tinyllama-1.1b-chat-v1.0.Q4_0.gguf",
			SHA256:      "da3087fb14aede55fde6eb81a0e55e886810e43509ec82ecdc7aa5d62a03b556",
			MinRAMGB:    4,
		},
		{
			ID:          "llama2-7b",
			DisplayName: "Llama 2 7B Chat Q4",
			URL:         "https://huggingface.co/TheBloke/Llama-2-7B-Chat-GGUF/resolve/main/llama-2-7b-chat.Q4_0.gguf",
			SHA256:      "9958ee9b670594147b750bbc7d0540b928fa12dcc5dd4c58cc56ed2eb85e371b",
			MinRAMGB:    16,
		},
	}
}

// FindModel finds a recommended model by ID.
func FindModel(id string) (Model, bool) {
	id = strings.ToLower(strings.TrimSpace(id))

	for _, m := range RecommendedModels() {
		if strings.ToLower(m.ID) == id {
			return m, true
		}
	}

	return Model{}, false
}

// EnsureModel downloads and verifies a recommended model.
func EnsureModel(ctx context.Context, m Model) (string, error) {
	dir, err := modelsDir()
	if err != nil {
		return "", err
	}

	fileName := filepath.Base(m.URL)
	if fileName == "" || fileName == "/" || fileName == "." {
		fileName = m.ID + ".gguf"
	}

	path := filepath.Join(dir, fileName)

	if _, err := os.Stat(path); err == nil {
		if err := verifySHA(path, m.SHA256); err != nil {
			return "", err
		}

		return path, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()

	tmpPath := path + ".tmp"

	if err := downloadFile(ctx, m.URL, tmpPath); err != nil {
		return "", err
	}

	if err := verifySHA(tmpPath, m.SHA256); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("move downloaded model: %w", err)
	}

	return path, nil
}

// EnsureModelFromURL downloads an arbitrary GGUF URL.
func EnsureModelFromURL(ctx context.Context, url string) (string, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return "", fmt.Errorf("model URL is empty")
	}

	m := Model{
		ID:          strings.TrimSuffix(filepath.Base(url), ".gguf"),
		DisplayName: filepath.Base(url),
		URL:         url,
	}

	return EnsureModel(ctx, m)
}

func modelsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	dir := os.Getenv("HELIX_MODEL_DIR")
	if dir == "" {
		dir = filepath.Join(home, ".helix", "models")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create model directory: %w", err)
	}

	return dir, nil
}

func downloadFile(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create download file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("write download file: %w", err)
	}

	return nil
}

func verifySHA(path, expected string) error {
	expected = strings.ToLower(strings.TrimSpace(expected))
	if expected == "" {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open model for checksum: %w", err)
	}
	defer f.Close()

	h := sha256.New()

	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("read model for checksum: %w", err)
	}

	actual := strings.ToLower(hex.EncodeToString(h.Sum(nil)))
	if actual != expected {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
	}

	return nil
}
