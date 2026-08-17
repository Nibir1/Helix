// internal/ai/failover_test.go
// Purpose: BlackBox P11.2 — the cloud→local brain circuit breaker trips on
// availability failures only, announces every switch, and restores the cloud
// model via the half-open probe or an explicit connectivity signal.
package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"helix/internal/providers"
)

// failoverFake is a scriptable provider: it fails the first failUntil calls,
// then succeeds, and records how many chat calls it served.
type failoverFake struct {
	name     string
	local    bool
	healthy  bool
	err      error
	mu       sync.Mutex
	calls    int
	failNext int // number of upcoming calls that must fail
}

func (p *failoverFake) Name() string        { return p.name }
func (p *failoverFake) DisplayName() string { return p.name }
func (p *failoverFake) SetAPIKey(string)    {}
func (p *failoverFake) ListModels(context.Context) ([]providers.ModelInfo, error) {
	return nil, nil
}
func (p *failoverFake) HealthCheck(context.Context) error {
	if p.healthy {
		return nil
	}
	return errors.New("sidecar down")
}
func (p *failoverFake) RequiresAPIKey() bool { return !p.local }
func (p *failoverFake) IsLocal() bool        { return p.local }
func (p *failoverFake) DefaultModel() string { return p.name + "-model" }
func (p *failoverFake) Capabilities() providers.Capabilities {
	return providers.Capabilities{Chat: true, Local: p.local}
}

