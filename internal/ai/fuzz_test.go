// internal/ai/fuzz_test.go
// Purpose: Continuous fuzzing for the strict JSON planner parser.
// Invariants: Must never panic; nil-error implies non-empty steps with valid tools.
package ai

import (
	"testing"
)

func FuzzParsePlanFromModelOutput(f *testing.F) {
	seeds := []string{
		`{"intent":"chat","steps":[{"tool":"response","message":"hi"}]}`,
		"```json\n" + `{"intent":"shell","steps":[{"tool":"shell","command":"ls"}]}` + "\n```",
		`{"intent":"multi_step","steps":[{"tool":"git","action":"add","args":{"paths":"README.md"}},{"tool":"git","action":"commit","args":{"message":"release v1.1.0"}}]}`,
		`{"intent":"chat","steps":[`, // unbalanced
		`not json at all`,
		`{"intent":"shell","steps":[{"tool":"shell","command":"rm -rf /"}]}`,
		`{"intent":"package","steps":[{"tool":"package","action":"install","args":{"name":"vim"}}]}`,
		`{"intent":"recon","steps":[{"tool":"recon","action":"nmap","args":{"target":"127.0.0.1"}}]}`,
		`{"intent":"chat","steps":[{"tool":"vision","action":"look","args":{"prompt":"what is this"}}]}`,
		"{\n  \"intent\": \"shell\",\n  \"steps\": [\n    {\n      \"tool\": \"shell\",\n      \"command\": \"echo \\\"nested \\\\\\\"quotes\\\\\\\"\\\"\"\n    }\n  ]\n}",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		plan, err := ParsePlanFromModelOutput(input)
		if err == nil {
			// Invariant: nil-error implies non-empty steps.
			if len(plan.Steps) == 0 {
				t.Fatal("parsed plan has no steps but returned no error")
			}
			// Invariant: tools must be in allowed set.
			validTools := map[string]bool{
				"response": true, "shell": true, "git": true, "package": true, "recon": true,
				"web": true, "vision": true,
			}
			for _, step := range plan.Steps {
				if !validTools[step.Tool] {
					t.Fatalf("invalid tool in parsed plan: %s", step.Tool)
				}
			}
		}
	})
}
