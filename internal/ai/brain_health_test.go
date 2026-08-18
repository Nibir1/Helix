// internal/ai/brain_health_test.go
// Purpose: the shell must not report a healthy grid while its brain is
// unreachable. This pins the recording side; cmd/helix pins the rendering.
package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// resetBrainHealth clears the recorded state for one test.
func resetBrainHealth(t *testing.T) {
	t.Helper()
	brainMu.Lock()
	prev := brainHealth
	brainHealth = BrainHealth{}
	brainMu.Unlock()
	t.Cleanup(func() {
		brainMu.Lock()
		brainHealth = prev
		brainMu.Unlock()
	})
}

// A provider nobody has called or probed is unknown, not broken — otherwise
// every session would start by declaring itself degraded.
func TestBrainHealthZeroValueIsNotDegraded(t *testing.T) {
	resetBrainHealth(t)
	h := LastBrainHealth()
	if h.Degraded() {
		t.Errorf("the zero value must read as unknown, got %+v", h)
	}
	if h.Reason() != "" {
		t.Errorf("reason = %q, want empty", h.Reason())
	}
}

// The QA case: the startup probe knew llama-server was refusing connections, and
// the status line still said CLEAR because that fact was printed and discarded.
func TestNoteProviderUnreachableIsRemembered(t *testing.T) {
	resetBrainHealth(t)
	NoteProviderUnreachable("llamacpp",
		`request failed: Get "http://127.0.0.1:8080/v1/models": dial tcp 127.0.0.1:8080: connect: connection refused`)

	h := LastBrainHealth()
	if !h.Degraded() {
		t.Fatal("a refused connection must be remembered as degraded")
	}
	reason := h.Reason()
	if !strings.Contains(reason, "llamacpp") {
		t.Errorf("reason should name the provider, got %q", reason)
	}
	if !strings.Contains(reason, "connection refused") {
		t.Errorf("reason should classify the cause, got %q", reason)
	}
	// The status line cannot wrap, so the reason must not drag a URL into it —
	// interpolating the raw error produced a 130-column line with the address cut
	// mid-URL, the exact /voice-status defect.
	if strings.Contains(reason, "http://") {
		t.Errorf("the raw URL must not reach the one-line status: %q", reason)
	}
	if strings.Contains(reason, "\n") {
		t.Errorf("the reason must stay one line for the status: %q", reason)
	}
	if n := len([]rune(reason)); n > 60 {
		t.Errorf("reason is too long for a one-line status (%d runes): %q", n, reason)
	}
	// The full error is still available where there is room to print it.
	if !strings.Contains(h.Detail, "127.0.0.1:8080") {
		t.Errorf("Detail should keep the full error for /provider-status, got %q", h.Detail)
	}
}

func TestShortCauseClassification(t *testing.T) {
	cases := map[string]string{
		`Get "http://127.0.0.1:8080/v1/models": dial tcp: connect: connection refused`: "connection refused",
		"context deadline exceeded":            "timed out",
		"HTTP 401: invalid api key":            "HTTP 401 unauthorized",
		"HTTP 404: model not found":            "HTTP 404 not found",
		"HTTP 429: slow down":                  "rate limited",
		"HTTP 503: upstream unavailable":       "provider server error",
		"lookup api.example.com: no such host": "host not found",
	}
	for detail, want := range cases {
		if got := shortCause(detail); got != want {
			t.Errorf("shortCause(%q) = %q, want %q", detail, got, want)
		}
	}
	// An unrecognized error yields no parenthetical rather than a sawn-off
	// fragment of something the classifier does not understand.
	if got := shortCause("something entirely novel happened"); got != "" {
		t.Errorf("an unknown cause must classify to empty, got %q", got)
	}
	h := BrainHealth{Attempted: true, Provider: "openai", Detail: "something novel"}
	if r := h.Reason(); r != "openai unreachable" {
		t.Errorf("reason = %q, want a bare unreachable with no parenthetical", r)
	}
}

func TestNoteProviderReachableClearsDegradation(t *testing.T) {
	resetBrainHealth(t)
	NoteProviderUnreachable("llamacpp", "connection refused")
	NoteProviderReachable("llamacpp")

	if h := LastBrainHealth(); h.Degraded() {
		t.Errorf("a later success must clear the failure, got %+v", h)
	}
}

// A non-availability error (401, 400) is deliberately invisible to the failover
// breaker, but a brain that 401s on every turn still cannot answer — the status
// line has to say so.
func TestNoteBrainCallRecordsNonAvailabilityErrors(t *testing.T) {
	resetBrainHealth(t)
	noteBrainCall(errors.New("HTTP 401: invalid api key"), false)

	h := LastBrainHealth()
	if !h.Degraded() {
		t.Fatal("an auth failure leaves Helix just as unable to answer")
	}
	if !strings.Contains(h.Reason(), "401") {
		t.Errorf("reason should carry the cause, got %q", h.Reason())
	}
}

// Ctrl+C and barge-in say nothing about the provider, mirroring the speech
// registry's treatment of a cancelled turn.
func TestNoteBrainCallIgnoresCancellation(t *testing.T) {
	resetBrainHealth(t)
	NoteProviderReachable("ollama")
	noteBrainCall(context.Canceled, false)

	if h := LastBrainHealth(); h.Degraded() {
		t.Errorf("a cancelled call must not mark the brain unhealthy, got %+v", h)
	}
}

// A half-open probe goes to the DISPLACED provider, so recording it under the
// active provider's name would mislabel the observation.
func TestNoteBrainCallSkipsHalfOpenProbes(t *testing.T) {
	resetBrainHealth(t)
	noteBrainCall(errors.New("connection refused"), true)

	if h := LastBrainHealth(); h.Attempted {
		t.Errorf("a probe result must not be recorded as the active provider's, got %+v", h)
	}
}

func TestShortDetailCollapsesAndBounds(t *testing.T) {
	if got := shortDetail("line one\n  line two\ttabbed"); strings.ContainsAny(got, "\n\t") {
		t.Errorf("detail must be one line, got %q", got)
	}
	long := strings.Repeat("x", 200)
	if got := shortDetail(long); len([]rune(got)) > brainDetailMax {
		t.Errorf("detail is %d runes, want ≤%d", len([]rune(got)), brainDetailMax)
	}
}

func TestCheckActiveProviderWithNoProvider(t *testing.T) {
	resetBrainHealth(t)
	prev := activeProvider
	activeProvider = nil
	t.Cleanup(func() { activeProvider = prev })

	if err := CheckActiveProvider(context.Background()); err == nil {
		t.Fatal("no configured provider must be reported")
	}
}
