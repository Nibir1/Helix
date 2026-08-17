// cmd/helix/handlers.go
// Purpose: Master slash-command dispatcher and all Helix control handlers.
// Hardening: /knowledge-update, /rag-reindex, and /rag-rebuild now register
// cancellable contexts with the interrupt manager so Ctrl+C aborts the running
// pipeline and returns to a live prompt instead of killing Helix.
// Phase 15: Added /typewrite-all, on-demand NVD fetch, auto-install for recon
// tools, and animated Thinker progress bars for blocking operations.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/audio"
	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/confinement"
	"helix/internal/diagnostics"
	"helix/internal/edge"
	"helix/internal/ollama"
	"helix/internal/rag"
	"helix/internal/shell"
	"helix/internal/speech"
	"helix/internal/utils"
	"helix/internal/ux"

	"github.com/fatih/color"
)

// -------------------------------------------------------
// MASTER DISPATCHER
// -------------------------------------------------------

func handleSlashCommand(input string) bool {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return false
	}
	cmd := strings.ToLower(parts[0])
	switch cmd {
	case "/about":
		handleAbout()
	case "/help":
		handleHelp()
	case "/setup":
		handleSetup()
	case "/debug":
		handleDebugCommand(input)
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
	case "/explain":
		handleExplainCommand(input)
	case "/vuln", "/intel":
		handleVulnCommand(input)
	case "/knowledge-update":
		handleKnowledgeUpdate()
	case "/knowledge-status":
		handleKnowledgeStats()
	case "/knowledge-reindex":
		handleKnowledgeReindex()
	case "/doctor":
		handleDoctor()
	case "/provider-status":
		handleProviderStatus()
	case "/provider":
		handleProviderCommand(parts)
	case "/model":
		handleModelCommand(parts)
	case "/status":
		handleStatus()
	case "/audio":
		handleAudioCommand(input)
	case "/typewrite-all":
		handleTypewriteAllCommand(input)
	case "/voice-setup":
		handleVoiceSetup()
	case "/voice-status":
		handleVoiceStatus()
	case "/voice":
		handleVoiceCommand(input)
	case "/manual":
		handleManualCommand()
	case "/wake":
		handleWakeCommand(input)
	case "/say":
		handleSayCommand(input)
	case "/tts":
		handleTTSCommand(input)
	case "/listen":
		handleListenCommand(input)
	case "/mictest":
		handleMicTest()
	case "/agentic":
		handleAgenticCommand(input)
	case "/memory":
		handleMemoryCommand(input)
	case "/eyes":
		handleEyesCommand(input)
	case "/purge":
		handlePurgeCommand()
	case "/crash":
		handleCrashCommand(parts)
	default:
		// Absolute-path executables (e.g. /usr/bin/git) are NOT Helix
		// control commands; let the normal pipeline handle them exactly
		// as before. Real Helix commands never contain a second slash.
		if strings.Contains(cmd[1:], "/") {
			return false
		}
		handleUnknownSlashCommand(parts[0])
	}
	return true
}

// -------------------------------------------------------
// /about — Helix philosophy & the creation
// -------------------------------------------------------

func handleAbout() {
	shell.RenderAbout(config.HelixVersion)
	fmt.Println("  " + shell.Fg(shell.HexSecondary, "▸ THE PHILOSOPHY"))
	philosophy := []string{
		"Helix inverts the terminal: instead of forcing humans to speak",
		"machine, the machine learns to speak human. One prompt accepts",
		"shell, git, packages, and plain thought - no mode switching.",
		"",
		"Every action passes through a safety-first pipeline: validation,",
		"risk tiers, sandbox confinement, and typed confirmations for the",
		"dangerous paths. Power without recklessness.",
		"",
		"Knowledge is local and explainable - MAN pages, CVEs, MITRE",
		"ATT&CK - retrieved, cited, and defended. Helix thinks like the",
		"red team so it can fight for the blue.",
	}
	for _, l := range philosophy {
		fmt.Printf("  %s %s\n", shell.Fg(shell.HexSubtle, "│"), shell.Fg(shell.HexText, l))
	}
	fmt.Println()
	fmt.Println("  " + shell.Fg(shell.HexSecondary, "▸ THE CREATION"))
	creation := []string{
		"Helix is designed and built by Nahasat Nibir - an AI Engineer",
		"crafting intelligent, high-performance developer tools and",
		"AI-powered systems in Go and Rust.",
		"",
		"GitHub      https://github.com/Nibir1",
		"LinkedIn    https://www.linkedin.com/in/nibir-1/",
		"ArtStation  https://www.artstation.com/nibir",
	}
	for _, l := range creation {
		body := shell.Fg(shell.HexText, l)
		if strings.Contains(l, "http") {
			body = shell.Fg(shell.HexTertiary, l)
		}
		fmt.Printf("  %s %s\n", shell.Fg(shell.HexSubtle, "│"), body)
	}
	fmt.Println()
}

// -------------------------------------------------------
// /setup — Unified Setup Wizard
// -------------------------------------------------------

func handleSetup() {
	for {
		fmt.Println()
		fmt.Println("  " + shell.Fg(shell.HexPrimary, "⚡ HELIX SETUP WIZARD"))
		fmt.Println("  " + shell.Fg(shell.HexSubtle, strings.Repeat("─", 40)))
		fmt.Printf("  %s %s\n", shell.Fg(shell.HexTertiary, "1)"), shell.Fg(shell.HexText, "Set Identity (Name)"))
		fmt.Printf("  %s %s\n", shell.Fg(shell.HexTertiary, "2)"), shell.Fg(shell.HexText, "Configure AI Provider"))
		fmt.Printf("  %s %s\n", shell.Fg(shell.HexTertiary, "3)"), shell.Fg(shell.HexText, "Exit Setup"))
		fmt.Println()
		choice := strings.TrimSpace(commands.AskLine("  Select option (1-3)"))
		switch choice {
		case "1":
			currentName := cfg.UserPrefs.UserName
			if currentName == "" {
				currentName = "Nahasat Nibir"
			}
			fmt.Println("  " + shell.Fg(shell.HexSubtle, "Current Identity: ") + shell.Fg(shell.HexPrimary, currentName))
			newName := strings.TrimSpace(commands.AskLine("  Enter your name (or leave blank to cancel)"))
			if newName != "" {
				cfg.UserPrefs.UserName = newName
				if err := cfg.SavePreferences(); err != nil {
					color.Red("Failed to save preferences: %v", err)
				} else {
					shell.SetUserName(newName)
					fmt.Println("  " + shell.Fg(shell.HexSecondary, "✔ Identity updated to: ") + shell.Fg(shell.HexPrimary, newName))
				}
			}
		case "2":
			if err := runNativeSetup(); err != nil {
				color.Red("Provider setup failed: %v", err)
			} else {
				cfg.Provider = ai.ActiveProviderName()
				cfg.ProviderModel = ai.ActiveModel()
				_ = cfg.SavePreferences()
				fmt.Println("  " + shell.Fg(shell.HexSecondary, "✔ AI Provider configured: ") + shell.Fg(shell.HexPrimary, cfg.Provider+" ("+cfg.ProviderModel+")"))
			}
		case "3", "q", "exit", "":
			return
		default:
			fmt.Println("  " + shell.Fg(shell.HexRectifier, "Invalid selection."))
		}
	}
}

