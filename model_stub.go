package main

import (
	"fmt"
	"strings"
)

// RunModel is a development stub. Replace with real llama.cpp integration.
func RunModel(prompt string) (string, error) {
	// TODO: wire up llama.cpp via cgo or call external inference process
	// For now, return a deterministic stubbed response for quick testing.
	fmt.Println("[model] prompt:", prompt)
	// Simple heuristic: if prompt contains "install" return an install command.
	if containsIgnoreCase(prompt, "install") && containsIgnoreCase(prompt, "git") {
		return "git --version || echo 'simulate install git'", nil
	}
	return "echo 'Helix (stub): I heard you'", nil
}

func containsIgnoreCase(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
