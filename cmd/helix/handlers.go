package main

import (
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
		return
	}

	stats := ragSystem.GetSystemStats()
	color.Cyan("  📊 Statistics:")
	color.Cyan("    • Initialized: %v", stats["initialized"])
	color.Cyan("    • Indexed MAN Pages: %v", stats["indexed_pages"])

	if stats["initialized"].(bool) {
		color.Green("  ✅ RAG system ACTIVE")
		color.Cyan("    • Vector Documents: %v", stats["total_documents"])
		color.Cyan("    • Unique Commands: %v", stats["unique_commands"])
	} else {
		color.Yellow("  🔄 RAG indexing in progress…")
	}
}

// -------------------------------------------------------
//
//	/rag-reindex
//
// -------------------------------------------------------
func handleRAGReindex() {
	if ragSystem == nil {
		color.Red("❌ RAG system not initialized")
		return
	}

	color.Blue("🔄 Forcing full RAG reindex…")

	home, _ := os.UserHomeDir()
	stateFile := filepath.Join(home, ".helix", "rag_index", "rag_state.json")
	_ = os.Remove(stateFile)

	go ragSystem.IndexAvailableManPages()
	color.Green("✅ RAG reindex started in background")
}

// -------------------------------------------------------
//
//	/rag-reset
//
// -------------------------------------------------------
func handleRAGReset() {
	if ragSystem == nil {
		color.Red("❌ RAG system not initialized")
		return
	}

	color.Blue("🔄 Resetting all RAG data…")

	home, _ := os.UserHomeDir()
	ragDir := filepath.Join(home, ".helix", "rag_index")

	if err := os.RemoveAll(ragDir); err != nil {
		color.Red("❌ Failed to reset: %v", err)
		return
	}

	color.Green("✅ RAG reset completed. Will reindex on next start.")
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
