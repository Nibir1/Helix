// internal/ollama/client.go
// Purpose: Native Ollama HTTP API client.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"helix/internal/providers"
)

// Client talks to a local Ollama daemon.
type Client struct {
	baseURL string
	client  *http.Client
}

// NewClient creates the default Ollama client.
func NewClient() *Client {
	return NewClientWithBaseURL("http://127.0.0.1:11434")
}

// NewClientWithBaseURL creates an Ollama client with a custom base URL.
func NewClientWithBaseURL(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client:  &http.Client{},
	}
}

// Health checks whether Ollama is responsive.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("create ollama health request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama health request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama health returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// ListModels lists installed Ollama models.
func (c *Client) ListModels(ctx context.Context) ([]providers.ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("create ollama list request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama list request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama list returned HTTP %d", resp.StatusCode)
	}

	var parsed struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("parse ollama models: %w", err)
	}

	out := make([]providers.ModelInfo, 0, len(parsed.Models))

	for _, m := range parsed.Models {
		out = append(out, providers.ModelInfo{
			ID:   m.Name,
			Name: m.Name,
		})
	}

	return out, nil
}

// PullModel pulls a model and reports progress.
//
// Args:
//   - ctx: cancellation/timeout context.
//   - model: Ollama model tag.
//   - progress: optional callback receiving (stage, completedBytes, totalBytes).
//     When totalBytes is 0, the UI should render an indeterminate spinner.
//
// Returns: error when the pull fails.
// Complexity: O(download size).
func (c *Client) PullModel(ctx context.Context, model string, progress func(status string, completed, total int64)) error {
	body := map[string]interface{}{
		"name":   model,
		"stream": true,
	}
	resp, err := c.post(ctx, "/api/pull", body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var event struct {
			Status    string `json:"status"`
			Error     string `json:"error"`
			Digest    string `json:"digest,omitempty"`
			Total     int64  `json:"total,omitempty"`
			Completed int64  `json:"completed,omitempty"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		if event.Error != "" {
			return fmt.Errorf("ollama pull error: %s", event.Error)
		}

		if progress != nil && event.Status != "" {
			progress(event.Status, event.Completed, event.Total)
		}

		if strings.Contains(strings.ToLower(event.Status), "success") {
			return nil
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("ollama pull stream error: %w", err)
	}

	// Some Ollama versions close the stream immediately after the final digest
	// without emitting an explicit "success" status. Reaching EOF cleanly is
	// treated as a successful pull.
	return nil
}

// Chat sends a native Ollama chat request.
func (c *Client) Chat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamChunk, error) {
	model := req.Model
	if model == "" {
		return nil, fmt.Errorf("ollama model is empty")
	}

	messages := make([]map[string]string, 0, len(req.Messages))

	for _, msg := range req.Messages {
		messages = append(messages, map[string]string{
			"role":    msg.Role,
			"content": msg.Content,
		})
	}

	body := map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   true,
	}

	options := map[string]interface{}{}

	if req.Temperature != nil {
		options["temperature"] = *req.Temperature
	}

	if req.MaxTokens > 0 {
		options["num_predict"] = req.MaxTokens
	}

	if len(options) > 0 {
		body["options"] = options
	}

	resp, err := c.post(ctx, "/api/chat", body)
	if err != nil {
		return nil, err
	}

	ch := make(chan providers.StreamChunk, 100)

	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()

		scanner := bufio.NewScanner(resp.Body)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 4*1024*1024)

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			var event struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				Done  bool   `json:"done"`
				Error string `json:"error"`
			}

			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue
			}

			if event.Error != "" {
				ch <- providers.StreamChunk{Error: fmt.Errorf("ollama chat error: %s", event.Error)}
				return
			}

			if event.Message.Content != "" {
				ch <- providers.StreamChunk{Content: event.Message.Content}
			}

			if event.Done {
				ch <- providers.StreamChunk{Done: true}
				return
			}
		}

		if err := scanner.Err(); err != nil {
			ch <- providers.StreamChunk{Error: fmt.Errorf("ollama chat stream error: %w", err)}
		}
	}()

	return ch, nil
}

func (c *Client) post(ctx context.Context, path string, body interface{}) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create ollama request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()

		return nil, fmt.Errorf("ollama returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	return resp, nil
}

// DefaultEmbeddingModel is the recommended local embedding model.
const DefaultEmbeddingModel = "nomic-embed-text"

// Embed produces vector embeddings for one or more texts using Ollama's
// native API. It prefers the modern batch endpoint /api/embed and falls back
// to the legacy single-prompt /api/embeddings endpoint on older daemons.
//
// Args:
//   - ctx: cancellation/timeout context.
//   - model: embedding model ("" → DefaultEmbeddingModel).
//   - texts: input strings.
//
// Returns: one embedding per input text, or an error.
// Complexity: O(len(texts)) HTTP round trips in the legacy path only.
func (c *Client) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if model == "" {
		model = DefaultEmbeddingModel
	}
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	// Modern batch endpoint.
	resp, err := c.post(ctx, "/api/embed", map[string]interface{}{
		"model": model,
		"input": texts,
	})
	if err == nil {
		var parsed struct {
			Embeddings [][]float32 `json:"embeddings"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&parsed)
		_ = resp.Body.Close()
		if decodeErr == nil && len(parsed.Embeddings) == len(texts) {
			return parsed.Embeddings, nil
		}
	}

	// Legacy per-prompt endpoint (older Ollama daemons).
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		resp, err := c.post(ctx, "/api/embeddings", map[string]interface{}{
			"model":  model,
			"prompt": text,
		})
		if err != nil {
			return nil, err
		}
		var parsed struct {
			Embedding []float32 `json:"embedding"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&parsed)
		_ = resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("parse ollama embedding: %w", decodeErr)
		}
		out = append(out, parsed.Embedding)
	}
	return out, nil
}