// -------------------------------------------------------
// /debug — Toggle Debug Logging
// -------------------------------------------------------

func handleDebugCommand(input string) {
	args := strings.Fields(input)
	if len(args) < 2 {
		current := "OFF"
		if utils.IsDebugMode() {
			current = "ON"
		}
		color.Cyan("Debug mode is currently: %s", current)
		color.Yellow("Usage: /debug <on|off>")
		return
	}
	switch strings.ToLower(args[1]) {
	case "on", "enable":
		utils.SetDebugMode(true)
		_ = os.Setenv("HELIX_DEBUG", "1")
		cfg.UserPrefs.DebugMode = true
		color.Green("Debug mode ENABLED")
	case "off", "disable":
		utils.SetDebugMode(false)
		_ = os.Unsetenv("HELIX_DEBUG")
		cfg.UserPrefs.DebugMode = false
		color.Yellow("Debug mode DISABLED")
	default:
		color.Red("Unknown debug setting: %s", args[1])
		color.Yellow("Usage: /debug <on|off>")
		return
	}
	_ = cfg.SavePreferences()
}

// -------------------------------------------------------
// /help — SOS Protocol
// -------------------------------------------------------

func handleHelp() {
	const colWidth = 28
	rule := "  " + shell.Fg(shell.HexSubtle, strings.Repeat("─", 70))
	fmt.Println()
	fmt.Println("  " + shell.Fg(shell.HexPrimary, "⚡ HELIX NATIVE SHELL") + " " + shell.Fg(shell.HexRectifier, "// SOS PROTOCOL"))
	fmt.Println(rule)
	fmt.Println("  " + shell.Fg(shell.HexSubtle, "AI-native shell · natural language · MAN pages · threat intelligence"))
	helpSection("CORE & NAVIGATION")
	helpLine(colWidth, "/about", "Helix philosophy, banner & creator")
	helpLine(colWidth, "/help", "Show this SOS menu")
	helpLine(colWidth, "/setup", "Unified setup wizard (Identity, AI Provider)")
	helpLine(colWidth, "/debug <on|off>", "Toggle verbose debug logging")
	helpLine(colWidth, "/cd <dir>", "Change directory (sandbox-aware)")
	helpLine(colWidth, "/status", "Check background RAG and AI provider status")
	helpLine(colWidth, "/doctor", "Run full system diagnostics")
	helpLine(colWidth, "/online", "Check internet connectivity")
	helpSection("AI & PROVIDERS")
	helpLine(colWidth, "/provider <name>", "Switch AI provider (openai, anthropic, ollama…)")
	helpLine(colWidth, "/provider-status", "Show detailed provider health and API keys")
	helpLine(colWidth, "/model <id>", "Switch active AI model")
	helpLine(colWidth, "/test-basic-ai", "Smoke test the active AI model")
	helpLine(colWidth, "/explain <cmd>", "AI-powered defensive analysis of a command")
	helpSection("RAG & KNOWLEDGE BASE")
	helpLine(colWidth, "/rag-status", "Show RAG indexing progress and vector stats")
	helpLine(colWidth, "/rag-reindex", "Trigger background RAG reindex")
	helpLine(colWidth, "/rag-rebuild", "Force full RAG knowledge base rebuild")
	helpLine(colWidth, "/rag-reset", "Wipe all RAG vector data")
	helpLine(colWidth, "/knowledge-update", "Fetch latest CVEs, CISA KEV, Exploits, MITRE")
	helpLine(colWidth, "/knowledge-status", "Show knowledge database row counts")
	helpLine(colWidth, "/knowledge-reindex", "Rebuild FTS5 search index")
	helpSection("SECURITY, RECON & STEALTH")
	helpLine(colWidth, "/vuln <query>", "Defensive vulnerability intel (CVE/EDB/MITRE lookup)")
	helpLine(colWidth, "/scan authorize <ip>", "Authorize recon target (written scope)")
	helpLine(colWidth, "/scan <ip>", "Run nmap/masscan on an authorized target")
	helpLine(colWidth, "/sandbox <mode>", "Directory confinement (off, current, strict)")
	helpLine(colWidth, "/stealth <on|off>", "Private history mode (suppresses shell history)")
	helpLine(colWidth, "/crash <list|view 1|clear>", "Inspect and manage local crash diagnostics")
	helpLine(colWidth, "/dry-run", "Toggle command execution preview mode")
	helpSection("UTILITIES & GIT")
	helpLine(colWidth, "/git <request>", "Natural language git operations with safety")
	helpLine(colWidth, "/audio <on|off>", "Toggle tonal audio feedback")
	helpLine(colWidth, "/typewrite-all <on|off>", "Toggle typewriter effect for ALL output")
	helpLine(colWidth, "/memory <show|clear>", "Show or wipe conversation memory")
	helpLine(colWidth, "/agentic <on|off>", "Iterative harness: observe step results & self-correct")
	helpSection("VOICE (BLACKBOX)")
	helpLine(colWidth, "/voice-setup", "Configure STT/TTS providers with live pricing")
	helpLine(colWidth, "/voice-status", "Speech chain health, keys, and recorder state")
	helpLine(colWidth, "/voice [off]", "Enter voice mode (speak instead of type)")
	helpLine(colWidth, "/manual", "Return to keyboard input (voice safety valve)")
	helpLine(colWidth, "/wake <on|off>", "Hands-free: listen for the wake phrase without touching the keyboard")
	helpLine(colWidth, "/say <text>", "Speak text through the TTS chain")
	helpLine(colWidth, "/listen [sec]", "Record and transcribe one clip (max 60s)")
	helpLine(colWidth, "/mictest", "3s self-test: is the mic actually being heard?")
	helpLine(colWidth, "/tts <on|off>", "Toggle automatic spoken responses")
	helpLine(colWidth, "/eyes <on|off>", "Toggle opt-in camera vision (memory-only)")
	helpSection("DANGER ZONE")
	helpLine(colWidth, "/purge", "Wipe ALL Helix data (keys, DBs, caches) for a fresh start")
	helpSection("TIPS & ACCELERATION")
	fmt.Println("  " + shell.Fg(shell.HexSubtle, "│"))
	fmt.Println("  " + shell.Fg(shell.HexSubtle, "│") + " " +
		shell.Fg(shell.HexTertiary, "⚡ NVD API KEY") + " " +
		shell.Fg(shell.HexSubtle, "—") + " " +
		shell.Fg(shell.HexText, "Accelerate knowledge sync from 40min → 10min"))
	fmt.Println("  " + shell.Fg(shell.HexSubtle, "│") + "   " +
		shell.Fg(shell.HexSubtle, "Get free key: ") +
		shell.Fg(shell.HexSecondary, "https://nvd.nist.gov/developers/request-an-api-key"))
	fmt.Println("  " + shell.Fg(shell.HexSubtle, "│") + "   " +
		shell.Fg(shell.HexSubtle, "Set environment: ") +
		shell.Fg(shell.HexSecondary, "export NVD_API_KEY=\"your-key-here\""))
	fmt.Println("  " + shell.Fg(shell.HexSubtle, "│"))
	fmt.Println("  " + shell.Fg(shell.HexSubtle, "│") + " " +
		shell.Fg(shell.HexTertiary, "💡 NATURAL LANGUAGE") + " " +
		shell.Fg(shell.HexSubtle, "—") + " " +
		shell.Fg(shell.HexText, "Just type plain English"))
	fmt.Println("  " + shell.Fg(shell.HexSubtle, "│") + "   " +
		shell.Fg(shell.HexSubtle, "Example: ") +
		shell.Fg(shell.HexSecondary, "\"find large files and delete them\""))
	fmt.Println("  " + shell.Fg(shell.HexSubtle, "│"))
	helpSection("PROMPT ANATOMY")
	fmt.Println("  " + shell.Fg(shell.HexSubtle, "│") + " " +
		shell.Seg(shell.HexPrimary, shell.HexVoid, " HELIX ") + shell.Fg(shell.HexSubtle, " identity  ") +
		shell.Seg(shell.HexSecondary, shell.HexText, " ~/path ") + shell.Fg(shell.HexSubtle, " context  ") +
		shell.Seg(shell.HexGrid, shell.HexTertiary, " main ") + shell.Fg(shell.HexSubtle, " telemetry  ") +
		shell.Fg(shell.HexRectifier, "❯") + shell.Fg(shell.HexSubtle, " interactive"))
	fmt.Println(rule)
	fmt.Println()
}

