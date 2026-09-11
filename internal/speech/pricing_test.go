// internal/speech/pricing_test.go
// Purpose: Embedded catalog integrity, user-override merging, and monthly
// cost estimates (ADR-006).
package speech

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedCatalogParses(t *testing.T) {
	entries, err := LoadCatalog()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(entries) < 5 {
		t.Fatalf("catalog suspiciously small: %d entries", len(entries))
	}
	for _, e := range entries {
		if e.Provider == "" || (e.Kind != "stt" && e.Kind != "tts") {
			t.Fatalf("malformed entry: %+v", e)
		}
		if e.Unit != "minute" && e.Unit != "1k_chars" {
			t.Fatalf("unknown unit in %+v", e)
		}
		if e.PricePerUnit < 0 {
			t.Fatalf("negative price in %+v", e)
		}
	}
}

func TestMergedCatalogUserOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	dir := filepath.Join(home, ".helix")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	override := pricingFile{Version: 1, Entries: []PricingEntry{
		{Provider: "openai", Kind: "stt", Model: "whisper-1", PricePerUnit: 0.002, Unit: "minute"},
		{Provider: "vulcan-telepathy", Kind: "stt", Model: "mind-meld", PricePerUnit: 0, Unit: "minute"},
	}}
	data, _ := json.Marshal(override)
	if err := os.WriteFile(filepath.Join(dir, "pricing.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	merged, err := LoadMergedCatalog()
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	// Matched on provider+kind+MODEL, which is the key the merge actually uses.
	// This used to match on provider+kind alone and passed only because openai
	// had exactly one STT row; a second one (gpt-live-transcribe) made it
	// assert the override against a row the override never named.
	foundOverride, foundNew, untouched := false, false, false
	for _, e := range merged {
		switch {
		case e.Provider == "openai" && e.Kind == "stt" && e.Model == "whisper-1":
			if e.PricePerUnit != 0.002 {
				t.Fatalf("override not applied: %+v", e)
			}
			foundOverride = true
		case e.Provider == "openai" && e.Kind == "stt" && e.Model == "gpt-live-transcribe":
			// A sibling row of the same provider and kind must survive
			// untouched, or one override would silently reprice the family.
			if e.PricePerUnit != 0.017 {
				t.Errorf("an unrelated row was repriced: %+v", e)
			}
			untouched = true
		case e.Provider == "vulcan-telepathy":
			foundNew = true
		}
	}
	if !untouched {
		t.Error("the embedded gpt-live-transcribe row disappeared from the merge")
	}
	if !foundOverride || !foundNew {
		t.Fatalf("merge incomplete: override=%v new=%v", foundOverride, foundNew)
	}
}

func TestEstimateMonthlyCost(t *testing.T) {
	stt := PricingEntry{Kind: "stt", Unit: "minute", PricePerUnit: 0.006}
	// 1h/day => 60 min/day * 30 days = 1800 min => $10.80
	if got := EstimateMonthlyCost(stt, 1); diff(got, 10.80) {
		t.Fatalf("stt estimate = %.4f, want 10.80", got)
	}

	tts := PricingEntry{Kind: "tts", Unit: "1k_chars", PricePerUnit: 0.015}
	// 1h/day speaking duty 25%: 60*0.25*850*30 = 382,500 chars => 382.5k * $0.015 = $5.7375
	if got := EstimateMonthlyCost(tts, 1); diff(got, 5.7375) {
		t.Fatalf("tts estimate = %.4f, want 5.7375", got)
	}

	free := PricingEntry{Kind: "tts", Unit: "1k_chars", PricePerUnit: 0}
	if got := EstimateMonthlyCost(free, 8); got != 0 {
		t.Fatalf("local providers must estimate $0, got %.2f", got)
	}
}

func diff(got, want float64) bool {
	return got < want-0.001 || got > want+0.001
}
