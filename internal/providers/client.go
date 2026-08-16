// internal/providers/client.go
// Purpose: Shared HTTP client with timeouts, retries, and OpenAI SSE parsing.
package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPClient is a shared client for provider HTTP calls.
type HTTPClient struct {
	client       *http.Client
	streamClient *http.Client
	retries      int
	retryDelay   time.Duration
}

// NewHTTPClient creates a client with a JSON timeout and a no-timeout streaming client.
// Streaming lifetime is controlled by caller contexts.
func NewHTTPClient(timeout time.Duration) *HTTPClient {
	return &HTTPClient{
		client:       &http.Client{Timeout: timeout},
		streamClient: &http.Client{},
		retries:      3,
		retryDelay:   700 * time.Millisecond,
	}
}

// RawClient exposes the underlying *http.Client for plain GET probes (health
// checks) that do not fit the JSON/raw-body helpers. Shares the JSON timeout.
func (c *HTTPClient) RawClient() *http.Client {
	return c.client
}

// DoJSON performs a JSON request and returns the response body.
func (c *HTTPClient) DoJSON(
	ctx context.Context,
	method string,
	url string,
	headers map[string]string,
	body interface{},
) ([]byte, error) {
	resp, err := c.do(c.client, ctx, method, url, headers, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, apiErrorSnippet(data))
	}

	return data, nil
}

// DoStream performs a streaming POST request and parses OpenAI-compatible SSE.
func (c *HTTPClient) DoStream(
	ctx context.Context,
	url string,
	headers map[string]string,
	body interface{},
) (<-chan StreamChunk, error) {
	resp, err := c.do(c.streamClient, ctx, http.MethodPost, url, headers, body)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamChunk, 100)

	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()
		parseOpenAIStream(resp.Body, ch)
	}()

	return ch, nil
}

// DoRequest performs a raw request for providers with custom streaming formats.
func (c *HTTPClient) DoRequest(
	ctx context.Context,
	method string,
	url string,
	headers map[string]string,
	body interface{},
) (*http.Response, error) {
	return c.do(c.streamClient, ctx, method, url, headers, body)
}

// DoRaw performs a request with a pre-built raw body (multipart forms, binary
// audio payloads) and returns the full response body bytes. It shares the
// retry policy of DoJSON. The body is passed as bytes so retries can re-send
// it; use the non-retrying DoRequest for unbufferable streams.
func (c *HTTPClient) DoRaw(
	ctx context.Context,
	method string,
	url string,
	headers map[string]string,
	contentType string,
	body []byte,
) ([]byte, error) {
	resp, err := c.doRaw(ctx, method, url, headers, contentType, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, apiErrorSnippet(data))
	}

	return data, nil
}

// doRaw is the retrying transport under DoRaw, mirroring do() but with an
// explicit content type and a pre-marshaled body.
func (c *HTTPClient) doRaw(
	ctx context.Context,
	method string,
	url string,
	headers map[string]string,
	contentType string,
	body []byte,
) (*http.Response, error) {
	var lastErr error

	for attempt := 0; attempt <= c.retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)

			if !sleepCtx(ctx, c.retryDelay*time.Duration(attempt+1)) {
				return nil, ctx.Err()
			}

			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()

			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, apiErrorSnippet(errBody))

			if !sleepCtx(ctx, c.retryDelay*time.Duration(attempt+1)) {
				return nil, ctx.Err()
			}

			continue
		}

		if resp.StatusCode >= 400 {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()

			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, apiErrorSnippet(errBody))
		}

		return resp, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("request failed after retries")
	}

	return nil, lastErr
}

func (c *HTTPClient) do(
	client *http.Client,
	ctx context.Context,
	method string,
	url string,
	headers map[string]string,
	body interface{},
) (*http.Response, error) {
	var bodyBytes []byte

	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
	}

	var lastErr error

	for attempt := 0; attempt <= c.retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		for k, v := range headers {
			req.Header.Set(k, v)
		}

		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)

			if !sleepCtx(ctx, c.retryDelay*time.Duration(attempt+1)) {
				return nil, ctx.Err()
			}

			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()

			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, apiErrorSnippet(errBody))

			if !sleepCtx(ctx, c.retryDelay*time.Duration(attempt+1)) {
				return nil, ctx.Err()
			}

			continue
		}

		if resp.StatusCode >= 400 {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()

			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, apiErrorSnippet(errBody))
		}

		return resp, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("request failed after retries")
	}

	return nil, lastErr
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func parseOpenAIStream(r io.Reader, ch chan<- StreamChunk) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		if data == "[DONE]" {
			ch <- StreamChunk{Done: true}
			return
		}

		var parsed struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(data), &parsed); err != nil {
			continue
		}

		if len(parsed.Choices) == 0 {
			continue
		}

		if parsed.Choices[0].Delta.Content != "" {
			ch <- StreamChunk{Content: parsed.Choices[0].Delta.Content}
		}

		if parsed.Choices[0].FinishReason == "stop" {
			ch <- StreamChunk{Done: true}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamChunk{Error: fmt.Errorf("stream read error: %w", err)}
	}
}

func apiErrorSnippet(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 300 {
		return s[:300] + "..."
	}

	return s
}
