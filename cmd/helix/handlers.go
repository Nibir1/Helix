// cmd/helix/handlers.go
// Purpose: Slash-command handlers for Helix.
// Author: Helix Red Team
// Date: 2026-05-09

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/utils"

	"github.com/fatih/color"
)

// -------------------------------------------------------
// MASTER DISPATCHER – called by the agent when input
// starts with "/". Returns true if handled.
// -------------------------------------------------------
func handleSlashCommand(input string) bool {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return false
	}

	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/sandbox":
		handleSandboxCommand(input)
	case "/cd":
		handleChangeDirectory(input)
	case "/git":
		handleGitCommand(input)
	case "/rag-status":
		handleRAGStatus()
	case "/rag-reindex":
		handleRAGReindex()
	case "/rag-reset":
		handleRAGReset()
	case "/rag-rebuild":
		handleRAGRebuild()
	case "/dry-run":
		toggleDryRun()
	case "/online":
		checkOnlineStatus()
	case "/test-basic-ai":
		testBasicAI()
	case "/stealth":
		handleStealthCommand(input)
	case "/scan":
		handleQuickScan(parts)
	default:
		return false // unknown – let the AI handle it
	}

	return true
}

// -------------------------------------------------------
// /sandbox
// -------------------------------------------------------
func handleSandboxCommand(input string) {
	args := strings.Fields(input)
	if len(args) < 2 {
		sandbox.PrintStatus()
		color.Yellow("Usage: /sandbox <mode>")
		color.Yellow("Modes: off, current, strict")
		return
	}

	mode := strings.ToLower(args[1])
	switch mode {
	case "off", "disable", "none":
		sandbox.SetMode(commands.SandboxDisabled)

	case "current", "dir", "normal":
		sandbox.SetMode(commands.SandboxCurrentDir)

	case "strict", "tight", "restricted":
		sandbox.SetMode(commands.SandboxStrict)

	default:
		color.Red("Unknown sandbox mode: %s", mode)
		color.Yellow("Valid modes: off, current, strict")
	}
}

// -------------------------------------------------------
// /cd
// -------------------------------------------------------
func handleChangeDirectory(input string) {
	targetDir := strings.TrimSpace(strings.TrimPrefix(input, "/cd"))
	if targetDir == "" {
		current, _ := os.Getwd()
		color.Cyan("Current directory: %s", current)
		return
	}

	if err := sandbox.ChangeDirectory(targetDir); err != nil {
		color.Red("Failed to change directory: %v", err)
	}
}

// -------------------------------------------------------
// /git
// -------------------------------------------------------
func handleGitCommand(input string) {
	request := strings.TrimSpace(strings.TrimPrefix(input, "/git"))
	if request == "" {
		color.Red("Usage: /git <natural-language git operation>")
		return
	}

	if err := gitManager.HandleGitRequest(request); err != nil {
		color.Red("Git operation failed: %v", err)
	}
}

// -------------------------------------------------------
// /rag-status
// -------------------------------------------------------
func handleRAGStatus() {
	color.Cyan("RAG System Status:")

	if ragSystem == nil {
		color.Red("RAG system not initialized")
		color.Yellow("RAG will be set up automatically on next start.")
		return
	}

	stats := ragSystem.GetSystemStats()
	statusText := ragSystem.GetInitializationStatus()

	initialized, _ := stats["initialized"].(bool)
	indexedPages := stats["indexed_pages"]

	color.Cyan("Statistics:")
	color.Cyan("  • Status: %v", statusText)
	color.Cyan("  • Initialized: %v", initialized)
	color.Cyan("  • Indexed MAN Pages: %v", indexedPages)

	if initialized {
		if totalDocs, ok := stats["total_documents"]; ok {
			color.Cyan("    • Vector Documents: %v", totalDocs)
		}
		if unique, ok := stats["unique_commands"]; ok {
			color.Cyan("    • Unique Commands: %v", unique)
		}
		color.Green("RAG system ACTIVE and ready for retrieval")
	} else {
		switch v := indexedPages.(type) {
		case int:
			if v > 0 {
				color.Yellow("RAG indexing in progress or partially completed…")
				color.Yellow("Some context may be available but system is not fully ready.")
				return
			}
		}
		color.Yellow("RAG not initialized yet (no MAN pages indexed).")
		color.Yellow("It will automatically index MAN pages when Helix initializes.")
	}
}

// -------------------------------------------------------
// /rag-reindex
// -------------------------------------------------------
func handleRAGReindex() {
	if ragSystem == nil {
		color.Red("RAG system not initialized")
		return
	}

	color.Blue("Forcing full RAG reindex…")

	home, _ := os.UserHomeDir()
	ragDir := filepath.Join(home, ".helix", "rag_index")

	if err := os.RemoveAll(ragDir); err != nil {
		color.Red("Failed to clear existing RAG index: %v", err)
		return
	}

	color.Green("Existing RAG index removed. Starting background rebuild…")

	go func() {
		color.Blue("Background RAG reindex started...")
		if err := ragSystem.Initialize(); err != nil {
			color.Yellow("Background RAG reindex completed with issues: %v", err)
		} else if ragSystem.IsInitialized() {
			color.Green("Background RAG reindex completed successfully")
		} else {
			color.Yellow("Background RAG reindex completed but system not fully initialized")
		}
	}()

	color.Green("RAG reindex requested (running in background)")
}

