// internal/speech/pricing.go
// Purpose: Data-driven pricing catalog for the provider-selection UX (ADR-006:
// pricing is data, not code). The embedded catalog can be overridden per entry
// or extended by ~/.helix/pricing.json — prices rot, and users must be able to
// fix them without a rebuild.
package speech

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed pricing.json
var embeddedCatalog []byte

// PricingEntry describes one provider/model price point for display.
type PricingEntry struct {
	Provider     string  `json:"provider"`
	Kind         string  `json:"kind"` // "stt" | "tts"
	Model        string  `json:"model"`
	PricePerUnit float64 `json:"price_per_unit"`
	Unit         string  `json:"unit"` // "minute" | "1k_chars"
	Latency      string  `json:"latency"`
	QualityTier  string  `json:"quality_tier"`
	Languages    string  `json:"languages"`
	RequiresKey  bool    `json:"requires_key"`
	Local        bool    `json:"local"`
	Recommended  bool    `json:"recommended"`
	Notes        string  `json:"notes"`
}

// APIModel returns the model identifier to persist in config and send to the
// provider API. Local sidecar entries use display-only model text ("piper
// (sidecar)") — for those the adapter default (empty) is correct.
func (e PricingEntry) APIModel() string {
	if e.Local {
		return ""
	}
	return e.Model
}

type pricingFile struct {
	Version int            `json:"version"`
	Note    string         `json:"note"`
	Entries []PricingEntry `json:"entries"`
}

// userPricingPath returns the override catalog location (~/.helix/pricing.json).
func userPricingPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helix", "pricing.json"), nil
}

// LoadCatalog parses the embedded catalog only.
func LoadCatalog() ([]PricingEntry, error) {
	var f pricingFile
	if err := json.Unmarshal(embeddedCatalog, &f); err != nil {
		return nil, fmt.Errorf("parse embedded pricing catalog: %w", err)
	}
	return f.Entries, nil
}

// LoadMergedCatalog returns the embedded catalog with user overrides applied.
// An override matches on provider+kind+model (replace) or introduces a new
// entry (append). A malformed user file yields the embedded catalog and an
// error the caller may surface without blocking.
func LoadMergedCatalog() ([]PricingEntry, error) {
	entries, err := LoadCatalog()
	if err != nil {
		return nil, err
	}

	path, err := userPricingPath()
	if err != nil {
		return entries, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return entries, nil // no override file is the normal case
	}

	var uf pricingFile
	if err := json.Unmarshal(data, &uf); err != nil {
		return entries, fmt.Errorf("parse %s (using embedded catalog): %w", path, err)
	}

	for _, over := range uf.Entries {
		replaced := false
		for i := range entries {
			if entries[i].Provider == over.Provider && entries[i].Kind == over.Kind && entries[i].Model == over.Model {
				entries[i] = over
				replaced = true
				break
			}
		}
		if !replaced {
			entries = append(entries, over)
		}
	}
	return entries, nil
}

// Constants for monthly-cost estimation. Spoken English averages ~150 words
// (~850 chars) per minute; the TTS duty cycle assumes Helix speaks for about a
// quarter of the interaction time.
const (
	charsPerMinuteSpoken = 850
	ttsDutyCycle         = 0.25
	daysPerMonth         = 30
)

// EstimateMonthlyCost projects a monthly USD cost for a usage profile.
//
// hoursPerDay is the projected daily interaction time (e.g. 1 or 8).
func EstimateMonthlyCost(e PricingEntry, hoursPerDay float64) float64 {
	if hoursPerDay <= 0 {
		hoursPerDay = 1
	}
	switch e.Unit {
	case "minute":
		minutes := hoursPerDay * 60 * daysPerMonth
		if e.Kind == "tts" {
			minutes *= ttsDutyCycle
		}
		return e.PricePerUnit * minutes
	case "1k_chars":
		chars := hoursPerDay * 60 * ttsDutyCycle * charsPerMinuteSpoken * daysPerMonth
		return e.PricePerUnit * (chars / 1000)
	default:
		return 0
	}
}

// FormatUnit renders a price point for tables ("$0.0060/min").
func FormatUnit(e PricingEntry) string {
	switch e.Unit {
	case "minute":
		return fmt.Sprintf("$%.4f/min", e.PricePerUnit)
	case "1k_chars":
		return fmt.Sprintf("$%.3f/1k chars", e.PricePerUnit)
	default:
		return "n/a"
	}
}