func helpSection(title string) {
	fmt.Println()
	fmt.Println("  " + shell.Fg(shell.HexSecondary, "▸ "+strings.ToUpper(title)))
}

func helpLine(colWidth int, cmd, desc string) {
	pad := colWidth - len(cmd)
	if pad < 2 {
		pad = 2
	}
	fmt.Printf("  %s %s%s%s\n",
		shell.Fg(shell.HexSubtle, "│"),
		shell.Fg(shell.HexTertiary, cmd),
		strings.Repeat(" ", pad),
		shell.Fg(shell.HexText, desc),
	)
}

// -------------------------------------------------------
// Unknown slash command
// -------------------------------------------------------

func handleUnknownSlashCommand(cmd string) {
	audio.PlayError()
	fmt.Println()
	fmt.Println("  " + shell.Fg(shell.HexRectifier, "⚠ UNRECOGNIZED SIGNAL") +
		" " + shell.Fg(shell.HexSubtle, "::") +
		" " + shell.Fg(shell.HexText, cmd))
	fmt.Println("  " + shell.Fg(shell.HexSubtle, "│") +
		" " + shell.Fg(shell.HexText, "That command does not exist in the Helix grid."))
	fmt.Println("  " + shell.Fg(shell.HexSubtle, "│") +
		" " + shell.Fg(shell.HexTertiary, "Execute /help") +
		" " + shell.Fg(shell.HexText, "for the SOS protocol and full command menu."))
	fmt.Println()
}

// -------------------------------------------------------
// STATUS / SANDBOX / CD / GIT
// -------------------------------------------------------

func handleStatus() {
	color.Cyan("⚡ HELIX BACKGROUND STATUS")

	// RAG System
	if ragSystem != nil {
		stats := ragSystem.GetSystemStats()
		statusText := ragSystem.GetInitializationStatus()
		color.Cyan("  RAG System: %s", statusText)
		if indexedPages, ok := stats["indexed_pages"]; ok {
			color.Cyan("    └─ MAN Pages Indexed: %v", indexedPages)
		}
		if totalDocs, ok := stats["total_documents"]; ok {
			color.Cyan("    └─ Vector Documents: %v", totalDocs)
		}
	} else {
		color.Yellow("  RAG System: Not initialized")
	}

	// Knowledge Base
	if ragSystem != nil && ragSystem.GetDB() != nil {
		stats := ragSystem.GetSystemStats()
		color.Cyan("  Knowledge Base:")
		if cves, ok := stats["db_cves"]; ok {
			color.Cyan("    └─ CVEs: %v", cves)
		}
		if exploits, ok := stats["db_exploits"]; ok {
			color.Cyan("    └─ Exploits: %v", exploits)
		}
		if kev, ok := stats["db_kev"]; ok {
			color.Cyan("    └─ KEV (CISA): %v", kev)
		}
		if mitre, ok := stats["db_mitre"]; ok {
			color.Cyan("    └─ MITRE Techniques: %v", mitre)
		}
		if last := rag.KnowledgeLastUpdate(ragSystem.GetDB()); last != "" {
			color.Cyan("    └─ Last Update: %s", last)
		} else {
			color.Yellow("    └─ Last Update: never")
		}
	} else {
		color.Yellow("  Knowledge Base: Not initialized")
	}

	// AI Provider
	color.Cyan("  AI Provider: %s (%s)", ai.ActiveProviderName(), ai.ActiveModel())

	// Audio Engine
	if audio.IsEnabled() {
		color.Green("  Audio Engine: Active")
	} else {
		color.Yellow("  Audio Engine: Inactive (Use /audio on)")
	}

	// Stealth Mode
	if agentCore != nil && agentCore.IsStealthEnabled() {
		color.Magenta("  Stealth Mode: ENABLED (History suppression active)")
	} else {
		color.Yellow("  Stealth Mode: DISABLED")
	}

	// Typewrite-all
	if cfg.UserPrefs.TypewriteAll {
		color.Green("  Typewrite-All: ENABLED (All output animated)")
	} else {
		color.Yellow("  Typewrite-All: DISABLED (AI output only)")
	}

	// Debug Mode
	if utils.IsDebugMode() {
		color.Magenta("  Debug Mode: ENABLED")
	} else {
		color.Yellow("  Debug Mode: DISABLED")
	}

	// Sandbox
	if sandbox != nil {
		color.Cyan("  Sandbox: %s", sandbox.ModeString())
		if sandbox.GetMode() == commands.SandboxStrict {
			color.Cyan("    └─ Confinement: %s", confinement.BackendName())
		}
	}
}

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
// RAG STATUS / REINDEX / RESET / REBUILD
// -------------------------------------------------------

func handleRAGStatus() {
	color.Cyan("RAG System Status:")

	if ragSystem == nil {
		color.Red("RAG system not initialized")
		return
	}

	// IMPORTANT: /rag-status should not query the knowledge DB.
	// That avoids hangs while knowledge bootstrap/update is running.
	stats := ragSystem.GetRAGStats()
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
				return
			}
		}

		color.Yellow("RAG not initialized yet (no MAN pages indexed).")
	}
}

