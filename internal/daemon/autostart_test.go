// internal/daemon/autostart_test.go
// Purpose: the daemon must not spawn a system-wide Ollama server as a side
// effect of a readiness check.
//
// This is a leak that actually happened. `ollama serve` binds port 11434 and
// outlives whatever started it, so every daemon test running with a temporary
// HOME left a server behind that served an EMPTY model store from that temp
// directory. It squatted the port, the developer's own `ollama list` came back
// blank while gigabytes sat in ~/.ollama, and every local-fallback request
// answered 404 — hours after the test had finished.
package daemon

import (
	"testing"

	"helix/internal/config"
)

// TestFallbackIsEnabledByDefault pins the premise: nothing has to opt IN for the
// readiness path to run, so the auto-start gate is the only thing standing
// between a test and a leaked daemon.
func TestFallbackIsEnabledByDefault(t *testing.T) {
	var unset config.LLMFallbackConfig
	if !unset.FallbackEnabled() {
		t.Fatal("premise changed: fallback is no longer on by default, " +
			"which alters what the auto-start gate is protecting")
	}
	if unset.EnsureReady {
		t.Fatal("ensure_ready must default to false — it is the consent gate for " +
			"heavyweight actions (starting a daemon, pulling gigabytes)")
	}
}

// TestDefaultConfigDoesNotConsentToAutoStart is the guard that matters: with a
// default config — which is what every test with a temp HOME gets — the daemon
// must probe, not spawn.
func TestDefaultConfigDoesNotConsentToAutoStart(t *testing.T) {
	defaults := config.LLMDefaults()
	if defaults.Fallback.EnsureReady {
		t.Error("the default config must not consent to starting or pulling anything; " +
			"a test with a temp HOME would leak a system-wide ollama serve")
	}
	// And the fallback still being ENABLED is what makes the gate load-bearing:
	// the readiness path runs, it just must not start anything.
	if !defaults.Fallback.FallbackEnabled() {
		t.Error("fallback should stay enabled by default; the gate is on starting, " +
			"not on checking")
	}
}
