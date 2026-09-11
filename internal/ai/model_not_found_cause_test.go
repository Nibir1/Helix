// internal/ai/model_not_found_cause_test.go
// Purpose: the per-turn status line must name the right fault.
package ai

import "testing"

// shortCause is first-match-wins, so the model markers have to sit BEFORE the
// bare "http 404" entry. Getting the order wrong is silent: the line still
// appears, it just sends the reader to check their URL when the actual problem
// is that the vendor retired their model.
func TestShortCauseNamesARetiredModelBeforeABare404(t *testing.T) {
	for _, detail := range []string{
		`HTTP 404: {"error":{"code":"model_not_found"}}`,
		`HTTP 400: Model Not Exist`,
		`HTTP 404: the model 'x' does not exist`,
		`HTTP 400: invalid model`,
	} {
		if got := shortCause(detail); got != "model no longer exists" {
			t.Errorf("shortCause(%q) = %q, want the model cause", detail, got)
		}
	}

	// A routing 404 must still read as a routing 404.
	if got := shortCause("HTTP 404: 404 page not found"); got != "HTTP 404 not found" {
		t.Errorf("a bare 404 = %q, want the route cause", got)
	}
	// And the causes that were already right must not have moved.
	if got := shortCause("HTTP 401: unauthorized"); got != "HTTP 401 unauthorized" {
		t.Errorf("401 = %q", got)
	}
}