// -------------------------------------------------------
// /rag-reset
// -------------------------------------------------------
func handleRAGReset() {
	if ragSystem == nil {
		color.Red("RAG system not initialized")
		return
	}

	color.Blue("Resetting all RAG data…")

	home, _ := os.UserHomeDir()
	ragDir := filepath.Join(home, ".helix", "rag_index")

	if err := os.RemoveAll(ragDir); err != nil {
		color.Red("Failed to reset RAG data: %v", err)
		return
	}

	color.Green("RAG reset completed.")
	color.Yellow("Restart Helix or run /rag-reindex or /rag-rebuild to regenerate the index.")
}

// -------------------------------------------------------
// /rag-rebuild
// -------------------------------------------------------
func handleRAGRebuild() {
	if ragSystem == nil {
		color.Red("RAG system not initialized")
		color.Yellow("Start Helix normally first so the RAG system is created, then run /rag-rebuild.")
		return
	}

	color.Yellow("Full RAG REBUILD will:")
	color.Yellow("  • Delete all cached MAN page embeddings")
	color.Yellow("  • Re-scan MAN pages and rebuild the vector index")
	color.Yellow("  • This may take several minutes on first run")
	fmt.Print("Proceed with full rebuild now? [y/N]: ")

	var answer string
	if _, err := fmt.Scanln(&answer); err != nil {
		answer = ""
	}

	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		color.Yellow("RAG rebuild cancelled by user")
		return
	}

	color.Blue("Clearing existing RAG index…")

	home, _ := os.UserHomeDir()
	ragDir := filepath.Join(home, ".helix", "rag_index")

	if err := os.RemoveAll(ragDir); err != nil {
		color.Red("Failed to remove RAG index directory: %v", err)
		return
	}

	color.Green("RAG index directory cleared.")
	color.Blue("Starting full RAG rebuild (this may take a while)...")

	if err := ragSystem.Initialize(); err != nil {
		color.Red("RAG rebuild failed: %v", err)
		return
	}

	if ragSystem.IsInitialized() {
		color.Green("RAG rebuild completed successfully and is now ACTIVE.")
	} else {
		color.Yellow("RAG rebuild finished but system reported as not fully initialized.")
		color.Yellow("You may want to check /rag-status for details.")
	}
}

// -------------------------------------------------------
// /dry-run
// -------------------------------------------------------
func toggleDryRun() {
	execConfig.DryRun = !execConfig.DryRun
	if execConfig.DryRun {
		color.Yellow("Dry-run mode ENABLED — commands will not execute")
	} else {
		color.Green("Dry-run mode DISABLED — commands will run normally")
	}
}

// -------------------------------------------------------
// /online
// -------------------------------------------------------
func checkOnlineStatus() {
	color.Blue("Checking internet connectivity…")

	if utils.IsOnline(3 * 1_000_000_000) { // 3s
		color.Green("Online — real-time capabilities available")
	} else {
		color.Yellow("Offline — using local AI only")
	}
}

// -------------------------------------------------------
// /test-basic-ai
// -------------------------------------------------------
func testBasicAI() {
	color.Cyan("Testing basic AI model responses…")

	resp1, err := ai.RunModel("Say 'hello world'")
	if err != nil {
		color.Red("Test 1 failed: %v", err)
	} else {
		color.Green("Test 1: %s", strings.TrimSpace(resp1))
	}

	resp2, err := ai.RunModel("Command to list files:")
	if err != nil {
		color.Red("Test 2 failed: %v", err)
	} else {
		color.Green("Test 2: %s", strings.TrimSpace(resp2))
	}
}

// -------------------------------------------------------
// /stealth
// -------------------------------------------------------
func handleStealthCommand(input string) {
	args := strings.Fields(input)
	if len(args) < 2 {
		if agentCore != nil {
			color.Cyan("Stealth mode: %v", agentCore.IsStealthEnabled())
		}
		color.Yellow("Usage: /stealth <on|off>")
		return
	}

	switch strings.ToLower(args[1]) {
	case "on", "enable":
		if agentCore != nil {
			agentCore.EnableStealth(true)
		}
	case "off", "disable":
		if agentCore != nil {
			agentCore.EnableStealth(false)
		}
	default:
		color.Red("Unknown stealth mode: %s", args[1])
	}
}

// -------------------------------------------------------
// /scan – manual recon test
// -------------------------------------------------------
func handleQuickScan(args []string) {
	if len(args) < 2 {
		color.Cyan("Usage: /scan <target>")
		return
	}
	target := args[1]
	if agentCore == nil {
		color.Red("Agent not initialized")
		return
	}
	result, err := agentCore.RunReconTool("nmap", "-sV", target)
	if err != nil {
		color.Red("Recon failed: %v", err)
		return
	}
	color.Green("Recon completed in %v", result.Elapsed)
	if result.Error != nil {
		color.Yellow("Tool error: %v", result.Error)
	} else {
		color.Cyan("Parsed results:")
		for k, v := range result.Parsed {
			color.Cyan("  %s: %v", k, v)
		}
	}
}