func (p *failoverFake) Chat(_ context.Context, _ providers.ChatRequest) (<-chan providers.StreamChunk, error) {
	p.mu.Lock()
	p.calls++
	fail := p.failNext > 0
	if fail {
		p.failNext--
	}
	p.mu.Unlock()

	if fail {
		return nil, p.err
	}
	ch := make(chan providers.StreamChunk, 2)
	ch <- providers.StreamChunk{Content: p.name + " says hi"}
	ch <- providers.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

func (p *failoverFake) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// failoverHarness installs a cloud provider, a local fallback, and a notice
// collector, restoring every global on cleanup. The ai package keeps provider
// selection in package-level state, so tests must be explicit about teardown.
type failoverHarness struct {
	cloud   *failoverFake
	local   *failoverFake
	notices chan string
	clock   time.Time
}

func newFailoverHarness(t *testing.T, cloudErr error, localHealthy bool) *failoverHarness {
	t.Helper()

	oldProvider, oldModel := activeProvider, activeModel
	oldCfg, oldLocal := fallbackCfg, localProvider
	oldNotice, oldNow := failoverNotice, now
	oldDegraded, oldSaved, oldSavedModel := degraded, savedProvider, savedModel
	oldFailures, oldProbe := consecutiveFailures, nextProbe

	h := &failoverHarness{
		cloud:   &failoverFake{name: "cloud", healthy: true, err: cloudErr},
		local:   &failoverFake{name: "ollama", local: true, healthy: localHealthy},
		notices: make(chan string, 8),
		clock:   time.Unix(1_700_000_000, 0),
	}

	t.Cleanup(func() {
		failoverMu.Lock()
		activeProvider, activeModel = oldProvider, oldModel
		fallbackCfg, localProvider = oldCfg, oldLocal
		failoverNotice, now = oldNotice, oldNow
		degraded, savedProvider, savedModel = oldDegraded, oldSaved, oldSavedModel
		consecutiveFailures, nextProbe = oldFailures, oldProbe
		failoverMu.Unlock()
	})

	failoverMu.Lock()
	activeProvider, activeModel = h.cloud, "cloud-model"
	fallbackCfg = LocalFallback{Enabled: true, Provider: "ollama", Threshold: 2,
		RetryAfter: 2 * time.Minute}
	localProvider = h.local
	degraded, savedProvider, savedModel = false, nil, ""
	consecutiveFailures, nextProbe = 0, time.Time{}
	now = func() time.Time { return h.clock }
	// notifyLocked fires the sink on a goroutine (it may speak, which blocks);
	// a buffered channel keeps the test deterministic without sleeping.
	failoverNotice = func(msg string) { h.notices <- msg }
	failoverMu.Unlock()

	return h
}

// advance moves the breaker's clock forward.
func (h *failoverHarness) advance(d time.Duration) { h.clock = h.clock.Add(d) }

// awaitNotice waits briefly for a switch announcement.
func (h *failoverHarness) awaitNotice(t *testing.T) string {
	t.Helper()
	select {
	case msg := <-h.notices:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("expected a spoken/printed notice for the brain switch, got none")
		return ""
	}
}

func (h *failoverHarness) noNotice(t *testing.T) {
	t.Helper()
	select {
	case msg := <-h.notices:
		t.Fatalf("expected silence, got notice %q", msg)
	case <-time.After(150 * time.Millisecond):
	}
}

// unreachable is the shape the shared HTTP client produces for a dead endpoint.
var unreachable = fmt.Errorf("request failed: %w",
	&net.OpError{Op: "dial", Err: errors.New("connect: connection refused")})

func TestFailoverTripsAfterThresholdAndAnnounces(t *testing.T) {
	h := newFailoverHarness(t, unreachable, true)
	h.cloud.failNext = 2

	// Failure 1: below threshold — still on the cloud provider, no notice.
	if _, err := RunModel("hello"); err == nil {
		t.Fatal("expected the first call to fail")
	}
	if LocalFallbackActive() {
		t.Fatal("one failure must not flip the brain — a single blip is not an outage")
	}
	h.noNotice(t)

	// Failure 2: threshold reached — flip to local.
	if _, err := RunModel("hello"); err == nil {
		t.Fatal("expected the second call to fail")
	}
	if !LocalFallbackActive() {
		t.Fatal("two consecutive availability failures must engage the local fallback")
	}
	h.awaitNotice(t)

	// The next call must be served by the local provider.
	out, err := RunModel("hello")
	if err != nil {
		t.Fatalf("degraded call should succeed on the local model: %v", err)
	}
	if out != "ollama says hi" {
		t.Fatalf("expected the local model to answer, got %q", out)
	}
	if ActiveProviderName() != "ollama" {
		t.Fatalf("status surfaces must report the brain in force, got %q", ActiveProviderName())
	}
}

func TestFailoverIgnoresNonAvailabilityErrors(t *testing.T) {
	// A 400 is a bad request, not an outage: degrading would hide a fixable
	// error behind a quieter model.
	h := newFailoverHarness(t, errors.New("HTTP 400: invalid request payload"), true)
	h.cloud.failNext = 5

	for i := 0; i < 4; i++ {
		if _, err := RunModel("hello"); err == nil {
			t.Fatal("expected the call to fail")
		}
	}
	if LocalFallbackActive() {
		t.Fatal("a 400 must never trip the breaker")
	}
	h.noNotice(t)
}

func TestFailoverDoesNotEngageWhenLocalBrainIsDown(t *testing.T) {
	// Degrading onto a dead Ollama would swap a true "cloud unreachable"
	// diagnosis for a misleading one.
	h := newFailoverHarness(t, unreachable, false)
	h.cloud.failNext = 6

	for i := 0; i < 4; i++ {
		_, _ = RunModel("hello")
	}
	if LocalFallbackActive() {
		t.Fatal("failover must not engage when the local provider fails its health check")
	}
	h.noNotice(t)
	if ActiveProviderName() != "cloud" {
		t.Fatalf("the configured provider must stay in force, got %q", ActiveProviderName())
	}
}

func TestFailoverHalfOpenProbeRestoresCloud(t *testing.T) {
	h := newFailoverHarness(t, unreachable, true)

	SetOfflineMode(true)
	if !LocalFallbackActive() {
		t.Fatal("an explicit offline signal must engage the local brain")
	}
	h.awaitNotice(t)

	cloudCallsAtDegrade := h.cloud.callCount()

	// Inside the retry window: calls stay local, the cloud is not probed.
	if _, err := RunModel("hello"); err != nil {
		t.Fatalf("local call failed: %v", err)
	}
	if h.cloud.callCount() != cloudCallsAtDegrade {
		t.Fatal("the cloud provider must not be dialed inside the retry window")
	}

	// After the window: the next call probes the cloud, which now answers.
	h.advance(3 * time.Minute)
	out, err := RunModel("hello")
	if err != nil {
		t.Fatalf("half-open probe should have succeeded: %v", err)
	}
	if out != "cloud says hi" {
		t.Fatalf("the probe must be served by the cloud provider, got %q", out)
	}
	if LocalFallbackActive() {
		t.Fatal("a successful probe must restore the cloud model")
	}
	h.awaitNotice(t)
	if ActiveProviderName() != "cloud" {
		t.Fatalf("expected the cloud provider restored, got %q", ActiveProviderName())
	}
}

func TestFailoverFailedProbeStaysLocalSilently(t *testing.T) {
	h := newFailoverHarness(t, unreachable, true)

	SetOfflineMode(true)
	h.awaitNotice(t)

	// The cloud is still down when the probe fires.
	h.cloud.failNext = 1
	h.advance(3 * time.Minute)
	if _, err := RunModel("hello"); err == nil {
		t.Fatal("the probe should have failed while the cloud is still down")
	}
	if !LocalFallbackActive() {
		t.Fatal("a failed probe must leave the local brain in force")
	}
	// No second announcement — the user already knows.
	h.noNotice(t)

	// The retry window restarted, so the following call is local again.
	if _, err := RunModel("hello"); err != nil {
		t.Fatalf("post-probe call should be served locally: %v", err)
	}
}

func TestSetOfflineModeFalseRestores(t *testing.T) {
	h := newFailoverHarness(t, unreachable, true)

	SetOfflineMode(true)
	h.awaitNotice(t)
	if ActiveProviderName() != "ollama" {
		t.Fatalf("expected the local brain, got %q", ActiveProviderName())
	}

	SetOfflineMode(false)
	h.awaitNotice(t)
	if LocalFallbackActive() {
		t.Fatal("connectivity restore must clear degradation")
	}
	if ActiveProviderName() != "cloud" || ActiveModel() != "cloud-model" {
		t.Fatalf("restore must return the exact prior selection, got %q/%q",
			ActiveProviderName(), ActiveModel())
	}
}

func TestUserProviderChoiceOutranksBreaker(t *testing.T) {
	h := newFailoverHarness(t, unreachable, true)

	SetOfflineMode(true)
	h.awaitNotice(t)

	// The user deliberately picks a model while degraded.
	UseModel("chosen-by-hand")
	if LocalFallbackActive() {
		t.Fatal("an explicit user choice must clear breaker state")
	}

	// A later restore signal must not silently undo that choice.
	SetOfflineMode(false)
	if ActiveModel() != "chosen-by-hand" {
		t.Fatalf("restore clobbered the user's explicit selection: %q", ActiveModel())
	}
}

func TestFailoverDisabledKeepsOldBehavior(t *testing.T) {
	h := newFailoverHarness(t, unreachable, true)
	failoverMu.Lock()
	fallbackCfg.Enabled = false
	failoverMu.Unlock()
	h.cloud.failNext = 6

	for i := 0; i < 4; i++ {
		if _, err := RunModel("hello"); err == nil {
			t.Fatal("expected the call to fail")
		}
	}
	if LocalFallbackActive() {
		t.Fatal("a disabled fallback must never engage")
	}
	if FailoverStatus() != "disabled" {
		t.Fatalf("status should report disabled, got %q", FailoverStatus())
	}
	h.noNotice(t)
}

func TestIsAvailabilityError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"user ctrl+c", context.Canceled, false},
		{"timeout", context.DeadlineExceeded, true},
		{"dial refused", unreachable, true},
		{"http 503", errors.New("HTTP 503: upstream unavailable"), true},
		{"http 429", errors.New("HTTP 429: rate limited"), true},
		{"http 400", errors.New("HTTP 400: bad request"), false},
		{"http 401", errors.New("HTTP 401: invalid api key"), false},
		{"dns", errors.New("request failed: no such host"), true},
		{"empty prompt", errors.New("empty prompt"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAvailabilityError(tc.err); got != tc.want {
				t.Fatalf("isAvailabilityError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
