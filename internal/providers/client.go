// internal/providers/client.go
// Purpose: Shared HTTP client with timeouts, retries, and OpenAI SSE parsing.
package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// StatusError is an HTTP response with a >= 400 status.
//
// It exists so callers can branch on the CODE instead of substring-matching the
// message. Two places need that and were doing it by hand: llama.cpp's
// diagnosis ("did something answer, or is nothing listening?") and the local
// sidecar adapters, which discover which route a server exposes by trying one
// and treating 404 as "wrong route" rather than "server is broken".
//
// The Error() text is byte-identical to the fmt.Errorf it replaced, so existing
// message-based checks keep working while new code uses errors.As.
type StatusError struct {
	Code    int
	Snippet string
}

// Error renders the status and a bounded body snippet.
func (e *StatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Code, e.Snippet)
}

// StatusCode extracts an HTTP status from an error chain, reporting whether one
// was present.
func StatusCode(err error) (int, bool) {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code, true
	}
	return 0, false
}

// IsNotFound reports whether err is a 404 — the signal that a route does not
// exist on an otherwise live server.
func IsNotFound(err error) bool {
	code, ok := StatusCode(err)
	return ok && code == http.StatusNotFound
}

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

// WithoutRetries returns a shallow copy that attempts each request exactly once.
//
// For a LOCAL health probe, retrying is actively harmful. A refused connection
// on 127.0.0.1 is instant and definitive — nothing is listening — but three
// attempts with backoff turn that into a context deadline, and a deadline says
// nothing about the cause. The reported symptom becomes "context deadline
// exceeded" where it should have been "nothing is listening on 127.0.0.1:8080",
// which is the difference between a diagnosis and a shrug.
//
// Retries stay the default everywhere else: a flaky cloud provider is exactly
// what they are for.
func (c *HTTPClient) WithoutRetries() *HTTPClient {
	clone := *c
	clone.retries = 0
	return &clone
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
		return nil, &StatusError{Code: resp.StatusCode, Snippet: apiErrorSnippet(data)}
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
		parseOpenAIStream(ctx, resp.Body, ch)
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
		return nil, &StatusError{Code: resp.StatusCode, Snippet: apiErrorSnippet(data)}
	}

	return data, nil
}

// DoRawWithHeaders is DoRaw plus the response headers.
//
// Some servers answer a question in a header rather than the body — notably
// whether an optional request feature was actually honored. Serde-based servers
// ignore unknown JSON fields by default, so "the request was accepted" does not
// mean "the field was used", and a client that cannot read the response headers
// cannot tell the difference.
func (c *HTTPClient) DoRawWithHeaders(
	ctx context.Context,
	method string,
	url string,
	headers map[string]string,
	contentType string,
	body []byte,
) ([]byte, http.Header, error) {
	resp, err := c.doRaw(ctx, method, url, headers, contentType, body)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, resp.Header, &StatusError{Code: resp.StatusCode, Snippet: apiErrorSnippet(data)}
	}
	return data, resp.Header, nil
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

			lastErr = &StatusError{Code: resp.StatusCode, Snippet: apiErrorSnippet(errBody)}

			if !sleepCtx(ctx, c.retryDelay*time.Duration(attempt+1)) {
				return nil, ctx.Err()
			}

			continue
		}

		if resp.StatusCode >= 400 {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()

			return nil, &StatusError{Code: resp.StatusCode, Snippet: apiErrorSnippet(errBody)}
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

			lastErr = &StatusError{Code: resp.StatusCode, Snippet: apiErrorSnippet(errBody)}

			if !sleepCtx(ctx, c.retryDelay*time.Duration(attempt+1)) {
				return nil, ctx.Err()
			}

			continue
		}

		if resp.StatusCode >= 400 {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()

			return nil, &StatusError{Code: resp.StatusCode, Snippet: apiErrorSnippet(errBody)}
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

func parseOpenAIStream(ctx context.Context, r io.Reader, ch chan<- StreamChunk) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	// send delivers a chunk but returns false if the consumer abandoned the
	// stream (ctx cancelled/timed out). Without this, a full 100-chunk buffer
	// blocks the producer forever after CollectChat returns on ctx.Done(),
	// leaking the goroutine and its HTTP body — costly in the long-lived daemon.
	send := func(c StreamChunk) bool {
		select {
		case ch <- c:
			return true
		case <-ctx.Done():
			return false
		}
	}

	// Tool calls arrive fragmented across many SSE frames: the first carries
	// the id and function name, later ones append slices of the JSON argument
	// string, all keyed by index. They are accumulated here and emitted whole
	// on the terminating frame, so consumers never see partial JSON (P8.7).
	toolAcc := NewToolCallAccumulator()

	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := strings.TrimSpace(scanner.Text())

		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		if data == "[DONE]" {
			send(StreamChunk{Done: true, ToolCalls: toolAcc.Assemble()})
			return
		}

		var parsed struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
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

		for _, tc := range parsed.Choices[0].Delta.ToolCalls {
			toolAcc.Add(tc.Index, tc.ID, tc.Function.Name, tc.Function.Arguments)
		}

		if parsed.Choices[0].Delta.Content != "" {
			if !send(StreamChunk{Content: parsed.Choices[0].Delta.Content}) {
				return
			}
		}

		// "tool_calls" is the finish reason when the model chose to call a
		// tool instead of answering; treating it like "stop" (and shipping the
		// accumulated calls) ends the stream correctly either way.
		if fr := parsed.Choices[0].FinishReason; fr == "stop" || fr == "tool_calls" {
			send(StreamChunk{Done: true, ToolCalls: toolAcc.Assemble()})
			return
		}
	}

	if err := scanner.Err(); err != nil {
		send(StreamChunk{Error: fmt.Errorf("stream read error: %w", err)})
	}
}

func apiErrorSnippet(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 300 {
		return s[:300] + "..."
	}

	return s
}
