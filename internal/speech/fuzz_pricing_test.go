// internal/speech/fuzz_pricing_test.go
// Purpose: P1.11's other half. That task deferred "fuzzing of pricing merge +
// WAV headers" to the Phase 7 hardening pass; the WAV half landed as
// FuzzWAVHeaderInfo and the pricing half never did.
//
// It is owed by §9 rule 5 ("fuzz every new parser") because the merge really is
// a parser of user-controlled input: ~/.helix/pricing.json is a file the user
// writes by hand to correct prices that have rotted (ADR-006). The contract that
// matters is that a malformed or hostile override can never take the wizard
// down — LoadMergedCatalog must always return a USABLE catalog, falling back to
// the embedded one, because a wizard that cannot render its table cannot
// configure speech at all.
package speech

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzPricingMerge(f *testing.F) {
	f.Add(`{"version":1,"entries":[]}`)
	f.Add(`{"entries":[{"provider":"openai","kind":"tts","model":"tts-1","price_per_unit":0.02,"unit":"1k_chars"}]}`)
	// An override of an existing entry (replace path) and a new one (append path).
	f.Add(`{"entries":[{"provider":"groq","kind":"stt","model":"whisper-large-v3-turbo","price_per_unit":0.001,"unit":"minute"}]}`)
	f.Add(`{"entries":[{"provider":"mine","kind":"stt","model":"x","price_per_unit":1,"unit":"minute","local":true}]}`)
	// Hostile / degenerate shapes.
	f.Add(`{`)
	f.Add(`[]`)
	f.Add(`null`)
	f.Add(``)
	f.Add(`{"entries":null}`)
	f.Add(`{"entries":[{"unit":"","price_per_unit":-1}]}`)
	f.Add(`{"entries":[{"unit":"minute","price_per_unit":1e308}]}`)
	f.Add(`{"entries":[{"provider":" ","kind":"stt","unit":"minute"}]}`)

	f.Fuzz(func(t *testing.T, override string) {
		// userPricingPath resolves against the home directory, so point HOME at
		// a temp dir to keep the fuzz hermetic and never touch the real file.
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home) // windows
		dir := filepath.Join(home, ".helix")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Skipf("cannot create temp helix dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pricing.json"), []byte(override), 0o600); err != nil {
			t.Skipf("cannot write override: %v", err)
		}

		// The contract: never panic, and ALWAYS hand back a usable catalog. An
		// error is allowed (it is surfaced as a notice) but it must come with
		// the embedded entries, not an empty slice — the wizard has a table to
		// render either way.
		entries, err := LoadMergedCatalog()
		if len(entries) == 0 {
			t.Fatalf("merge returned an empty catalog for override %q (err=%v); "+
				"the wizard would have no table to show", override, err)
		}

		// Every surviving entry must be safe to RENDER and to price, because
		// the wizard does both to whatever the merge returns.
		for _, e := range entries {
			_ = FormatUnit(e)
			_ = e.APIModel()
			for _, hours := range []float64{0, 1, 2, 8, -1} {
				cost := EstimateMonthlyCost(e, hours)
				// A NaN would render as "$NaN/mo" in a table of dollars, and an
				// infinity would print as "+Inf" — both are worse than a wrong
				// number because they look like a bug in Helix rather than a bad
				// override.
				if cost != cost {
					t.Fatalf("EstimateMonthlyCost produced NaN for entry %+v (override %q)", e, override)
				}
			}
		}
	})
}
