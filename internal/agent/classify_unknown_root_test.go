// internal/agent/classify_unknown_root_test.go
// Purpose: typed requests whose first word is English and whose other words
// hold a path scored as shell at full confidence and ran verbatim in zsh
// ("command not found: run"). Metabolism's automated day of 2026-10-07 lost 10
// of 100 turns to it. The phrasings below are those turns.
package agent

import (
	"errors"
	"testing"

	"helix/internal/input"
	"helix/internal/shell"
)

var typedRequestsWithPaths = []string{
	"run go test ./internal/providers/... in Development/Personal/Metabolism/.runs/auto/workspace/Helix",
	"run go build ./... in Development/Personal/Metabolism/.runs/auto/workspace/Metabolism",
	"run go vet ./... in Development/Personal/Metabolism/.runs/auto/workspace/Helix",
	"summarise CLAUDE.md in Development/Personal/Metabolism/.runs/auto/workspace/Metabolism",
}

func withLookPath(t *testing.T, found map[string]bool) {
	t.Helper()
	prev := lookPath
	lookPath = func(name string) (string, error) {
		if found[name] {
			return "/usr/local/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { lookPath = prev })
}

func TestUnknownRootNotOnPathGoesToPlanner(t *testing.T) {
	withLookPath(t, nil)
	a := &Agent{channel: input.ChannelText}
	for _, text := range typedRequestsWithPaths {
		c := shell.Classify(text)
		if c.Kind == shell.KindShellCommand && c.KnownRoot {
			t.Errorf("%q: root %q should not count as known", text, c.RootCommand)
		}
		if a.directShellAllowed(c) {
			t.Errorf("%q: bypassed the planner (kind=%s confidence=%.2f)", text, c.Kind, c.Confidence)
		}
	}
}

func TestUnknownRootOnPathStillRunsAsTyped(t *testing.T) {
	withLookPath(t, map[string]bool{"kubectx": true})
	a := &Agent{channel: input.ChannelText}
	c := shell.Classify("kubectx --current")
	if c.KnownRoot {
		t.Fatalf("kubectx is not in the known list; the test needs an unknown root")
	}
	if !a.directShellAllowed(c) {
		t.Fatalf("an executable on PATH typed with flags should still run directly (kind=%s confidence=%.2f)", c.Kind, c.Confidence)
	}
}

func TestKnownRootsDoNotNeedPath(t *testing.T) {
	withLookPath(t, nil)
	a := &Agent{channel: input.ChannelText}
	for _, text := range []string{"ls -la", "git status", "cat README.md", "./build.sh --fast", "FOO=1 make"} {
		c := shell.Classify(text)
		if !c.KnownRoot {
			t.Errorf("%q: root %q should count as known", text, c.RootCommand)
		}
		if !a.directShellAllowed(c) {
			t.Errorf("%q: should run directly (kind=%s confidence=%.2f)", text, c.Kind, c.Confidence)
		}
	}
}
