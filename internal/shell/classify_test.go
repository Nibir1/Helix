// internal/shell/classify_test.go
// Purpose: Unit tests for the unified input classifier.
// Author:  Nahasat Nibir
// Dependencies: testing, internal/shell.
package shell

import "testing"

func TestClassifyEmpty(t *testing.T) {
	c := Classify("   ")
	if c.Kind != KindEmpty {
		t.Fatalf("expected empty, got %s", c.Kind)
	}
}

func TestClassifySlashCommand(t *testing.T) {
	c := Classify("/git undo last commit")
	if c.Kind != KindSlashCommand {
		t.Fatalf("expected slash-command, got %s", c.Kind)
	}
}

func TestClassifyShellCommands(t *testing.T) {
	cases := []string{
		"ls -la",
		"git status",
		"cat README.md",
		"cd /tmp",
		"echo hello",
		"grep -r foo .",
		"find . -name *.go",
		"rm -rf node_modules",
		"cat foo.txt | grep bar",
		"grep error main.go",
	}
	for _, in := range cases {
		c := Classify(in)
		if c.Kind != KindShellCommand {
			t.Errorf("input %q: expected shell-command, got %s (reason: %s)", in, c.Kind, c.Reason)
			continue
		}
		if c.Confidence < HighConfidence {
			t.Errorf("input %q: expected confidence >= %.2f for AI-bypass, got %.2f", in, HighConfidence, c.Confidence)
		}
	}
}

func TestClassifyNaturalLanguage(t *testing.T) {
	cases := []string{
		"what is a process",
		"why is my build failing?",
		"find all large files",
		"commit my changes with a message",
		"install git",
		"hello",
		"list all files",
		"show me the recent commits",
	}
	for _, in := range cases {
		c := Classify(in)
		if c.Kind != KindNaturalLanguage {
			t.Errorf("input %q: expected natural-language, got %s (reason: %s)", in, c.Kind, c.Reason)
		}
	}
}

func TestClassifyAmbiguousFind(t *testing.T) {
	shellCase := Classify("find . -name *.go")
	if shellCase.Kind != KindShellCommand {
		t.Fatalf("structural find should be shell, got %s", shellCase.Kind)
	}
	nlCase := Classify("find all large files")
	if nlCase.Kind != KindNaturalLanguage {
		t.Fatalf("spoken find should be natural language, got %s", nlCase.Kind)
	}
}
