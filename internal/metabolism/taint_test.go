package metabolism

import (
	"os"
	"path/filepath"
	"testing"
)

// Files in a git working tree are the user's own, except dependency
// directories; files elsewhere, and web content, are not (D-038, option b).
func TestTaint(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	cases := []struct {
		name, tool, action, path string
		ok                       bool
		want                     string
	}{
		{"own file", "file", "read", filepath.Join(repo, "main.go"), true, ""},
		{"own grep", "file", "grep", filepath.Join(repo, "internal"), true, ""},
		{"vendored file", "file", "read", filepath.Join(repo, "vendor", "x", "y.go"), true, ProvFileContent},
		{"node_modules", "file", "read", filepath.Join(repo, "web", "node_modules", "a.js"), true, ProvFileContent},
		{"outside any repo", "file", "read", filepath.Join(outside, "notes.txt"), true, ProvFileContent},
		{"a listing brings names, not content", "file", "list", outside, true, ""},
		{"a failed read brought nothing", "file", "read", filepath.Join(outside, "x"), false, ""},
		{"web", "web", "", "", true, ProvRetrievedWeb},
		{"shell", "shell", "", "", true, ""},
	}
	for _, c := range cases {
		if got := ConsumedBy(c.tool, c.action, c.path, c.ok); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A turn records each consumed provenance once, and the episode carries it.
func TestTurnConsumed(t *testing.T) {
	var tn Turn
	tn.AddConsumed(ProvRetrievedWeb)
	tn.AddConsumed(ProvRetrievedWeb)
	tn.AddConsumed(ProvFileContent)
	tn.AddConsumed("")
	if len(tn.consumed) != 2 {
		t.Fatalf("consumed %v", tn.consumed)
	}
}
