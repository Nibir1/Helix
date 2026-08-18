// internal/ai/failover.go
//
// Purpose: automatic cloud→local LLM failover (BlackBox P11.2).
//
// The problem this solves: before this file, Helix degraded asymmetrically. The
// speech layer already flipped local-first when the network dropped
// (speech.SetOfflineMode → Registry.STTChain/TTSChain reordering, P4.10), so an
// offline Helix could still hear and speak perfectly — but every planner and
// chat call died with a raw provider error, because activeProvider was a single
// global with no fallback. Helix kept its ears and its voice and lost its mind.
//
// The design mirrors the speech pattern deliberately (same mental model, same
// vocabulary) and adds a circuit breaker so failover also works in the
// interactive shell, which has no connectivity monitor to tell it the network
// died:
//
//	CLOSED    → normal: calls go to the configured (usually cloud) provider.
//	OPEN      → after N consecutive availability failures, or an explicit
//	            SetOfflineMode(true) from the daemon monitor, calls go to the
//	            local provider. A spoken/printed notice fires once.
//	HALF-OPEN → after RetryAfter elapses, the next call probes the cloud
//	            provider. Success restores it (with a notice); failure resets
//	            the timer and stays local, silently.
//
// Guardrail note (§12 #3): this changes only WHICH model produces a plan. Every
// plan still re-enters the same pipeline — classify → planner → Instruction
// Firewall → risk tiers → Voice Risk Policy → sandbox → confinement. Failover
// cannot relax a control, and it is not an input channel.
//
// Honesty note: degrading usually swaps a large cloud model for a small local
// one, so plan quality genuinely drops. That is why the switch always announces
// itself rather than degrading silently. "Usually", because the primary is not
// necessarily cloud — llama.cpp primary with Ollama behind it is a supported
// all-local configuration, so every user-facing string names the provider it is
// actually talking about instead of assuming the word "cloud" applies.
package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"helix/internal/providers"
)

// Failover defaults.
const (
	// DefaultFailureThreshold is how many consecutive availability failures
	// trip the breaker. Two, not one: a single timeout is often a slow model
	// or a blip, and flipping the brain on every hiccup would be worse than
	// the outage it guards against.
	DefaultFailureThreshold = 2

	// DefaultRetryAfter is how long Helix stays local before probing the cloud
	// provider again.
	DefaultRetryAfter = 2 * time.Minute

	// localHealthTimeout bounds the pre-flip health check on the local
	// provider.
	localHealthTimeout = 4 * time.Second
)

// LocalFallback configures automatic cloud→local degradation.
type LocalFallback struct {
	// Enabled arms the breaker. Disabled means the old behavior exactly:
	// provider errors surface to the user untouched.
	Enabled bool

	// Provider is the registry name of the local brain ("ollama", "llamacpp").
	Provider string

	// Model is the local model to use ("" → the provider's default).
	Model string

	// Threshold is the consecutive-failure count that trips the breaker
	// (0 → DefaultFailureThreshold).
	Threshold int

	// RetryAfter is the half-open probe interval (0 → DefaultRetryAfter).
	RetryAfter time.Duration
}

var (
	failoverMu sync.Mutex

	fallbackCfg   LocalFallback
	localProvider providers.AIProvider

	consecutiveFailures int
	degraded            bool

	// Saved cloud selection, restored when connectivity returns.
	savedProvider providers.AIProvider
	savedModel    string

	// nextProbe is when the breaker next allows a cloud probe (half-open).
	nextProbe time.Time

	// failoverNotice reports switches to the user. The seam mirrors the voice
	// OnSpeak wiring: cmd/helix prints + speaks, the daemon speaks + journals,
	// and tests capture. Nil = silent (the library default).
	failoverNotice func(string)

	// now is a test seam for the breaker clock.
	now = time.Now
)

// ConfigureLocalFallback arms or disarms automatic failover.
//
// A configured-but-unregistered provider is a user error worth surfacing, so it
// returns an error rather than silently running unprotected.
//
// Args:
//   - f: fallback settings from config.
//
// Returns: error when the named provider is not registered.
// Complexity: O(1).
func ConfigureLocalFallback(f LocalFallback) error {
	failoverMu.Lock()
	defer failoverMu.Unlock()

	// Rearming resets breaker state; a config change should not inherit a
	// stale failure count from the previous settings.
	consecutiveFailures = 0
	if degraded {
		restoreLocked("Restoring the configured model.")
	}
	fallbackCfg = f
	localProvider = nil

	if !f.Enabled {
		return nil
	}
	name := strings.TrimSpace(f.Provider)
	if name == "" {
		fallbackCfg.Enabled = false
		return fmt.Errorf("local LLM fallback is enabled but no provider is named")
	}
	p, err := registryGet(name)
	if err != nil {
		fallbackCfg.Enabled = false
		return fmt.Errorf("local LLM fallback provider %q: %w", name, err)
	}
	if !p.IsLocal() {
		fallbackCfg.Enabled = false
		return fmt.Errorf(
			"local LLM fallback provider %q is not a local provider — "+
				"failing over to another cloud provider would not survive an outage", name)
	}
	localProvider = p
	return nil
}

