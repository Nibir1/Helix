// internal/ai/planner_adapter_test.go
package ai

import "testing"

func TestIsPlannerJSON(t *testing.T) {
	valid := `{"intent":"chat","steps":[{"tool":"response","message":"hi"}]}`

	if !isPlannerJSON(valid) {
		t.Fatal("expected valid JSON to pass")
	}

	fenced := "```json\n" + valid + "\n```"

	if !isPlannerJSON(fenced) {
		t.Fatal("expected fenced JSON to pass")
	}

	invalid := `{"intent":"chat","steps":[`

	if isPlannerJSON(invalid) {
		t.Fatal("expected invalid JSON to fail")
	}
}