func handleRAGReindex() {
	if ragSystem == nil {
		color.Red("RAG system not initialized")
		return
	}
	color.Cyan("Forcing full RAG reindex…")
	ctx, cancel := context.WithCancel(context.Background())
	unreg := utils.RegisterOperation(cancel)
	err := ragSystem.RebuildWithProgressCtx(ctx)
	unreg()
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			color.Yellow("RAG reindex cancelled.")
			return
		}
		color.Red("RAG reindex failed: %v", err)
		return
	}
	color.Green("RAG reindex completed successfully")
}

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
}

func handleRAGRebuild() {
	if ragSystem == nil {
		color.Red("RAG system not initialized")
		color.Yellow("Start Helix normally first so the RAG system is created, then run /rag-rebuild.")
		return
	}
	color.Yellow("Full RAG REBUILD will delete all cached embeddings and rebuild every index.")
	if !commands.AskForConfirmation("Proceed with full rebuild now?") {
		color.Yellow("RAG rebuild cancelled by user")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	unreg := utils.RegisterOperation(cancel)
	err := ragSystem.RebuildWithProgressCtx(ctx)
	unreg()
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			color.Yellow("RAG rebuild cancelled.")
			return
		}
		color.Red("RAG rebuild failed: %v", err)
		return
	}
	color.Green("RAG rebuild completed successfully and is now ACTIVE.")
}

// -------------------------------------------------------
// TOGGLES / SMOKE TESTS
// -------------------------------------------------------

func toggleDryRun() {
	execConfig.DryRun = !execConfig.DryRun
	if execConfig.DryRun {
		color.Yellow("Dry-run mode ENABLED — commands will not execute")
	} else {
		color.Green("Dry-run mode DISABLED — commands will run normally")
	}
}

func checkOnlineStatus() {
	color.Blue("Checking internet connectivity…")
	if utils.IsOnline(3 * time.Second) {
		color.Green("Online — real-time capabilities available")
	} else {
		color.Yellow("Offline — using local AI only")
	}
}

func testBasicAI() {
	color.Cyan("Testing basic AI model responses…")
	think := ux.NewThinker("HELIX :: SMOKE TEST")
	think.Start()
	resp1, err := ai.RunModel("Say 'hello world'")
	think.Stop()
	if err != nil {
		color.Red("Test 1 failed: %v", err)
	} else {
		color.Green("Test 1: %s", strings.TrimSpace(resp1))
	}
}

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
	}
}

// -------------------------------------------------------
// /memory — BlackBox Phase 4B conversation memory
// -------------------------------------------------------

// handleAgenticCommand toggles the iterative agentic harness: /agentic [on|off|status].
func handleAgenticCommand(input string) {
	if agentCore == nil {
		color.Red("Agent is not available in this session.")
		return
	}
	arg := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(input, "/agentic")))
	switch arg {
	case "on", "enable":
		agentCore.Agentic = true
		cfg.UserPrefs.AgenticMode = true
		_ = cfg.SavePreferences()
		color.Green("Agentic harness ON — Helix will observe step results and self-correct across up to %d follow-ups.", agenticStepBudget())
		color.Cyan("Every follow-up plan still passes the full safety pipeline. Use /agentic off to return to single-shot planning.")
	case "off", "disable":
		agentCore.Agentic = false
		cfg.UserPrefs.AgenticMode = false
		_ = cfg.SavePreferences()
		color.Yellow("Agentic harness OFF — single-shot planning restored.")
	default:
		state := "OFF"
		if agentCore.Agentic {
			state = "ON"
		}
		color.Cyan("Agentic harness: %s (step budget %d). Toggle with /agentic on|off.", state, agenticStepBudget())
	}
}

func agenticStepBudget() int {
	if agentCore != nil && agentCore.MaxAgenticSteps > 0 {
		return agentCore.MaxAgenticSteps
	}
	return 4
}

func handleMemoryCommand(input string) {
	args := strings.Fields(input)
	action := "show"
	if len(args) >= 2 {
		action = strings.ToLower(args[1])
	}

	if agentCore == nil || agentCore.Session == nil {
		color.Red("Session memory is not available in this session.")
		return
	}

	switch action {
	case "show", "list", "ls", "":
		turns := agentCore.Session.Recent(agentCore.Session.Len())
		if len(turns) == 0 {
			color.Cyan("Conversation memory is empty.")
			return
		}
		color.Cyan("Conversation memory (%d turns, oldest first):", len(turns))
		for _, t := range turns {
			channel := t.Channel
			if channel == "" {
				channel = "text"
			}
			fmt.Printf("  [%s] (%s) %s\n", t.Timestamp.Format("15:04:05"), channel, truncStr(t.UserText, 80))
			if t.Reply != "" {
				fmt.Printf("         ↳ %s\n", truncStr(t.Reply, 80))
			}
		}
	case "clear", "wipe", "reset":
		if !commands.AskForConfirmation("Clear all conversation memory?") {
			color.Yellow("Memory clear cancelled.")
			return
		}
		if err := agentCore.Session.Clear(); err != nil {
			color.Red("Memory clear failed: %v", err)
			return
		}
		color.Green("Conversation memory cleared.")
	default:
		color.Yellow("Usage: /memory <show|clear>")
	}
}

// -------------------------------------------------------
// RECON
// -------------------------------------------------------
func handleQuickScan(args []string) {
	if len(args) < 2 {
		color.Cyan("Usage: /scan authorize <target> --reason \"<written scope>\"")
		return
	}
	if agentCore == nil {
		color.Red("Agent not initialized")
		return
	}
	switch strings.ToLower(args[1]) {
	case "authorize":
		if len(args) < 3 {
			color.Red("Usage: /scan authorize <target> --reason \"<written scope>\"")
			return
		}
		target := args[2]
		reason := "manual authorization"
		for i, arg := range args {
			if strings.EqualFold(arg, "--reason") && i+1 < len(args) {
				reason = strings.Join(args[i+1:], " ")
				break
			}
		}
		agentCore.AuthorizeRecon(target, reason)
	case "status":
		targets := agentCore.ListAuthorizedReconTargets()
		if len(targets) == 0 {
			color.Yellow("No authorized recon targets.")
			return
		}
		color.Cyan("Authorized recon targets:")
		for target, reason := range targets {
			color.Cyan("  • %s — %s", target, reason)
		}
	default:
		target := args[1]
		if !agentCore.IsReconTargetAuthorized(target) {
			color.Red("Target %q is not authorized for reconnaissance.", target)
			return
		}

		toolName := "nmap"

		// Phase 15 Fix: Show reasoning progress bar during the scan
		think := ux.NewThinker("HELIX :: SCANNING")
		think.Start()
		result, err := agentCore.RunReconTool(toolName, "-sV", target)
		think.Stop()

		if err != nil {
			color.Red("Recon failed: %v", err)
			return
		}

		// Auto-install missing recon tools
		if result.Error != nil && strings.Contains(strings.ToLower(result.Error.Error()), "not found") {
			color.Yellow("Recon tool %q is not installed.", toolName)
			if commands.AskForConfirmation(fmt.Sprintf("Install %s now using system package manager?", toolName)) {
				if installErr := agentCore.InstallTool(toolName); installErr != nil {
					color.Red("Installation failed: %v", installErr)
					return
				}
				color.Green("%s installed successfully. Retrying scan...", toolName)

				think2 := ux.NewThinker("HELIX :: SCANNING")
				think2.Start()
				result, err = agentCore.RunReconTool(toolName, "-sV", target)
				think2.Stop()

				if err != nil {
					color.Red("Recon retry failed: %v", err)
					return
				}
			} else {
				color.Yellow("Scan skipped.")
				return
			}
		}

		if result.Error != nil {
			color.Red("Recon tool issue: %v", result.Error)
		}
		color.Green("Recon completed in %v", result.Elapsed)
		if result.Raw != "" {
			fmt.Println(result.Raw)
		} else if len(result.Parsed) > 0 {
			summary, _ := json.MarshalIndent(result.Parsed, "", "  ")
			color.Cyan("Parsed Results:")
			fmt.Println(string(summary))
		}
	}
}

