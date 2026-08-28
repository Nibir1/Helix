// internal/shell/completion_test.go
// Purpose: Tab completion — the pure helpers plus the editor's own
// word-boundary logic, exercised without a TTY.
package shell

import (
	"os"
	"strings"
	"testing"
)

func TestMatchPrefix(t *testing.T) {
	names := []string{"/rag-rebuild", "/rag-reindex", "/rag-reset", "/rag-status", "/help"}

	got := matchPrefix(names, "/rag-re")
	want := []string{"/rag-rebuild", "/rag-reindex", "/rag-reset"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("matchPrefix = %v, want %v", got, want)
	}
	if got := matchPrefix(names, "/h"); len(got) != 1 || got[0] != "/help" {
		t.Errorf("single match = %v, want [/help]", got)
	}
	if got := matchPrefix(names, "/zzz"); len(got) != 0 {
		t.Errorf("no match should be empty, got %v", got)
	}
	if got := matchPrefix(names, ""); len(got) != len(names) {
		t.Errorf("empty prefix should match everything, got %d", len(got))
	}
}

func TestLongestCommonPrefix(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"/help"}, "/help"},
		{[]string{"/rag-rebuild", "/rag-reindex", "/rag-reset"}, "/rag-re"},
		{[]string{"/voice", "/voice-setup", "/voice-status"}, "/voice"},
		{[]string{"/help", "/todo"}, "/"},
		{[]string{"abc", "xyz"}, ""},
	}
	for _, tc := range cases {
		if got := longestCommonPrefix(tc.in); got != tc.want {
			t.Errorf("longestCommonPrefix(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSetAndGetSlashCommands(t *testing.T) {
	original := SlashCommands()
	t.Cleanup(func() { SetSlashCommands(original) })

	SetSlashCommands([]string{"/alpha", "/beta"})
	got := SlashCommands()
	if len(got) != 2 || got[0] != "/alpha" {
		t.Fatalf("SlashCommands = %v", got)
	}

	// The getter must hand back a copy; a caller mutating it must not corrupt
	// the registry the line editor completes against.
	got[0] = "/mutated"
	if again := SlashCommands(); again[0] != "/alpha" {
		t.Errorf("the published list was mutated through the returned slice: %v", again)
	}
}

// newCompletionEditor builds an editor with a buffer, bypassing the TTY.
func newCompletionEditor(line string) *editor {
	e := &editor{width: 80, buf: []rune(line)}
	e.cursor = len(e.buf)
	return e
}

func TestCompleteSlashCommandUnique(t *testing.T) {
	original := SlashCommands()
	t.Cleanup(func() { SetSlashCommands(original) })
	SetSlashCommands([]string{"/permissions", "/plan", "/purge"})

	e := newCompletionEditor("/pl")
	e.completeAtCursor()
	if got := string(e.buf); got != "/plan" {
		t.Errorf("buffer = %q, want /plan", got)
	}
	if e.cursor != len(e.buf) {
		t.Errorf("cursor = %d, want %d (end of the inserted word)", e.cursor, len(e.buf))
	}
}

// TestCompleteSlashCommandExtendsToCommonPrefix is the behavior the old
// completer lacked: it only ever inserted on a UNIQUE match, so Tab did nothing
// at all on a shared stem like "/rag-".
func TestCompleteSlashCommandExtendsToCommonPrefix(t *testing.T) {
	original := SlashCommands()
	t.Cleanup(func() { SetSlashCommands(original) })
	SetSlashCommands([]string{"/rag-rebuild", "/rag-reindex", "/rag-reset", "/help"})

	e := newCompletionEditor("/rag")
	e.completeAtCursor()
	if got := string(e.buf); got != "/rag-re" {
		t.Errorf("buffer = %q, want the common prefix /rag-re", got)
	}
}

func TestCompleteSlashCommandIsCaseInsensitive(t *testing.T) {
	original := SlashCommands()
	t.Cleanup(func() { SetSlashCommands(original) })
	SetSlashCommands([]string{"/status"})

	e := newCompletionEditor("/STAT")
	e.completeAtCursor()
	if got := string(e.buf); got != "/status" {
		t.Errorf("buffer = %q, want /status (typed case should not defeat completion)", got)
	}
}

// TestCompleteDoesNotTreatPathsAsCommands: "/usr/bin/gi" is a path, not a verb.
func TestCompleteDoesNotTreatPathsAsCommands(t *testing.T) {
	original := SlashCommands()
	t.Cleanup(func() { SetSlashCommands(original) })
	SetSlashCommands([]string{"/git"})

	e := newCompletionEditor("/usr/bin/definitely-not-here-xyz")
	before := string(e.buf)
	e.completeAtCursor()
	if got := string(e.buf); got != before {
		t.Errorf("a path with no filesystem match must be left alone, got %q", got)
	}
}

// TestCompleteOnlyCompletesTheFirstWord: an argument is a path, even when it
// happens to look like a command name.
func TestCompleteOnlyCompletesTheFirstWord(t *testing.T) {
	original := SlashCommands()
	t.Cleanup(func() { SetSlashCommands(original) })
	SetSlashCommands([]string{"/status"})

	e := newCompletionEditor("/help /stat")
	before := string(e.buf)
	e.completeAtCursor()
	if got := string(e.buf); got != before {
		t.Errorf("the second word must not be command-completed, got %q", got)
	}
}

func TestCompleteEmptyBufferIsNoOp(t *testing.T) {
	e := newCompletionEditor("")
	e.completeAtCursor()
	if got := string(e.buf); got != "" {
		t.Errorf("buffer = %q, want it unchanged", got)
	}
}

// TestCompletePreservesTextAfterTheCursor: completing mid-line must not eat the
// rest of the line.
func TestCompletePreservesTextAfterTheCursor(t *testing.T) {
	original := SlashCommands()
	t.Cleanup(func() { SetSlashCommands(original) })
	SetSlashCommands([]string{"/status"})

	e := &editor{width: 80, buf: []rune("/stat extra")}
	e.cursor = 5 // just after "/stat"
	e.completeAtCursor()

	if got := string(e.buf); got != "/status extra" {
		t.Errorf("buffer = %q, want %q", got, "/status extra")
	}
	if e.cursor != len("/status") {
		t.Errorf("cursor = %d, want %d (end of the completed word)", e.cursor, len("/status"))
	}
}

func TestCompleteFilePath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := writeEmpty("uniquely-named-file.txt"); err != nil {
		t.Fatal(err)
	}

	e := newCompletionEditor("cat uniquely")
	e.completeAtCursor()
	if got := string(e.buf); got != "cat uniquely-named-file.txt" {
		t.Errorf("buffer = %q, want the completed path", got)
	}
}

func writeEmpty(name string) error {
	return os.WriteFile(name, nil, 0o600)
}
