// cmd/helix/purge.go
//
// Purpose: /purge — full local data wipe for a clean-slate restart.
// Deletes provider API keys, the knowledge database (plus SQLite journals),
// all vector/RAG/MAN caches, preferences, history, logs, and temp artifacts
// after an explicit warning and y/N confirmation. Model weights are removed
// only with a second explicit confirmation.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"helix/internal/commands"

	"github.com/fatih/color"
)

// purgeTarget describes one filesystem path removed by /purge.
type purgeTarget struct {
	path string
	desc string
}

// handlePurgeCommand implements the /purge flow:
// warning banner → y/N confirmation → optional model wipe → deletion report.
//
// Args: none.
// Returns: none.
// Complexity: O(n) in the number of purge targets.
func handlePurgeCommand() {
	home, err := os.UserHomeDir()
	if err != nil {
		color.Red("Purge failed: cannot resolve home directory: %v", err)
		return
	}
	helixDir := filepath.Join(home, ".helix")

	// Core data targets: secrets, knowledge DB (+ journals), caches, prefs,
	// and history. Every one of these is recreated automatically on next boot.
	targets := []purgeTarget{
		{filepath.Join(helixDir, "secrets.json"), "provider API keys (all providers)"},
		{filepath.Join(helixDir, "openai_api_key"), "legacy OpenAI key file"},
		{filepath.Join(helixDir, "helix.db"), "knowledge database (CVE / KEV / Exploit-DB / MITRE + FTS)"},
		{filepath.Join(helixDir, "helix.db-wal"), "SQLite WAL journal"},
		{filepath.Join(helixDir, "helix.db-shm"), "SQLite shared-memory journal"},
		{filepath.Join(helixDir, "rag_index"), "RAG state and index cache"},
		{filepath.Join(helixDir, "vector_index"), "vector embedding index"},
		{filepath.Join(helixDir, "man_index"), "MAN page index cache"},
		{filepath.Join(helixDir, "config.json"), "user preferences and provider settings"},
		{filepath.Join(home, ".helix_history"), "command history"},
		{filepath.Join(helixDir, ".helix_history"), "legacy command history"},
	}

	// Sweep log, temp, and crash-report artifacts from the Helix home.
	for _, pattern := range []string{"*.log", "*.tmp", "crash-*.json"} {
		matches, _ := filepath.Glob(filepath.Join(helixDir, pattern))
		for _, m := range matches {
			targets = append(targets, purgeTarget{path: m, desc: "log/temp artifact"})
		}
	}

	// MANDATORY WARNING: show exactly what exists and will be destroyed.
	color.Red("⚠  HELIX PURGE PROTOCOL")
	color.Yellow("This will PERMANENTLY delete the following local data:")
	for _, t := range targets {
		if pathExists(t.path) {
			color.Yellow("   • %s — %s", t.path, t.desc)
		}
	}
	color.Yellow("Downloaded model weights and the llama.cpp runtime are NOT deleted by default.")
	fmt.Println()

	if !commands.AskForConfirmation("Proceed with FULL PURGE of all Helix data?") {
		color.Yellow("Purge cancelled. Nothing was deleted.")
		return
	}

	// Optional second confirmation for the large model-weight directory.
	if commands.AskForConfirmation("Also delete downloaded model weights (~/.helix/models)?") {
		targets = append(targets, purgeTarget{
			path: filepath.Join(helixDir, "models"),
			desc: "downloaded model weights",
		})
	}

	deleted, failed := 0, 0
	for _, t := range targets {
		if !pathExists(t.path) {
			continue
		}
		if err := os.RemoveAll(t.path); err != nil {
			color.Red("   ✖ %s: %v", t.path, err)
			failed++
			continue
		}
		deleted++
	}

	fmt.Println()
	if failed > 0 {
		color.Yellow("Purge finished with %d deletion(s) and %d failure(s).", deleted, failed)
	} else {
		color.Green("✔ PURGE COMPLETE — %d item(s) deleted. Helix is a blank slate.", deleted)
	}
	color.Yellow("Restart Helix now to finish starting fresh (open DB handles close on exit).")
}

// pathExists reports whether a file or directory exists on disk.
//
// Args:
//   - path: filesystem path.
//
// Returns: bool.
// Complexity: O(1).
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