// -------------------------------------------------------
// /explain — defensive debrief
// -------------------------------------------------------
func handleExplainCommand(input string) {
	args := strings.TrimSpace(strings.TrimPrefix(input, "/explain"))
	if args == "" {
		color.Red("Usage: /explain <command or technique description>")
		return
	}
	mitreContext := ""
	if ragSystem != nil && ragSystem.IsInitialized() {
		snippets, err := ragSystem.RetrieveMitreContext(args, 3)
		if err == nil && len(snippets) > 0 {
			mitreContext = "MITRE ATT&CK knowledge:\n" + strings.Join(snippets, "\n")
		}
	}
	prompt := fmt.Sprintf(`You are Helix's defensive explainability module.
Given the user's command or technique description, produce a structured defensive debrief with these sections:
1. Technique(s): Which MITRE ATT&CK techniques are relevant? List IDs and names.
2. Expected Detections: What logs, telemetry, or security controls may detect this activity?
3. Mitigation Controls: What defensive mitigations, hardening steps, or patches reduce risk?
4. Safer Operational Alternatives: Which supported, lower-risk commands or workflows achieve the same operational goal?
MITRE Context: %s
User Request: %s
FORMAT RULES: Use ONLY plain text. NO markdown. Separate sections with blank lines.`, mitreContext, args)

	explainConfig := ai.ModelConfig{Temperature: 0.7, TopP: 0.9, TopK: 40, MaxTokens: 2048}
	think := ux.NewThinker("HELIX :: REASONING")
	think.Start()
	resp, err := ai.RunModelWithConfig(prompt, explainConfig)
	think.Stop()
	if err != nil {
		color.Red("AI call failed: %v", err)
		return
	}
	cleaned := cleanDebrief(strings.TrimSpace(resp))
	if cleaned == "" {
		color.Yellow("The AI model returned an empty explanation. Try rephrasing the request or checking /provider-status.")
		return
	}
	if agentCore != nil {
		agentCore.GetUX().PrintAIMessage(cleaned, agentCore.GetTypingEffect())
	}
}

func listAvailableModels() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	think := ux.NewThinker("HELIX :: REASONING")
	think.Start()
	models, err := ai.ListProviderModels(ctx)
	think.Stop()
	if err != nil {
		color.Red("Could not list models: %v", err)
		return
	}
	color.Cyan("Available models:")
	for i, model := range models {
		if i >= 50 {
			color.Cyan("... and %d more", len(models)-50)
			break
		}
		color.Cyan("  %s", model.ID)
	}
}

func cleanDebrief(text string) string {
	text = strings.ReplaceAll(text, "**", "")
	headers := []string{
		"1. Technique(s):",
		"2. Expected Detections:",
		"3. Mitigation Controls:",
		"4. Safer Operational Alternatives:",
	}
	for _, h := range headers {
		coloured := color.New(color.FgCyan, color.Bold).Sprint(h)
		text = strings.Replace(text, h, coloured, 1)
	}
	return text
}

// -------------------------------------------------------
// KNOWLEDGE BASE
// -------------------------------------------------------

func handleKnowledgeUpdate() {
	if ragSystem == nil || ragSystem.GetDB() == nil {
		color.Red("Knowledge database not available.")
		return
	}
	prog := rag.NewProgress()
	prog.SetStage("STARTING KNOWLEDGE UPDATE")
	prog.Start()
	rag.OnUpdateStage = func(stage string, current, total int) {
		if total > 0 {
			prog.Set(stage, current, total)
		} else {
			prog.SetStage(stage)
		}
	}
	rag.OnInteractivePrompt = func(active bool) {
		if active {
			prog.Stop()
		} else {
			prog.Start()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	unreg := utils.RegisterOperation(cancel)
	err := ragSystem.UpdateKnowledgeCtx(ctx)
	unreg()
	cancel()
	rag.OnUpdateStage = nil
	rag.OnInteractivePrompt = nil
	prog.Stop()
	if err != nil {
		if ctx.Err() != nil {
			color.Yellow("Knowledge update cancelled.")
			return
		}
		if errors.Is(err, rag.ErrOffline) {
			color.Yellow("You appear to be OFFLINE — knowledge update requires internet connectivity.")
			return
		}
		color.Red("Update failed: %v", err)
		return
	}
	color.Green("Knowledge base updated successfully.")
}

func handleKnowledgeStats() {
	if ragSystem == nil || ragSystem.GetDB() == nil {
		color.Red("Knowledge database not available.")
		return
	}
	stats := ragSystem.GetSystemStats()
	color.Cyan("Knowledge Base Statistics:")
	if cves, ok := stats["db_cves"]; ok {
		color.Cyan("  CVEs: %v", cves)
	}
	if exploits, ok := stats["db_exploits"]; ok {
		color.Cyan("  Exploits: %v", exploits)
	}
	if kev, ok := stats["db_kev"]; ok {
		color.Cyan("  KEV (CISA): %v", kev)
	}
	if mitre, ok := stats["db_mitre"]; ok {
		color.Cyan("  MITRE Techniques: %v", mitre)
	}
	if last := rag.KnowledgeLastUpdate(ragSystem.GetDB()); last != "" {
		color.Cyan("  Last knowledge update: %s", last)
	} else {
		color.Yellow("  Last knowledge update: never (auto-bootstraps in background when online)")
	}
}

func handleVulnCommand(input string) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return
	}
	query := strings.TrimSpace(strings.TrimPrefix(input, fields[0]))
	query = strings.Trim(query, `"'`)
	if query == "" {
		color.Red("Usage: /vuln <CVE-ID|EDB-ID|MITRE-T-ID|search query>")
		return
	}
	if ragSystem == nil || ragSystem.GetDB() == nil {
		color.Red("Knowledge database not available.")
		return
	}
	db := ragSystem.GetDB()

	if strings.HasPrefix(strings.ToUpper(query), "CVE-") {
		exact, err := rag.LookupVulnByID(db, query)
		if err == nil && len(exact) > 0 {
			displayVulnEntries(exact)
			return
		}

		color.Yellow("⚠ Local CVE database does not contain %s (rolling 119-day window).", strings.ToUpper(query))
		color.Yellow("  Attempting on-demand fetch from NVD API...")

		// Phase 15 Fix: Show reasoning progress bar during the API fetch
		think := ux.NewThinker("HELIX :: FETCHING NVD")
		think.Start()
		fetchErr := fetchAndInsertCVE(db, strings.ToUpper(query))
		think.Stop()

		if fetchErr == nil {
			exact, err = rag.LookupVulnByID(db, query)
			if err == nil && len(exact) > 0 {
				displayVulnEntries(exact)
				return
			}
		} else {
			color.Yellow("  On-demand fetch failed: %v", fetchErr)
		}

		color.Yellow("  Run /knowledge-update to sync full NVD data if needed.")
	} else {
		exact, err := rag.LookupVulnByID(db, query)
		if err == nil && len(exact) > 0 {
			displayVulnEntries(exact)
			return
		}
	}

	entries, err := rag.SearchVulns(db, query, 5)
	if err != nil {
		color.Red("Vulnerability search failed: %v", err)
		return
	}
	if len(entries) == 0 {
		color.Yellow("No matching vulnerability intelligence found.")
		return
	}
	displayVulnEntries(entries)
}

