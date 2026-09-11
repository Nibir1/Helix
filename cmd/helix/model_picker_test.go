// cmd/helix/model_picker_test.go
// Purpose: the number-or-name resolution, which has one guard that is easy to
// get wrong and silent when it is.
package main

import (
	"testing"

	"helix/internal/providers"
)

var pickerModels = []providers.ModelInfo{
	{ID: "gemini-3.7-flash"},
	{ID: "gemini-3.1-pro"},
	{ID: "text-embedding-004"},
}

func TestResolveModelChoiceByNumber(t *testing.T) {
	got, verbatim := resolveModelChoice("2", pickerModels, pickerModels, "gemini-3.7-flash")
	if got != "gemini-3.1-pro" {
		t.Errorf("row 2 = %q", got)
	}
	if verbatim {
		t.Error("a listed row was reported as unrecognised")
	}
}

func TestResolveModelChoiceByExactName(t *testing.T) {
	got, verbatim := resolveModelChoice("text-embedding-004", pickerModels, pickerModels, "")
	if got != "text-embedding-004" || verbatim {
		t.Errorf("got %q verbatim=%v", got, verbatim)
	}
}

// The provider's own casing wins, because a saved ID that differs only in case
// can fail on strict endpoints.
func TestResolveModelChoiceFixesCasing(t *testing.T) {
	got, verbatim := resolveModelChoice("GEMINI-3.1-PRO", pickerModels, pickerModels, "")
	if got != "gemini-3.1-pro" {
		t.Errorf("got %q, want the provider's casing", got)
	}
	if verbatim {
		t.Error("a case-different match was treated as unrecognised")
	}
}

// THE guard. A provider is free to ship a model literally called "3", and
// reading that as row three would pick a different model without saying so.
func TestANumericModelNameBeatsTheRowNumber(t *testing.T) {
	models := []providers.ModelInfo{
		{ID: "alpha"}, {ID: "beta"}, {ID: "3"},
	}
	got, verbatim := resolveModelChoice("3", models, models, "")
	if got != "3" {
		t.Errorf("got %q, want the model literally named 3 — not row three (%q)",
			got, models[2].ID)
	}
	if verbatim {
		t.Error("a listed model was reported as unrecognised")
	}
}

// Rows below the display cap must still be reachable by name, or the cap
// becomes a hard limit on what can be selected.
func TestModelsBelowTheCapAreReachableByName(t *testing.T) {
	shown := pickerModels[:1]
	got, verbatim := resolveModelChoice("gemini-3.1-pro", shown, pickerModels, "")
	if got != "gemini-3.1-pro" || verbatim {
		t.Errorf("got %q verbatim=%v — a model past the cap was unreachable", got, verbatim)
	}
}

// Anything unrecognised is accepted and FLAGGED. This is how gpt-live-1
// becomes selectable without a code change, and the flag is what makes the
// resulting 404 explainable instead of mystifying.
func TestUnknownIDIsAcceptedVerbatim(t *testing.T) {
	got, verbatim := resolveModelChoice("gpt-live-1", pickerModels, pickerModels, "")
	if got != "gpt-live-1" {
		t.Errorf("got %q, want the typed id", got)
	}
	if !verbatim {
		t.Error("an unrecognised id was not flagged, so the user gets no warning " +
			"before every turn starts failing")
	}
}

func TestEmptyChoiceFallsBackToThePreferred(t *testing.T) {
	got, _ := resolveModelChoice("  ", pickerModels, pickerModels, "gemini-3.1-pro")
	if got != "gemini-3.1-pro" {
		t.Errorf("got %q, want the current model", got)
	}
	// With nothing preferred, the best-ranked visible row stands in.
	got, _ = resolveModelChoice("", pickerModels, pickerModels, "")
	if got != "gemini-3.7-flash" {
		t.Errorf("got %q, want the first shown row", got)
	}
	// And with nothing at all, no invention.
	if got, _ := resolveModelChoice("", nil, nil, ""); got != "" {
		t.Errorf("got %q from nothing, want empty", got)
	}
}

// An out-of-range number is not a row; it is a model name the provider might
// know. Silently clamping it would select something the user did not ask for.
func TestOutOfRangeNumberIsTreatedAsAName(t *testing.T) {
	got, verbatim := resolveModelChoice("99", pickerModels, pickerModels, "")
	if got != "99" || !verbatim {
		t.Errorf("got %q verbatim=%v, want it passed through and flagged", got, verbatim)
	}
}
