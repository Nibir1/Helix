// cmd/helix/handlers.go

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
//
//	/sandbox
//
// -------------------------------------------------------
func handleSandboxCommand(input string) {
	args := strings.Fields(input)
	if len(args) < 2 {
		sandbox.PrintStatus()
		color.Yellow("💡 Usage: /sandbox <mode>")
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
		color.Red("❌ Unknown sandbox mode: %s", mode)
		color.Yellow("Valid modes: off, current, strict")
	}
}

// -------------------------------------------------------
//
//	/cd
//
// -------------------------------------------------------
func handleChangeDirectory(input string) {
	targetDir := strings.TrimSpace(strings.TrimPrefix(input, "/cd"))
	if targetDir == "" {
		current, _ := os.Getwd()
		color.Cyan("📁 Current directory: %s", current)
		return
	}

	if err := sandbox.ChangeDirectory(targetDir); err != nil {
		color.Red("❌ Failed to change directory: %v", err)
	}
}

// -------------------------------------------------------
//
//	/git
//
// -------------------------------------------------------
func handleGitCommand(input string) {
	request := strings.TrimSpace(strings.TrimPrefix(input, "/git"))
	if request == "" {
		color.Red("❌ Usage: /git <natural-language git operation>")
		return
	}

	if err := gitManager.HandleGitRequest(request); err != nil {
		color.Red("❌ Git operation failed: %v", err)
	}
}

// -------------------------------------------------------
//
//	/rag-status
//
// -------------------------------------------------------
func handleRAGStatus() {
	color.Cyan("🧠 RAG System Status:")

	if ragSystem == nil {
		color.Red("  ❌ RAG system not initialized")
		color.Yellow("  💡 RAG will be set up automatically on next start.")
		return
	}

	stats := ragSystem.GetSystemStats()
	statusText := ragSystem.GetInitializationStatus()

	initialized, _ := stats["initialized"].(bool)
	indexedPages := stats["indexed_pages"]

	color.Cyan("  📊 Statistics:")
	color.Cyan("    • Status: %v", statusText)
	color.Cyan("    • Initialized: %v", initialized)
	color.Cyan("    • Indexed MAN Pages: %v", indexedPages)

	if initialized {
		// Vector store details (if present)
		if totalDocs, ok := stats["total_documents"]; ok {
			color.Cyan("    • Vector Documents: %v", totalDocs)
		}
		if unique, ok := stats["unique_commands"]; ok {
			color.Cyan("    • Unique Commands: %v", unique)
		}
		color.Green("  ✅ RAG system ACTIVE and ready for retrieval")
	} else {
		// Not initialized — decide whether we're actually indexing or just empty
		switch v := indexedPages.(type) {
		case int:
			if v > 0 {
				color.Yellow("  🔄 RAG indexing in progress or partially completed…")
				color.Yellow("     Some context may be available but system is not fully ready.")
				return
			}
		}

		color.Yellow("  💤 RAG not initialized yet (no MAN pages indexed).")
		color.Yellow("  💡 It will automatically index MAN pages when Helix initializes.")
	}
}

// -------------------------------------------------------
//
//	/rag-reindex
//
// -------------------------------------------------------
//
// Force a full reindex in the background:
//   - Deletes the on-disk RAG index directory
//   - Keeps the current process running
//   - Starts a background Initialize() which will rebuild everything
func handleRAGReindex() {
	if ragSystem == nil {
		color.Red("❌ RAG system not initialized")
		return
	}

	color.Blue("🔄 Forcing full RAG reindex…")

	home, _ := os.UserHomeDir()
	ragDir := filepath.Join(home, ".helix", "rag_index")

	// Remove the entire index directory so Initialize() is forced to rebuild
	if err := os.RemoveAll(ragDir); err != nil {
		color.Red("❌ Failed to clear existing RAG index: %v", err)
		return
	}

	color.Green("✅ Existing RAG index removed. Starting background rebuild…")

	// Kick off a background re-initialize
	go func() {
		color.Blue("🔄 Background RAG reindex started...")
		if err := ragSystem.Initialize(); err != nil {
			color.Yellow("⚠️  Background RAG reindex completed with issues: %v", err)
		} else if ragSystem.IsInitialized() {
			color.Green("✅ Background RAG reindex completed successfully")
		} else {
			color.Yellow("⚠️  Background RAG reindex completed but system not fully initialized")
		}
	}()

	color.Green("✅ RAG reindex requested (running in background)")
}

