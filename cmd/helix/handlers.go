// cmd/helix/handlers.go
// Purpose: Slash-command handlers for Helix.

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/rag"
	"helix/internal/utils"

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
	case "/exploit":
		color.Yellow("/exploit is deprecated. Use /vuln for defensive vulnerability intelligence.")
		handleVulnCommand(strings.Replace(input, "/exploit", "/vuln", 1))
	case "/knowledge-update":
		handleKnowledgeUpdate()
	case "/knowledge-stats":
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
	default:
		return false
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

	if !commands.AskForConfirmation("Proceed with full rebuild now?") {
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
		color.Cyan("Usage:")
		color.Cyan("  /scan authorize <target> --reason \"<written scope>\"")
		color.Cyan("  /scan status")
		color.Cyan("  /scan <target>")
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
		return

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
		return

	default:
		target := args[1]

		if !agentCore.IsReconTargetAuthorized(target) {
			color.Red("Target %q is not authorized for reconnaissance.", target)
			color.Yellow("Run: /scan authorize %s --reason \"<written scope>\"", target)
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
			return
		}

		color.Cyan("Parsed results:")
		for k, v := range result.Parsed {
			color.Cyan("  %s: %v", k, v)
		}
	}
}

// -------------------------------------------------------
// /explain – AI-powered technique analysis
// -------------------------------------------------------

func handleExplainCommand(input string) {
	args := strings.TrimSpace(strings.TrimPrefix(input, "/explain"))
	if args == "" {
		color.Red("Usage: /explain <command or technique description>")
		return
	}

	color.Cyan("Explainable defensive analysis — analysing request...")

	mitreContext := ""
	if ragSystem != nil && ragSystem.IsInitialized() {
		snippets, err := ragSystem.RetrieveMitreContext(args, 3)
		if err == nil && len(snippets) > 0 {
			mitreContext = "MITRE ATT&CK knowledge:\n" + strings.Join(snippets, "\n")
		}
	}

	prompt := fmt.Sprintf(`
You are Helix's defensive explainability module.
Given the user's command or technique description, produce a structured defensive debrief with these sections:

1. Technique(s): Which MITRE ATT&CK techniques are relevant? List IDs and names.
2. Expected Detections: What logs, telemetry, or security controls may detect this activity?
3. Mitigation Controls: What defensive mitigations, hardening steps, or patches reduce risk?

Use the following MITRE ATT&CK context if provided; otherwise, rely on your internal knowledge.

MITRE Context:
%s

User Request: %s

FORMAT RULES (STRICT):
- Use ONLY plain text.
- NO markdown, NO bold/italic, NO backticks, NO hash signs.
- Separate sections with blank lines.
- Use simple hyphens for bullet points.
- Keep section titles exactly as given.

Now output the debrief:`,
		mitreContext,
		args,
	)

	explainConfig := ai.ModelConfig{
		Temperature: 0.7,
		TopP:        0.9,
		TopK:        40,
		MaxTokens:   512,
	}

	resp, err := ai.RunModelWithConfig(prompt, explainConfig)
	if err != nil {
		color.Red("AI call failed: %v", err)
		return
	}

	cleaned := cleanDebrief(strings.TrimSpace(resp))

	if agentCore != nil {
		agentCore.GetUX().PrintAIMessage(cleaned, agentCore.GetTypingEffect())
		agentCore.GetUX().PrintSuccess("Helix :: GRID STATUS :: CLEAR")
	} else {
		color.Cyan(cleaned)
		color.Green("Helix :: GRID STATUS :: CLEAR")
	}
}

// cleanDebrief removes markdown artifacts and colorizes section headers.
func cleanDebrief(text string) string {
	text = strings.ReplaceAll(text, "**", "")

	headers := []string{
		"1. Technique(s):",
		"2. Expected Detections:",
		"3. Mitigation Controls:",
	}

	for _, h := range headers {
		coloured := color.New(color.FgCyan, color.Bold).Sprint(h)
		text = strings.Replace(text, h, coloured, 1)
	}

	return text
}

// -------------------------------------------------------
// /knowledge-update – triggers a live data refresh
// -------------------------------------------------------
func handleKnowledgeUpdate() {
	if ragSystem == nil {
		color.Red("RAG system not initialized")
		return
	}
	color.Cyan("Starting knowledge base update...")
	if err := ragSystem.UpdateKnowledge(); err != nil {
		color.Red("Update failed: %v", err)
		return
	}
	color.Green("Knowledge base updated successfully.")
	if agentCore != nil {
		agentCore.GetUX().PrintSuccess("Helix :: GRID STATUS :: CLEAR")
	} else {
		color.Green("Helix :: GRID STATUS :: CLEAR")
	}
}

// -------------------------------------------------------
// /knowledge-stats – shows database statistics
// -------------------------------------------------------
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
}

// handleVulnCommand provides defensive vulnerability intelligence.
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

	// Exact ID lookup first.
	exact, err := rag.LookupVulnByID(db, query)
	if err == nil && len(exact) > 0 {
		displayVulnEntries(exact)
		return
	}

	// Search fallback.
	entries, err := rag.SearchVulns(db, query, 5)
	if err != nil {
		color.Red("Vulnerability search failed: %v", err)
		return
	}

	if len(entries) == 0 {
		color.Yellow("No matching vulnerability intelligence found.")
		color.Yellow("Try /knowledge-update or /knowledge-reindex.")
		return
	}

	displayVulnEntries(entries)
}

