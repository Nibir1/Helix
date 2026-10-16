package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"helix/internal/input"
)

// A turn that read from the web, or a file outside any repository, is
// recorded with what it consumed; one that read only the user's own
// repository is not (Metabolism D-038).
func TestRecordedTurnsCarryWhatTheyConsumed(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		obs  []StepObservation
		want string
	}{
		{"own repository", []StepObservation{{Tool: "file", Action: "read", OK: true, Path: filepath.Join(repo, "main.go")}}, ""},
		{"web", []StepObservation{{Tool: "web", OK: true}}, `"consumed":["retrieved-web"]`},
		{"outside", []StepObservation{{Tool: "file", Action: "read", OK: true, Path: filepath.Join(t.TempDir(), "x.txt")}}, `"consumed":["file-content"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, path := flipAgent(t, false, false, false)
			a.beginEpisode()
			a.recordSteps(tc.obs)
			a.turnPlanned = true
			a.finishEpisode(input.InputEvent{Text: "x"})
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			line := strings.SplitN(string(data), "\n", 2)[0]
			if tc.want == "" && strings.Contains(line, `"consumed"`) || tc.want != "" && !strings.Contains(line, tc.want) {
				t.Fatalf("want %q in:\n%s", tc.want, line)
			}
		})
	}
}
