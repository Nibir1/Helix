// internal/speech/metrics_locality_test.go
// Purpose: keep metrics.LocalProviders honest about which speech providers run
// on this machine.
//
// WHY THIS TEST EXISTS. §10 grades TTS first-audio and STT latency against two
// different budgets — 800 ms cloud, 1500 ms local — and `/blackbox stats` picks
// the column from the provider recorded with each sample. That decision reads a
// hand-kept map in internal/metrics, which cannot ask this package: metrics
// imports nothing but the standard library, enforced by its own
// TestNoNetworkImports, because those files are local-only by guarantee.
//
// A copy that cannot be derived drifts, and this one did. `csm-local` shipped as
// a local TTS provider (ADR-017) and was never added to the map, so its samples
// were judged against the CLOUD budget. CSM's measured real-time factor is
// 1.69× — slower than playback, on purpose, because it is a discrete-GPU
// capability — so every honest local measurement of it was reported as a hard
// failure against a target that does not apply to it. Nothing was wrong with
// the arithmetic; the wrong column was chosen.
//
// The test lives HERE rather than in internal/metrics for the same reason the
// map is hand-kept: only this side of the boundary knows what IsLocal() says.
package speech

import (
	"testing"

	"helix/internal/metrics"
	"helix/internal/providers"
)

// TestMetricsKnowsEveryLocalSpeechProvider walks every builtin adapter and
// requires the metrics map to agree with the adapter's own IsLocal().
//
// Both directions are checked. A local provider missing from the map is the bug
// above; a CLOUD provider listed in the map is the mirror image, and worse in a
// quiet way — it would grade a cloud sample against the roomier local budget and
// report a latency regression as a pass.
func TestMetricsKnowsEveryLocalSpeechProvider(t *testing.T) {
	keys, err := providers.NewKeyStore()
	if err != nil {
		t.Fatalf("keystore: %v", err)
	}
	reg := NewRegistry(keys, sharedClient)
	registerBuiltins(reg, Config{})

	for _, name := range reg.STTNames() {
		p, ok := reg.STTProvider(name)
		if !ok {
			t.Fatalf("%s is named by the registry and cannot be fetched from it", name)
		}
		assertLocality(t, "STT", name, p.IsLocal())
	}
	for _, name := range reg.TTSNames() {
		p, ok := reg.TTSProvider(name)
		if !ok {
			t.Fatalf("%s is named by the registry and cannot be fetched from it", name)
		}
		assertLocality(t, "TTS", name, p.IsLocal())
	}
}

func assertLocality(t *testing.T, kind, name string, local bool) {
	t.Helper()
	switch {
	case local && !metrics.IsLocal(name):
		t.Errorf("%s provider %q reports IsLocal() but metrics.LocalProviders does not list it — "+
			"its samples would be graded against the CLOUD §10 target", kind, name)
	case !local && metrics.IsLocal(name):
		t.Errorf("%s provider %q is a cloud provider yet metrics.LocalProviders lists it — "+
			"its samples would be graded against the roomier LOCAL §10 target", kind, name)
	}
}

// The guard above is only as good as the registry it walks: if registerBuiltins
// ever registered nothing (a refactor, a build tag), every assertion would pass
// vacuously and the drift it exists to catch would return unnoticed. §9 rule 8 —
// assert the precondition that puts the code under test on the path.
func TestLocalityGuardActuallySeesLocalProviders(t *testing.T) {
	keys, err := providers.NewKeyStore()
	if err != nil {
		t.Fatalf("keystore: %v", err)
	}
	reg := NewRegistry(keys, sharedClient)
	registerBuiltins(reg, Config{})

	var local, cloud int
	for _, name := range reg.TTSNames() {
		if p, ok := reg.TTSProvider(name); ok {
			if p.IsLocal() {
				local++
			} else {
				cloud++
			}
		}
	}
	if local == 0 || cloud == 0 {
		t.Fatalf("the registry must offer both local and cloud voices for this guard to mean "+
			"anything (local=%d cloud=%d)", local, cloud)
	}
}