// registryGet is a thin indirection so failover state can be configured in
// tests without a full provider registry.
func registryGet(name string) (providers.AIProvider, error) {
	if registry == nil {
		return nil, fmt.Errorf("provider registry not initialized")
	}
	return registry.Get(name)
}

// SetFailoverNotice installs the user-facing notice sink (print/speak).
func SetFailoverNotice(fn func(string)) {
	failoverMu.Lock()
	failoverNotice = fn
	failoverMu.Unlock()
}

// SetOfflineMode forces or clears LLM degradation, mirroring
// speech.SetOfflineMode. The daemon's connectivity monitor calls both on the
// same transition so the ears, the voice, and the brain move together.
//
// Args:
//   - on: true when the network is known to be down.
//
// Complexity: O(1) plus one local health check when engaging.
func SetOfflineMode(on bool) {
	failoverMu.Lock()
	defer failoverMu.Unlock()

	if on {
		if !degraded {
			degradeLocked(fmt.Sprintf("I lost the %s model. Switching to local intelligence.",
				primaryNameLocked()))
		}
		return
	}
	if degraded {
		restoreLocked(fmt.Sprintf("The %s model is restored.", displacedNameLocked()))
	}
	consecutiveFailures = 0
}

// LocalFallbackActive reports whether the local brain is currently in force.
func LocalFallbackActive() bool {
	failoverMu.Lock()
	defer failoverMu.Unlock()
	return degraded
}

// FailoverStatus returns a one-line human-readable breaker state for /doctor,
// /provider, and `helix remote status`.
func FailoverStatus() string {
	failoverMu.Lock()
	defer failoverMu.Unlock()

	if !fallbackCfg.Enabled {
		return "disabled"
	}
	if degraded {
		return fmt.Sprintf("ACTIVE — using local %s (%s retry in %s)",
			fallbackCfg.Provider, displacedNameLocked(), remainingLocked().Round(time.Second))
	}
	// Deliberately not "ready": readiness is only known at switch time, when
	// degradeLocked health-checks the provider. Claiming a fallback is ready
	// when it might not be pulled is exactly the false comfort P11.3 exists to
	// remove.
	return fmt.Sprintf("armed — will switch to %s if %s fails",
		fallbackCfg.Provider, primaryNameLocked())
}

// primaryNameLocked names the provider the breaker is currently protecting.
// Callers must hold failoverMu.
//
// It exists because this line used to say "if the cloud model fails" no matter
// what was selected. Failing over between two LOCAL providers is a supported and
// sensible configuration — llama.cpp primary with Ollama behind it is exactly the
// edge-box story in docs/edge_deployment.md §5 — and describing llama.cpp as
// "the cloud model" made the status line read as though Helix had misunderstood
// its own configuration.
func primaryNameLocked() string {
	if degraded {
		return displacedNameLocked()
	}
	if activeProvider != nil {
		return activeProvider.Name()
	}
	return "the active model"
}

// displacedNameLocked names the provider the breaker stepped away from (the one
// a half-open probe will retry). Callers must hold failoverMu.
func displacedNameLocked() string {
	if savedProvider != nil {
		return savedProvider.Name()
	}
	return "the previous model"
}

func remainingLocked() time.Duration {
	d := nextProbe.Sub(now())
	if d < 0 {
		return 0
	}
	return d
}

// resolveProvider picks the provider for the next call and reports whether it
// is a half-open cloud probe.
//
// Returns: provider, model, probing flag. A nil provider means none is
// configured and the caller surfaces the usual error.
// Complexity: O(1).
func resolveProvider() (providers.AIProvider, string, bool) {
	failoverMu.Lock()
	defer failoverMu.Unlock()

	if !degraded {
		return activeProvider, activeModel, false
	}
	// Half-open: the retry window elapsed, so let this call probe the cloud.
	if !now().Before(nextProbe) && savedProvider != nil {
		return savedProvider, savedModel, true
	}
	return activeProvider, activeModel, false
}

