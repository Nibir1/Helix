// internal/providers/model_not_found_test.go
// Purpose: a retired model must be distinguishable from a bad URL and a bad
// key, using the wordings vendors actually send.
package providers

import (
	"errors"
	"fmt"
	"testing"
)

// The three shapes that exist in the wild. A status-code-only check would
// catch one of them.
func TestIsModelNotFoundAcrossVendors(t *testing.T) {
	for name, err := range map[string]error{
		"openai 404":    &StatusError{Code: 404, Snippet: `{"error":{"code":"model_not_found","message":"The model 'gpt-4' does not exist"}}`},
		"deepseek 400":  &StatusError{Code: 400, Snippet: `{"error":{"message":"Model Not Exist"}}`},
		"anthropic 404": &StatusError{Code: 404, Snippet: `{"type":"error","error":{"type":"not_found_error","message":"model: unknown model"}}`},
		"gemini compat": &StatusError{Code: 400, Snippet: `models/gemini-1.0 is not found or invalid model`},
		"wrapped":       fmt.Errorf("chat request: %w", &StatusError{Code: 404, Snippet: "model_not_found"}),
	} {
		if !IsModelNotFound(err) {
			t.Errorf("%s: not recognised as a retired model: %v", name, err)
		}
	}
}

// The errors it must NOT claim. Treating any of these as a retired model would
// make self-repair overwrite a working configuration.
func TestIsModelNotFoundRejectsOtherFailures(t *testing.T) {
	for name, err := range map[string]error{
		"bad key":      &StatusError{Code: 401, Snippet: "Incorrect API key provided"},
		"forbidden":    &StatusError{Code: 403, Snippet: "insufficient permissions"},
		"rate limited": &StatusError{Code: 429, Snippet: "rate limit reached"},
		"server error": &StatusError{Code: 500, Snippet: "internal server error"},
		"wrong route":  &StatusError{Code: 404, Snippet: `{"error":"Not Found"}`},
		"plain error":  errors.New("connection refused"),
		"nil":          nil,
	} {
		if IsModelNotFound(err) {
			t.Errorf("%s: wrongly read as a retired model: %v", name, err)
		}
	}
}

// A bare 404 with no model wording is a ROUTE problem, and saying otherwise
// would send the reader to /model list when their base URL is wrong.
func TestBare404IsNotAModelProblem(t *testing.T) {
	err := &StatusError{Code: 404, Snippet: "404 page not found"}
	if IsModelNotFound(err) {
		t.Error("a bare 404 was read as a retired model")
	}
	if !IsNotFound(err) {
		t.Error("IsNotFound stopped recognising a 404")
	}
}