// displayVulnEntries prints defensive vulnerability intelligence.
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

		if e.KEVDueDate != "" {
			color.Cyan("%s %s", bold("KEV Due:"), e.KEVDueDate)
		}

		if e.Platform != "" {
			color.Cyan("%s %s", bold("Platform:"), e.Platform)
		}

		if e.Detection != "" {
			color.Cyan("%s %s", bold("Detection:"), e.Detection)
		}

		if e.PatchGuidance != "" {
			color.Cyan("%s %s", bold("Patch Guidance:"), e.PatchGuidance)
		}

		if len(e.References) > 0 {
			color.Cyan("%s %s", bold("References:"), strings.Join(e.References, ", "))
		}
	}

	fmt.Println()
	color.Yellow("Defensive use only: prioritize patching and detection. No exploit execution is performed.")
}

// handleKnowledgeReindex explicitly rebuilds the FTS knowledge index.
func handleKnowledgeReindex() {
	if ragSystem == nil || ragSystem.GetDB() == nil {
		color.Red("Knowledge database not available.")
		return
	}

	db := ragSystem.GetDB()

	color.Blue("Rebuilding knowledge FTS index...")

	if err := rag.ReindexKnowledgeFTS(db); err != nil {
		color.Red("FTS reindex failed: %v", err)
		return
	}

	count, err := rag.FTSCount(db)
	if err != nil {
		color.Yellow("FTS reindex completed but count check failed: %v", err)
		return
	}

	color.Green("FTS index rebuilt successfully. Rows indexed: %d", count)
}

// handleDoctor performs runtime diagnostics.
func handleDoctor() {
	color.Cyan("=== Helix Doctor ===")

	// Config directory.
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

	// Database and FTS.
	if ragSystem != nil && ragSystem.GetDB() != nil {
		db := ragSystem.GetDB()

		if err := db.Ping(); err != nil {
			color.Red("Database ping failed: %v", err)
		} else {
			color.Green("Database connection OK")
		}

		count, err := rag.FTSCount(db)
		if err != nil {
			color.Red("FTS count failed: %v", err)
		} else {
			color.Cyan("FTS rows: %d", count)
			if count == 0 {
				color.Yellow("FTS index empty. Run /knowledge-update and /knowledge-reindex.")
			}
		}
	} else {
		color.Red("Knowledge database not initialized")
	}

	// Provider/model.
	color.Cyan("Provider: %s", ai.GetProvider())
	color.Cyan("OpenAI key configured: %v", ai.HasOpenAIKey())
	color.Cyan("Local model loaded: %v", ai.ModelIsLoaded())

	if cfg != nil {
		if _, err := os.Stat(cfg.ModelFile); err == nil {
			color.Green("Model file present: %s", cfg.ModelFile)
		} else {
			color.Yellow("Model file missing: %s", cfg.ModelFile)
		}
	}

	// Network.
	if utils.IsOnline(3 * time.Second) {
		color.Green("Network: online")
	} else {
		color.Yellow("Network: offline")
	}

	// Environment.
	color.Cyan("OS: %s", env.OSName)
	color.Cyan("Shell: %s", env.Shell)

	// Sandbox.
	if sandbox != nil {
		color.Cyan("Sandbox: %s", sandbox.ModeString())
	}

	// Recon tools.
	for _, tool := range []string{"nmap", "masscan", "ffuf", "amass"} {
		if _, err := exec.LookPath(tool); err == nil {
			color.Green("Recon tool available: %s", tool)
		} else {
			color.Yellow("Recon tool missing: %s", tool)
		}
	}
}

// handleProviderStatus shows AI provider health.
func handleProviderStatus() {
	color.Cyan("=== Provider Status ===")

	// Use the comprehensive registry status
	lines := ai.ProviderStatus()
	for _, line := range lines {
		color.Cyan(line)
	}

	color.Cyan("Active Provider: %s", ai.ActiveProviderName())
	color.Cyan("Active Model: %s", ai.ActiveModel())

	if utils.IsOnline(3 * time.Second) {
		color.Green("Network: online")
	} else {
		color.Yellow("Network: offline")
	}
}

// -------------------------------------------------------
// /provider
// -------------------------------------------------------
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

		if !ai.HasProvider(name) && name != "custom" {
			color.Red("Unknown provider: %s", name)
			return
		}

		if err := useProviderInteractive(name); err != nil {
			color.Red("Provider switch failed: %v", err)
			return
		}

		if err := ai.UseProvider(name); err != nil {
			color.Red("Provider activation failed: %v", err)
			return
		}

		cfg.Provider = name
		cfg.ProviderModel = ai.ActiveModel()

		if err := cfg.SavePreferences(); err != nil {
			color.Yellow("Could not save provider preferences: %v", err)
		}

		color.Green("Active provider: %s", name)
		color.Green("Active model: %s", ai.ActiveModel())

	default:
		color.Yellow("Usage: /provider | /provider status | /provider use <provider>")
	}
}

// -------------------------------------------------------
// /model
// -------------------------------------------------------
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

		if err := cfg.SavePreferences(); err != nil {
			color.Yellow("Could not save model preference: %v", err)
		}

		color.Green("Active model: %s", ai.ActiveModel())

	default:
		color.Yellow("Usage: /model | /model list | /model use <model-id>")
	}
}

func displayProviderStatus() {
	lines := ai.ProviderStatus()

	color.Cyan("=== Provider Status ===")

	for _, line := range lines {
		color.Cyan(line)
	}
}

func listAvailableModels() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, err := ai.ListProviderModels(ctx)
	if err != nil {
		color.Red("Could not list models: %v", err)
		return
	}

	if len(models) == 0 {
		color.Yellow("No models returned by provider.")
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
