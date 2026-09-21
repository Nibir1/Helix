// cmd/helix/docs_tools_test.go
// Purpose: the documented tool vocabulary must be the real one.
//
// It had already drifted before anyone noticed: `vision` shipped in 2026-08 and
// the README's JSON schema sample and its feature list never learned about it,
// so two places told readers the planner had six tools when it had seven. Two
// more arrived since. A vocabulary published in four files and enforced in one
// is a vocabulary that will drift again, so the list is DERIVED from the code
// here and the documents are checked against it.
package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// liveToolVocabulary reads the closed set out of the planner's own validTools
// map, which is the thing the executor actually enforces.
func liveToolVocabulary(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("../../internal/ai/planner.go")
	if err != nil {
		t.Fatalf("read planner.go: %v", err)
	}
	body := string(src)

	start := strings.Index(body, "var validTools = map[string]bool{")
	if start < 0 {
		t.Fatal("validTools not found in planner.go — the vocabulary moved; re-point this test")
	}
	end := strings.Index(body[start:], "}")
	if end < 0 {
		t.Fatal("could not bound validTools")
	}
	block := body[start : start+end]

	var tools []string
	for _, m := range regexp.MustCompile(`"([a-z_]+)":\s*true`).FindAllStringSubmatch(block, -1) {
		tools = append(tools, m[1])
	}
	if len(tools) < 5 {
		t.Fatalf("only parsed %d tools from validTools; the walk is broken and this "+
			"test would pass by finding nothing", len(tools))
	}
	return tools
}

// Each file, and the passage in it that claims to list every tool.
var vocabularyClaims = []struct {
	file   string
	anchor string // a line that must contain the whole vocabulary
	what   string
}{
	{"../../README.md", `"tool": "response"`, "the planner schema sample"},
	{"../../README.md", "Unified multi-tool agent system:", "the feature list"},
	{"../../docs/architecture.md", "**Tool Use**: Supports", "the architecture summary"},
}

func TestDocumentedToolVocabularyMatchesTheCode(t *testing.T) {
	tools := liveToolVocabulary(t)

	for _, claim := range vocabularyClaims {
		src, err := os.ReadFile(claim.file)
		if err != nil {
			t.Fatalf("read %s: %v", claim.file, err)
		}
		var line string
		for _, l := range strings.Split(string(src), "\n") {
			if strings.Contains(l, claim.anchor) {
				line = l
				break
			}
		}
		if line == "" {
			t.Errorf("%s: %s is gone (anchor %q); re-point this test rather than "+
				"deleting it", claim.file, claim.what, claim.anchor)
			continue
		}
		for _, tool := range tools {
			if !strings.Contains(line, tool) {
				t.Errorf("%s: %s does not list %q, which the planner accepts:\n\t%s",
					claim.file, claim.what, tool, strings.TrimSpace(line))
			}
		}
	}
}

// And /tools must offer a row for each, or the command that exists to print the
// vocabulary prints an incomplete one.
func TestToolsCommandCoversEveryTool(t *testing.T) {
	tools := liveToolVocabulary(t)
	src, err := os.ReadFile("harness_cmds.go")
	if err != nil {
		t.Fatalf("read harness_cmds.go: %v", err)
	}
	body := string(src)

	start := strings.Index(body, "rows := []toolRow{")
	if start < 0 {
		t.Fatal("the /tools row table moved; re-point this test")
	}
	end := strings.Index(body[start:], "\n\t}")
	block := body[start : start+end]

	for _, tool := range tools {
		if !strings.Contains(block, `name: "`+tool+`"`) {
			t.Errorf("/tools has no row for %q, so the command that prints the "+
				"vocabulary prints an incomplete one", tool)
		}
	}
}
