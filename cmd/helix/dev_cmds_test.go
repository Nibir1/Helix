package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"helix/internal/session"
)

func TestStripCodeFence(t *testing.T) {
	cases := []struct{ in, want string }{
		{"# Title\ncontent", "# Title\ncontent"},
		{"```markdown\n# Title\ncontent\n```", "# Title\ncontent"},
		{"```\nplain\n```", "plain"},
		{"  ```md\nx\n```  ", "x"},
		// A fence in the middle is content, not a wrapper.
		{"before\n```\nmid\n```\nafter", "before\n```\nmid\n```\nafter"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := stripCodeFence(tc.in); got != tc.want {
			t.Errorf("stripCodeFence(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSplitDiffArgs(t *testing.T) {
	cases := []struct {
		fields     []string
		wantStaged bool
		wantPaths  []string
	}{
		{nil, false, nil},
		{[]string{"--staged"}, true, nil},
		{[]string{"--cached"}, true, nil},
		{[]string{"-s"}, true, nil},
		{[]string{"internal/", "cmd/"}, false, []string{"internal/", "cmd/"}},
		{[]string{"--staged", "internal/"}, true, []string{"internal/"}},
		{[]string{"internal/", "--staged"}, true, []string{"internal/"}},
	}
	for _, tc := range cases {
		staged, paths := splitDiffArgs(tc.fields)
		if staged != tc.wantStaged {
			t.Errorf("%v: staged = %v, want %v", tc.fields, staged, tc.wantStaged)
		}
		if strings.Join(paths, ",") != strings.Join(tc.wantPaths, ",") {
			t.Errorf("%v: paths = %v, want %v", tc.fields, paths, tc.wantPaths)
		}
	}
}

// TestLooksLikeURL: an argument that merely mentions a domain must be SEARCHED,
// not fetched — fetching what the user meant to search is a surprising request
// to an address they did not choose.
func TestLooksLikeURL(t *testing.T) {
	urls := []string{"https://example.com", "http://example.com/a?b=c", "  https://x.dev/y  "}
	for _, u := range urls {
		if !looksLikeURL(u) {
			t.Errorf("looksLikeURL(%q) = false, want true", u)
		}
	}
	notURLs := []string{
		"example.com",                 // bare host: ambiguous, so search
		"what is https://example.com", // a question containing a URL
		"ftp://example.com",           // not http(s)
		"https://a.com https://b.com", // two of them
		"",
	}
	for _, s := range notURLs {
		if looksLikeURL(s) {
			t.Errorf("looksLikeURL(%q) = true, want false", s)
		}
	}
}

func TestProjectContextNamesAndDiscovery(t *testing.T) {
	names := projectContextNames()
	if len(names) == 0 || names[0] != "HELIX.md" {
		t.Fatalf("HELIX.md must be the first-choice project file, got %v", names)
	}

	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "HELIX.md"), []byte("# Root project\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Discovery walks UP: running Helix from a subdirectory must still find the
	// repository's own notes.
	t.Chdir(nested)
	content, path, ok := loadProjectContext()
	if !ok {
		t.Fatal("project context was not found from a subdirectory")
	}
	if !strings.Contains(content, "Root project") {
		t.Errorf("content = %q", content)
	}
	if filepath.Base(path) != "HELIX.md" {
		t.Errorf("path = %q", path)
	}

	// The nearest file wins, so a subproject can override the root.
	if err := os.WriteFile(filepath.Join(nested, "HELIX.md"), []byte("# Nested project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, _, ok = loadProjectContext()
	if !ok || !strings.Contains(content, "Nested project") {
		t.Errorf("the nearest file must win, got %q", content)
	}
}

func TestLoadProjectContextIgnoresBlankFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "HELIX.md"), []byte("   \n\t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := loadProjectContext(); ok {
		t.Error("a whitespace-only project file must not count as context")
	}
}

func TestLoadProjectContextIsBounded(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	huge := strings.Repeat("x", maxProjectContextBytes*3)
	if err := os.WriteFile(filepath.Join(dir, "HELIX.md"), []byte(huge), 0o644); err != nil {
		t.Fatal(err)
	}
	content, _, ok := loadProjectContext()
	if !ok {
		t.Fatal("expected the file to be found")
	}
	if len(content) > maxProjectContextBytes {
		t.Errorf("loaded %d bytes, want at most %d", len(content), maxProjectContextBytes)
	}
}

func TestExportPathDefaultsAndOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := exportPath("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, filepath.Join(home, ".helix", "exports")) {
		t.Errorf("default export path = %q, want it under ~/.helix/exports", got)
	}
	if !strings.HasSuffix(got, ".md") {
		t.Errorf("default export path %q should be a Markdown file", got)
	}

	// A directory argument keeps the generated filename.
	dir := t.TempDir()
	got, err = exportPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got) != dir || !strings.HasSuffix(got, ".md") {
		t.Errorf("directory argument = %q, want a generated name inside %q", got, dir)
	}

	// An explicit file is used verbatim (made absolute).
	explicit := filepath.Join(dir, "notes.md")
	got, err = exportPath(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if got != explicit {
		t.Errorf("explicit path = %q, want %q", got, explicit)
	}

	// ~ expansion, because people type it.
	got, err = exportPath("~/out.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(home, "out.md") {
		t.Errorf("tilde path = %q, want %q", got, filepath.Join(home, "out.md"))
	}
}

// TestRenderTranscriptQuotesContent: a transcript containing its own Markdown
// headings or fences must not restructure the exported document around itself.
func TestRenderTranscriptQuotesContent(t *testing.T) {
	turns := []session.Turn{{
		Channel:  "voice",
		UserText: "# Not a heading\n```\nfenced\n```",
		Reply:    "line one\nline two",
	}}
	out := renderTranscript(turns)

	if !strings.Contains(out, "# Helix session transcript") {
		t.Error("the transcript needs its own title")
	}
	if !strings.Contains(out, "voice") {
		t.Error("the channel should be recorded")
	}
	for _, line := range []string{"> # Not a heading", "> ```", "> line one", "> line two"} {
		if !strings.Contains(out, line) {
			t.Errorf("expected quoted line %q in:\n%s", line, out)
		}
	}
	// No unquoted fence may survive, or the document structure breaks.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "```") {
			t.Errorf("unquoted fence leaked into the transcript: %q", line)
		}
	}
}

func TestQuoteBlockHandlesEmpty(t *testing.T) {
	if got := quoteBlock("   "); !strings.HasPrefix(got, "> ") {
		t.Errorf("quoteBlock on blank input = %q, want a quoted placeholder", got)
	}
}