func fetchAndInsertCVE(db *sql.DB, cveID string) error {
	if !utils.IsOnline(3 * time.Second) {
		return fmt.Errorf("offline")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	url := fmt.Sprintf("https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=%s", cveID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Helix/1.0 (defensive threat intelligence)")

	if apiKey := os.Getenv("NVD_API_KEY"); apiKey != "" {
		req.Header.Set("apiKey", apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("NVD API returned %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))

	var nvdResp struct {
		Vulnerabilities []struct {
			CVE struct {
				ID           string `json:"id"`
				Published    string `json:"published"`
				LastModified string `json:"lastModified"`
				Descriptions []struct {
					Lang  string `json:"lang"`
					Value string `json:"value"`
				} `json:"descriptions"`
				Metrics struct {
					CVSSMetricV31 []struct {
						CVSSData struct {
							BaseScore float64 `json:"baseScore"`
						} `json:"cvssData"`
					} `json:"cvssMetricV31"`
				} `json:"metrics"`
			} `json:"cve"`
		} `json:"vulnerabilities"`
	}

	if err := json.Unmarshal(bodyBytes, &nvdResp); err != nil {
		return err
	}

	if len(nvdResp.Vulnerabilities) == 0 {
		return fmt.Errorf("CVE not found in NVD")
	}

	vuln := nvdResp.Vulnerabilities[0].CVE
	desc := ""
	if len(vuln.Descriptions) > 0 {
		for _, d := range vuln.Descriptions {
			if d.Lang == "en" {
				desc = d.Value
				break
			}
		}
		if desc == "" {
			desc = vuln.Descriptions[0].Value
		}
	}

	cvss := 0.0
	if len(vuln.Metrics.CVSSMetricV31) > 0 {
		cvss = vuln.Metrics.CVSSMetricV31[0].CVSSData.BaseScore
	}

	raw, _ := json.Marshal(vuln)
	_, err = db.Exec(`INSERT OR REPLACE INTO cve(id, description, cvss_score, published_date, last_modified_date, raw_json) VALUES(?,?,?,?,?,?)`,
		vuln.ID, desc, cvss, vuln.Published, vuln.LastModified, string(raw))

	return err
}

func displayVulnEntries(entries []rag.VulnIntel) {
	bold := color.New(color.FgCyan, color.Bold).SprintFunc()
	color.Cyan("=== Vulnerability Intelligence ===")
	for i, e := range entries {
		if i > 0 {
			fmt.Println()
		}
		color.Cyan("%s %s", bold("ID:"), e.ID)
		color.Cyan("%s %s", bold("Source:"), e.SourceType)
		color.Cyan("%s %s", bold("Title:"), e.Title)
		if e.Description != "" {
			color.Cyan("%s %s", bold("Description:"), e.Description)
		}
		if e.CVSS > 0 {
			color.Cyan("%s %.1f", bold("CVSS:"), e.CVSS)
		}
		color.Cyan("%s %v", bold("CISA KEV:"), e.KEV)
		if e.KEVAction != "" {
			color.Cyan("%s %s", bold("KEV Action:"), e.KEVAction)
		}
		if e.Detection != "" {
			color.Cyan("%s %s", bold("Detection:"), e.Detection)
		}
		if e.PatchGuidance != "" {
			color.Cyan("%s %s", bold("Patch Guidance:"), e.PatchGuidance)
		}
	}
	fmt.Println()
	color.Yellow("Defensive use only: prioritize patching and detection.")
}

func handleKnowledgeReindex() {
	if ragSystem == nil || ragSystem.GetDB() == nil {
		color.Red("Knowledge database not available.")
		return
	}
	db := ragSystem.GetDB()
	prog := rag.NewProgress()
	prog.SetStage("REBUILDING FTS INDEX")
	prog.Start()
	err := rag.ReindexKnowledgeFTS(db, func(current, total int) {
		if total > 0 {
			prog.Set("REBUILDING FTS INDEX", current, total)
		}
	})
	prog.Stop()
	if err != nil {
		color.Red("FTS reindex failed: %v", err)
		return
	}
	count, cerr := rag.FTSCount(db)
	if cerr != nil {
		color.Yellow("FTS reindex completed but count check failed: %v", cerr)
		return
	}
	color.Green("FTS index rebuilt successfully. Rows indexed: %d", count)
}

// -------------------------------------------------------
// DOCTOR / PROVIDERS / MODELS
// -------------------------------------------------------

func handleDoctor() {
	color.Cyan("=== Helix Doctor ===")
	home, err := os.UserHomeDir()
	if err != nil {
		color.Red("Home directory error: %v", err)
	} else {
		helixDir := filepath.Join(home, ".helix")
		if fi, err := os.Stat(helixDir); err == nil && fi.IsDir() {
			color.Green("Config directory OK: %s", helixDir)
		} else {
			color.Red("Config directory missing: %s", helixDir)
		}
	}
	if ragSystem != nil && ragSystem.GetDB() != nil {
		db := ragSystem.GetDB()

		pingCtx, pingCancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := db.PingContext(pingCtx)
		pingCancel()

		if err != nil {
			color.Red("Database ping failed: %v", err)
		} else {
			color.Green("Database connection OK")
		}

		color.Cyan("Knowledge schema version: v%d", rag.SchemaVersion(db))
	}
	color.Cyan("Provider: %s", ai.GetProvider())
	color.Cyan("Local model loaded: %v", ai.ModelIsLoaded())
	if utils.IsOnline(3 * time.Second) {
		color.Green("Network: online")
	} else {
		color.Yellow("Network: offline")
	}
	color.Cyan("OS: %s", env.OSName)
	color.Cyan("Shell: %s", env.Shell)
	if sandbox != nil {
		color.Cyan("Sandbox: %s", sandbox.ModeString())
	}
	color.Cyan("Confinement backend: %s", confinement.BackendName())

	// BlackBox P10.3: the edge-appliance picture.
	printEdgeSection()

	// BlackBox Phase 4: Living AI daemon presence.
	if daemonRunning() {
		color.Green("Daemon: running (Living AI)")
	} else {
		color.Yellow("Daemon: not running — start it with `helix daemon`")
	}

	if summaries := diagnostics.ListReports(); len(summaries) > 0 {
		color.Yellow("Pending crash reports (%d):", len(summaries))
		for _, s := range summaries {
			color.Yellow("  • %s — %s", s.Time, s.Reason)
			color.Yellow("    %s", s.Path)
		}
		color.Yellow("Reports are local-only; run /purge to delete them.")
	} else {
		color.Green("Crash diagnostics: no pending reports (telemetry-free)")
	}
}

// printEdgeSection renders the "edge appliance" diagnostics block (P10.3).
//
// It exists because the two Linux edge gotchas fail SILENTLY: a CGO-free build
// is structurally mute however the TTS provider is configured, and kernel
// confinement degrades to none on an old kernel without stopping anything. On a
// headless board those are invisible until something important does not happen,
// so /doctor states them outright, with the fix attached.
func printEdgeSection() {
	rep := edge.Collect()

	color.Cyan("--- Edge appliance ---")
	color.Cyan("Platform: %s/%s", rep.OS, rep.Arch)
	if rep.Board != "" {
		color.Cyan("Board: %s", rep.Board)
	}

	// Build flavor — the audio_cgo gotcha (docs/edge_deployment.md §3.1).
	if rep.SpeechSupported {
		color.Green("Audio output: %s", rep.AudioBackend)
	} else {
		color.Yellow("Audio output: %s", rep.AudioBackend)
		color.Yellow("  → Helix cannot speak on this build. To hear TTS: " +
			"sudo apt install -y libasound2-dev && CGO_ENABLED=1 go build -tags audio_cgo ./cmd/helix")
	}

	// Confinement actually in force, not what the config hopes for.
	if rep.Note == "" {
		color.Green("Confinement in force: %s", rep.Confinement)
	} else {
		color.Yellow("Confinement in force: %s", rep.Confinement)
		color.Yellow("  → %s", rep.Note)
	}

	// Microphone capture (CGO-free; sox/ffmpeg shell-out per ADR-003).
	if rec, err := speech.DetectRecorder(); err == nil {
		color.Green("Recorder: %s", rec)
	} else {
		color.Yellow("Recorder: none found — install sox (`sudo apt install -y sox`) for voice input")
	}

	printEdgeSidecars()
	printEdgeThermals(rep)
}

// printEdgeSidecars probes the local services this device is configured to
// depend on. Only LOCAL providers are listed: a cloud endpoint being reachable
// is a network fact, already covered by the Network line above.
func printEdgeSidecars() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	report := speech.Status(ctx)
	for _, group := range [][]speech.ProviderStatusRow{report.STTStatus, report.TTSStatus} {
		for _, row := range group {
			if !row.Local {
				continue
			}
			if row.Healthy {
				color.Green("Sidecar %s: reachable", row.Name)
			} else {
				color.Yellow("Sidecar %s: unreachable (%s)", row.Name, row.HealthDetail)
			}
		}
	}

	// The offline brain (P11.2/P11.3): configured but unreachable or unpulled
	// is the failure that only shows up during an outage, when it is too late.
	cfg, err := config.DefaultConfig()
	if err != nil {
		return
	}
	fb := cfg.LLM.Fallback
	if !fb.FallbackEnabled() {
		color.Cyan("Offline LLM fallback: disabled")
		return
	}
	provider := fb.Provider
	if provider == "" {
		provider = config.LLMDefaults().Fallback.Provider
	}

	if provider == "ollama" {
		client := ollama.NewClient()
		if herr := client.Health(ctx); herr != nil {
			color.Yellow("Offline LLM (ollama): unreachable — %v", herr)
			return
		}
		model := fb.Model
		if model == "" {
			model = ai.ActiveModel()
		}
		if model == "" {
			color.Yellow("Offline LLM (ollama): running, but no fallback model configured (llm.fallback.model)")
			return
		}
		if ollamaHasModel(ctx, client, model) {
			color.Green("Offline LLM (ollama): ready — %s", model)
		} else {
			color.Yellow("Offline LLM (ollama): model %q NOT pulled — run `ollama pull %s`", model, model)
		}
		return
	}

	// llama.cpp is a user-managed sidecar with no pull API; reachability is
	// the only thing Helix can honestly assert.
	if p, gerr := ai.GetProviderByName(provider); gerr == nil {
		if herr := p.HealthCheck(ctx); herr == nil {
			color.Green("Offline LLM (%s): reachable", provider)
		} else {
			color.Yellow("Offline LLM (%s): unreachable — %v", provider, herr)
		}
	}
}

// ollamaHasModel reports whether a model tag is installed, tolerating the bare
// name vs "name:tag" difference so an installed model is not reported missing.
func ollamaHasModel(ctx context.Context, client *ollama.Client, model string) bool {
	installed, err := client.ListModels(ctx)
	if err != nil {
		return false
	}
	for _, m := range installed {
		if m.ID == model || strings.HasPrefix(m.ID, model+":") {
			return true
		}
	}
	return false
}

// printEdgeThermals reports temperature and throttling. Sustained throttling is
// the usual explanation for an appliance that "got slow" after working fine.
func printEdgeThermals(rep edge.Report) {
	if rep.ThermalC <= 0 {
		if rep.ThermalErr != "" {
			color.Cyan("Thermals: %s", rep.ThermalErr)
		}
		return
	}
	line := fmt.Sprintf("Thermals: %.1f°C (%s)", rep.ThermalC, edge.ThermalVerdict(rep.ThermalC))
	if rep.ThermalC >= 80 {
		color.Yellow(line)
	} else {
		color.Green(line)
	}
	if rep.Throttled {
		color.Yellow("  → firmware reports a throttle event; sustained load is being capped")
	}
}

func handleProviderStatus() {
	color.Cyan("=== Provider Status ===")
	lines := ai.ProviderStatus()
	for _, line := range lines {
		color.Cyan(line)
	}
	color.Cyan("Active Provider: %s", ai.ActiveProviderName())
	color.Cyan("Active Model: %s", ai.ActiveModel())
	// BlackBox P11.2: when the breaker is engaged the two lines above already
	// name the LOCAL model, which would otherwise look like the user's own
	// choice. Say why.
	if ai.LocalFallbackActive() {
		color.Yellow("Offline fallback: %s", ai.FailoverStatus())
	} else {
		color.Cyan("Offline fallback: %s", ai.FailoverStatus())
	}
	// BlackBox P8.7: which mechanism carries the plan. Worth surfacing because
	// it explains a real behavior difference — native tool calling removes the
	// JSON-repair retries the prompt path needs.
	color.Cyan("Planner protocol: %s", ai.PlannerTransport())
}

func handleProviderCommand(args []string) {
	if len(args) == 1 {
		displayProviderStatus()
		return
	}
	switch strings.ToLower(args[1]) {
	case "status":
		displayProviderStatus()
	case "use":
		if len(args) < 3 {
			color.Red("Usage: /provider use <provider>")
			return
		}
		name := strings.ToLower(args[2])
		if err := useProviderInteractive(name); err != nil {
			color.Red("Provider switch failed: %v", err)
			return
		}
		cfg.Provider = name
		cfg.ProviderModel = ai.ActiveModel()
		_ = cfg.SavePreferences()
		color.Green("Active provider: %s", name)
	}
}

func handleModelCommand(args []string) {
	if len(args) == 1 {
		listAvailableModels()
		return
	}
	switch strings.ToLower(args[1]) {
	case "list":
		listAvailableModels()
	case "use":
		if len(args) < 3 {
			color.Red("Usage: /model use <model-id>")
			return
		}
		model := strings.Join(args[2:], " ")
		if err := useModelInteractive(ai.ActiveProviderName(), model); err != nil {
			color.Red("Model switch failed: %v", err)
			return
		}
		cfg.ProviderModel = ai.ActiveModel()
		_ = cfg.SavePreferences()
		color.Green("Active model: %s", ai.ActiveModel())
	}
}

func displayProviderStatus() {
	lines := ai.ProviderStatus()
	color.Cyan("=== Provider Status ===")
	for _, line := range lines {
		color.Cyan(line)
	}
}

func handleAudioCommand(input string) {
	args := strings.Fields(input)
	if len(args) < 2 {
		current := "ON"
		if !audio.IsEnabled() {
			current = "OFF"
		}
		ready := "READY"
		if !audio.IsReady() {
			ready = "NOT READY"
		}
		color.Cyan("Audio is currently: %s (%s)", current, ready)
		color.Yellow("Usage: /audio <on|off>")
		return
	}
	switch strings.ToLower(args[1]) {
	case "on", "enable":
		audio.SetEnabled(true)
		if err := audio.EnsureReady(true); err != nil {
			color.Yellow("Audio enabled, but the sound engine is unavailable: %v", err)
			color.Yellow("Check your system output device and volume.")
			color.Yellow("SSH, Docker, and headless sessions have no local speaker.")
			return
		}
		color.Green("Audio enabled")
		audio.PlayAlert()
		time.Sleep(100 * time.Millisecond)
	case "off", "disable":
		audio.SetEnabled(false)
		color.Yellow("Audio disabled")
	}
}

// -------------------------------------------------------
// CRASH DIAGNOSTICS (/crash)
// -------------------------------------------------------

func handleCrashCommand(parts []string) {
	action := "list"
	if len(parts) >= 2 {
		action = strings.ToLower(parts[1])
	}

	switch action {
	case "list", "ls", "status":
		summaries := diagnostics.ListReports()
		if len(summaries) == 0 {
			color.Green("✔ No pending crash reports. System is clean.")
			return
		}
		color.Yellow("⚠ Pending crash reports (%d):", len(summaries))
		for i, s := range summaries {
			color.Yellow("  [%d] %s — %s", i+1, s.Time, s.Reason)
			color.Yellow("      %s", s.Path)
		}
		fmt.Println()
		color.Cyan("Use '/crash view <number>' to inspect the redacted stack trace.")
		color.Cyan("Use '/crash clear' to safely delete them without wiping your config.")

	case "view", "show", "cat", "read":
		if len(parts) < 3 {
			color.Red("Usage: /crash view <number>")
			return
		}
		summaries := diagnostics.ListReports()
		if len(summaries) == 0 {
			color.Yellow("No crash reports to view.")
			return
		}

		idx, err := strconv.Atoi(parts[2])
		if err != nil || idx < 1 || idx > len(summaries) {
			color.Red("Invalid report number. Use '/crash list' to see valid numbers (1-%d).", len(summaries))
			return
		}

		target := summaries[idx-1]
		data, err := os.ReadFile(target.Path)
		if err != nil {
			color.Red("Failed to read crash report: %v", err)
			return
		}

		fmt.Println()
		color.Cyan("=== Crash Report: %s ===", filepath.Base(target.Path))
		var prettyJSON bytes.Buffer
		if json.Indent(&prettyJSON, data, "", "  ") == nil {
			fmt.Println(prettyJSON.String())
		} else {
			fmt.Println(string(data))
		}
		fmt.Println()
		color.Yellow("Note: All API keys, tokens, and secrets are automatically [REDACTED].")

	case "clear", "clean", "rm", "delete":
		n, err := diagnostics.PurgeReports()
		if err != nil {
			color.Red("Failed to clear crash reports: %v", err)
			return
		}
		if n == 0 {
			color.Yellow("No crash reports to clear.")
		} else {
			color.Green("✔ Cleared %d crash report(s). Your config, keys, and history remain intact.", n)
		}

	default:
		color.Yellow("Usage: /crash <list|view <number>|clear>")
	}
}

// -------------------------------------------------------
// /typewrite-all — Global Typewriter Effect Toggle
// -------------------------------------------------------
func handleTypewriteAllCommand(input string) {
	args := strings.Fields(input)
	if len(args) < 2 {
		current := "OFF"
		if cfg.UserPrefs.TypewriteAll {
			current = "ON"
		}
		color.Cyan("Typewrite-all mode is currently: %s", current)
		color.Yellow("Usage: /typewrite-all <on|off>")
		return
	}

	gui := agentCore.GetUX()

	switch strings.ToLower(args[1]) {
	case "on", "enable":
		cfg.UserPrefs.TypewriteAll = true
		if gui != nil {
			gui.SetTypewriteAll(true)
		}
		color.Green("Typewrite-all ENABLED — all output will use typewriter effect")
	case "off", "disable":
		cfg.UserPrefs.TypewriteAll = false
		if gui != nil {
			gui.SetTypewriteAll(false)
		}
		color.Yellow("Typewrite-all DISABLED — only AI output will use typewriter effect")
	default:
		color.Red("Unknown setting: %s", args[1])
		color.Yellow("Usage: /typewrite-all <on|off>")
		return
	}
	_ = cfg.SavePreferences()
}