// -------------------------------------------------------
//
//	/rag-reset
//
// -------------------------------------------------------
//
// Full reset of on-disk RAG data, but DOES NOT reindex immediately.
// The user is expected to restart Helix or call /rag-reindex or /rag-rebuild.
func handleRAGReset() {
	if ragSystem == nil {
		color.Red("❌ RAG system not initialized")
		return
	}

	color.Blue("🔄 Resetting all RAG data…")

	home, _ := os.UserHomeDir()
	ragDir := filepath.Join(home, ".helix", "rag_index")

	if err := os.RemoveAll(ragDir); err != nil {
		color.Red("❌ Failed to reset RAG data: %v", err)
		return
	}

	color.Green("✅ RAG reset completed.")
	color.Yellow("💡 Restart Helix or run /rag-reindex or /rag-rebuild to regenerate the index.")
}

// -------------------------------------------------------
//
//	/rag-rebuild
//
// -------------------------------------------------------
//
// Force a full synchronous rebuild of the RAG index.
//   - Deletes all RAG index files
//   - Runs Initialize() in the foreground (with progress logs)
//   - Best for “nuke and rebuild now” scenarios
func handleRAGRebuild() {
	if ragSystem == nil {
		color.Red("❌ RAG system not initialized")
		color.Yellow("💡 Start Helix normally first so the RAG system is created, then run /rag-rebuild.")
		return
	}

	color.Yellow("⚠️ Full RAG REBUILD will:")
	color.Yellow("   • Delete all cached MAN page embeddings")
	color.Yellow("   • Re-scan MAN pages and rebuild the vector index")
	color.Yellow("   • This may take several minutes on first run")
	fmt.Print("Proceed with full rebuild now? [y/N]: ")

	var answer string
	if _, err := fmt.Scanln(&answer); err != nil {
		// If user just hits enter, treat as "no"
		answer = ""
	}

	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		color.Yellow("❌ RAG rebuild cancelled by user")
		return
	}

	color.Blue("🧨 Clearing existing RAG index…")

	home, _ := os.UserHomeDir()
	ragDir := filepath.Join(home, ".helix", "rag_index")

	if err := os.RemoveAll(ragDir); err != nil {
		color.Red("❌ Failed to remove RAG index directory: %v", err)
		return
	}

	color.Green("✅ RAG index directory cleared.")
	color.Blue("🚀 Starting full RAG rebuild (this may take a while)...")

	// Synchronous rebuild using same Initialize() logic as startup
	if err := ragSystem.Initialize(); err != nil {
		color.Red("❌ RAG rebuild failed: %v", err)
		return
	}

	if ragSystem.IsInitialized() {
		color.Green("🎉 RAG rebuild completed successfully and is now ACTIVE.")
	} else {
		color.Yellow("⚠️ RAG rebuild finished but system reported as not fully initialized.")
		color.Yellow("   You may want to check /rag-status for details.")
	}
}

// -------------------------------------------------------
//
//	/dry-run
//
// -------------------------------------------------------
func toggleDryRun() {
	execConfig.DryRun = !execConfig.DryRun
	if execConfig.DryRun {
		color.Yellow("🔒 Dry-run mode ENABLED — commands will not execute")
	} else {
		color.Green("🚀 Dry-run mode DISABLED — commands will run normally")
	}
}

// -------------------------------------------------------
//
//	/online
//
// -------------------------------------------------------
func checkOnlineStatus() {
	color.Blue("🌐 Checking internet connectivity…")

	if utils.IsOnline(3 * 1_000_000_000) { // 3s
		color.Green("✅ Online — real-time capabilities available")
	} else {
		color.Yellow("⚠️ Offline — using local AI only")
	}
}

// -------------------------------------------------------
//
//	/test-basic-ai
//
// -------------------------------------------------------
func testBasicAI() {
	color.Cyan("🧪 Testing basic AI model responses…")

	// Test 1
	resp1, err := ai.RunModel("Say 'hello world'")
	if err != nil {
		color.Red("❌ Test 1 failed: %v", err)
	} else {
		color.Green("Test 1: %s", strings.TrimSpace(resp1))
	}

	// Test 2
	resp2, err := ai.RunModel("Command to list files:")
	if err != nil {
		color.Red("❌ Test 2 failed: %v", err)
	} else {
		color.Green("Test 2: %s", strings.TrimSpace(resp2))
	}
}
