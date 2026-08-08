// cmd/helix/handlers.go
// Purpose: Master slash-command dispatcher and all Helix control handlers.
// Hardening: /knowledge-update, /rag-reindex, and /rag-rebuild now register
// cancellable contexts with the interrupt manager so Ctrl+C aborts the running
// pipeline and returns to a live prompt instead of killing Helix.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/audio"
	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/rag"
	"helix/internal/shell"
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
	case "/purge":
		handlePurgeCommand()
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
		current := "ON"
		if !utils.IsDebugMode() {
			current = "OFF"
		}
		color.Cyan("Debug mode is currently: %s", current)
		color.Yellow("Usage: /debug <on|off>")
		return
	}
	switch strings.ToLower(args[1]) {
	case "on", "enable":
		_ = os.Setenv("HELIX_DEBUG", "1")
		cfg.UserPrefs.DebugMode = true
		color.Green("Debug mode ENABLED")
	case "off", "disable":
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
	const colWidth = 24
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
	helpLine(colWidth, "/vuln <query>", "Defensive vulnerability intel (CVE/EDB/MITRE)")
	helpLine(colWidth, "/scan authorize <ip>", "Authorize recon target (written scope)")
	helpLine(colWidth, "/scan <ip>", "Run nmap/masscan on an authorized target")
	helpLine(colWidth, "/sandbox <mode>", "Directory confinement (off, current, strict)")
	helpLine(colWidth, "/stealth <on|off>", "Private history mode (suppresses shell history)")
	helpLine(colWidth, "/dry-run", "Toggle command execution preview mode")
	helpSection("UTILITIES & GIT")
	helpLine(colWidth, "/git <request>", "Natural language git operations with safety")
	helpLine(colWidth, "/audio <on|off>", "Toggle tonal audio feedback")
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
// Unknown slash command — graceful, aesthetic rejection.
// Guarantees Helix NEVER hangs or misroutes a typo'd /command:
// immediate feedback, no shell execution, no planner call.
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
	color.Cyan("  AI Provider: %s (%s)", ai.ActiveProviderName(), ai.ActiveModel())
	if audio.IsEnabled() {
		color.Green("  Audio Engine: Active")
	} else {
		color.Yellow("  Audio Engine: Inactive (Use /audio on)")
	}
	if agentCore != nil && agentCore.IsStealthEnabled() {
		color.Magenta("  Stealth Mode: ENABLED (History suppression active)")
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
				return
			}
		}
		color.Yellow("RAG not initialized yet (no MAN pages indexed).")
	}
}

// handleRAGReindex performs a FULL foreground rebuild with live progress.
// FIX (interrupt hardening): Ctrl+C cancels the rebuild and returns to the
// prompt instead of killing Helix.
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

// handleRAGRebuild performs a confirmed full rebuild with live progress.
// FIX (interrupt hardening): Ctrl+C cancels the rebuild and returns to the
// prompt instead of killing Helix.
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
		result, err := agentCore.RunReconTool("nmap", "-sV", target)
		if err != nil {
			color.Red("Recon failed: %v", err)
			return
		}
		color.Green("Recon completed in %v", result.Elapsed)
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
	explainConfig := ai.ModelConfig{Temperature: 0.7, TopP: 0.9, TopK: 40, MaxTokens: 512}
	think := ux.NewThinker("HELIX :: REASONING")
	think.Start()
	resp, err := ai.RunModelWithConfig(prompt, explainConfig)
	think.Stop()
	if err != nil {
		color.Red("AI call failed: %v", err)
		return
	}
	cleaned := cleanDebrief(strings.TrimSpace(resp))
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

// handleKnowledgeUpdate runs a staged fetch with a live progress bar that
// politely pauses whenever the pipeline needs to ask the user something
// (e.g. the Ollama embedding bootstrap).
// FIX (interrupt hardening): Ctrl+C cancels the update and returns to the
// prompt instead of killing Helix.
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
	prog.Stop() // always heals the line + cursor, even after cancellation
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
	exact, err := rag.LookupVulnByID(db, query)
	if err == nil && len(exact) > 0 {
		displayVulnEntries(exact)
		return
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

// handleKnowledgeReindex rebuilds the FTS index with live progress.
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
		if err := db.Ping(); err != nil {
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
}

func handleProviderStatus() {
	color.Cyan("=== Provider Status ===")
	lines := ai.ProviderStatus()
	for _, line := range lines {
		color.Cyan(line)
	}
	color.Cyan("Active Provider: %s", ai.ActiveProviderName())
	color.Cyan("Active Model: %s", ai.ActiveModel())
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

// handleAudioCommand processes /audio and verifies the sound engine is
// actually ready, so silence can never be invisible again.
//
// Args:
//   - input: raw user input line.
//
// Returns: none.
// Complexity: O(1), plus possible audio-device initialization time.
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
		// Explicit user action: force a retry of speaker initialization.
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