// noteCallResult feeds one provider call outcome to the breaker.
//
// Args:
//   - err: the call's error (nil on success).
//   - probing: true when the call was a half-open cloud probe.
//
// Complexity: O(1) plus one local health check on the flip.
func noteCallResult(err error, probing bool) {
	// Record what this call proves about the active provider BEFORE the breaker
	// logic, and under a different lock: the breaker deliberately ignores
	// non-availability errors, but the status line must still know the brain
	// could not answer (see brain_health.go).
	noteBrainCall(err, probing)

	failoverMu.Lock()
	defer failoverMu.Unlock()

	if err == nil {
		if probing {
			restoreLocked(fmt.Sprintf("The %s model is reachable again. Switching back.",
				displacedNameLocked()))
			return
		}
		consecutiveFailures = 0
		return
	}

	// Only availability failures count. A 400, a 401, or a Ctrl+C is a real
	// error the user must see — hiding it behind a quieter local model would
	// turn a fixable misconfiguration into a mysterious quality drop.
	if !isAvailabilityError(err) {
		return
	}

	if probing {
		// Cloud still down: reset the window and stay local, silently.
		nextProbe = now().Add(retryAfterLocked())
		return
	}
	if degraded || !fallbackCfg.Enabled {
		return
	}

	consecutiveFailures++
	if consecutiveFailures < thresholdLocked() {
		return
	}
	degradeLocked(fmt.Sprintf("The %s model is not responding. Switching to local intelligence.",
		primaryNameLocked()))
}

func thresholdLocked() int {
	if fallbackCfg.Threshold > 0 {
		return fallbackCfg.Threshold
	}
	return DefaultFailureThreshold
}

func retryAfterLocked() time.Duration {
	if fallbackCfg.RetryAfter > 0 {
		return fallbackCfg.RetryAfter
	}
	return DefaultRetryAfter
}

// degradeLocked switches the active provider to the local brain. Callers must
// hold failoverMu.
//
// The local health check before flipping is the important part: degrading onto
// an Ollama that is not running would replace a clear "cloud unreachable" error
// with a confusing "local unreachable" one and lose the real diagnosis. If the
// local brain is not actually there, stay put and let the cloud error surface.
func degradeLocked(notice string) {
	if !fallbackCfg.Enabled || localProvider == nil || activeProvider == nil {
		return
	}
	if localProvider.Name() == activeProvider.Name() {
		return // already thinking locally; nothing to fail over to
	}

	// This health check holds failoverMu for up to localHealthTimeout, so a
	// concurrent model call blocks behind it. That is the intended ordering —
	// the next call should use the decision, not race it — and it happens only
	// on a transition, not per call. Model calls are serialized anyway (the
	// daemon behind d.mu, the shell single-threaded).
	ctx, cancel := context.WithTimeout(context.Background(), localHealthTimeout)
	defer cancel()
	if err := localProvider.HealthCheck(ctx); err != nil {
		// Do not announce a switch that did not happen.
		consecutiveFailures = 0
		return
	}

	savedProvider, savedModel = activeProvider, activeModel
	activeProvider = localProvider
	activeModel = fallbackCfg.Model
	if activeModel == "" {
		activeModel = localProvider.DefaultModel()
	}
	degraded = true
	consecutiveFailures = 0
	nextProbe = now().Add(retryAfterLocked())
	notifyLocked(notice)
}

// restoreLocked returns to the saved cloud selection. Callers must hold
// failoverMu.
func restoreLocked(notice string) {
	if !degraded {
		return
	}
	if savedProvider != nil {
		activeProvider = savedProvider
		activeModel = savedModel
	}
	savedProvider, savedModel = nil, ""
	degraded = false
	consecutiveFailures = 0
	notifyLocked(notice)
}

// notifyLocked fires the notice sink outside the lock's critical path for the
// callback itself — the sink may speak, which takes seconds.
func notifyLocked(msg string) {
	fn := failoverNotice
	if fn == nil || msg == "" {
		return
	}
	go fn(msg)
}

// clearDegradedForUserOverride drops breaker state when the user deliberately
// selects a provider or model. An explicit choice must outrank the breaker —
// otherwise a later automatic "restore" would silently undo what the user just
// asked for.
func clearDegradedForUserOverride() {
	failoverMu.Lock()
	defer failoverMu.Unlock()
	degraded = false
	savedProvider, savedModel = nil, ""
	consecutiveFailures = 0
}

// isAvailabilityError reports whether an error means "this provider cannot be
// reached right now" as opposed to "this request was wrong".
//
// Complexity: O(len(err.Error())).
func isAvailabilityError(err error) bool {
	if err == nil {
		return false
	}
	// A user Ctrl+C cancels the context; that is not an outage.
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}

	s := strings.ToLower(err.Error())
	// The shared HTTP client already retries 429/5xx internally and surfaces
	// the exhausted attempt as "HTTP <code>: ..." (internal/providers/client.go).
	for _, marker := range []string{
		"http 5", "http 429",
		"request failed",
		"connection refused", "connection reset",
		"no such host", "network is unreachable", "host is unreachable",
		"i/o timeout", "timeout", "timed out",
		"tls handshake", "eof",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}
