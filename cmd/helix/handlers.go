// cmd/helix/handlers.go
// Purpose: Slash-command handlers for Helix.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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
	case "/exploit":
		handleExploitCommand(input)
	case "/knowledge-update":
		handleKnowledgeUpdate()
	case "/knowledge-stats":
		handleKnowledgeStats()
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

// -------------------------------------------------------
// /explain – AI-powered technique analysis
// -------------------------------------------------------

func handleExplainCommand(input string) {
	args := strings.TrimSpace(strings.TrimPrefix(input, "/explain"))
	if args == "" {
		color.Red("Usage: /explain <command or technique description>")
		return
	}

	color.Cyan("Adversarial Mind – analysing request...")

	// Retrieve MITRE context
	mitreContext := ""
	if ragSystem != nil && ragSystem.IsInitialized() {
		snippets, err := ragSystem.RetrieveMitreContext(args, 3)
		if err == nil && len(snippets) > 0 {
			mitreContext = "MITRE ATT&CK knowledge:\n" + strings.Join(snippets, "\n")
		}
	}

	// UPDATED PROMPT: explicitly ban all markdown
	prompt := fmt.Sprintf(`
You are Helix's Adversarial Mind – an explainable attack planning module.

Given the user's command or technique description, produce a structured debrief with these sections:

1. Technique(s): Which MITRE ATT&CK techniques are involved? (list them with IDs and names)
2. Expected Detections: What security tools or log sources would likely detect this activity?
3. Quieter Alternatives: How could the same objective be achieved with less noise or fewer footprints?

Use the following MITRE ATT&CK context if provided; otherwise, rely on your internal knowledge.

MITRE Context:
%s

User Request: %s

FORMAT RULES (STRICT):
- Use ONLY plain text. NO markdown, NO bold/italic, NO backticks, NO hash signs.
- Separate sections with blank lines.
- Use simple hyphens for bullet points (- ).
- Keep the section titles exactly as given: "1. Technique(s):", "2. Expected Detections:", "3. Quieter Alternatives:"
- Do NOT wrap anything in asterisks.

Now output the debrief:`,
		mitreContext, args)

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

	// Strip any rogue markdown and add colour to section headers
	cleaned := cleanDebrief(strings.TrimSpace(resp))

	// Display via agent UX (typing effect, TUI‑routed)
	if agentCore != nil {
		agentCore.GetUX().PrintAIMessage(cleaned, agentCore.GetTypingEffect())
	} else {
		color.Cyan(cleaned)
	}

	// Standard mission‑complete banner
	if agentCore != nil {
		agentCore.GetUX().PrintSuccess("Helix :: GRID STATUS :: CLEAR")
	} else {
		color.Green("Helix :: GRID STATUS :: CLEAR")
	}
}

// cleanDebrief removes markdown artefacts and colourises section headers.
func cleanDebrief(text string) string {
	// Remove all double‑asterisks (bold markers)
	text = strings.ReplaceAll(text, "**", "")

	// Colourise the three expected section headers with cyan
	headers := []string{
		"1. Technique(s):",
		"2. Expected Detections:",
		"3. Quieter Alternatives:",
	}
	for _, h := range headers {
		coloured := color.New(color.FgCyan, color.Bold).Sprint(h)
		text = strings.Replace(text, h, coloured, 1)
	}

	return text
}

// truncateText shortens a string for display purposes.
func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// isPlatformCompatible checks if the exploit's platform string matches the current OS.
// Example: "windows" matches "windows", "linux" matches "linux", "multi" matches any.
func isPlatformCompatible(exploitPlatform, currentOS string) bool {
	plat := strings.ToLower(strings.TrimSpace(exploitPlatform))
	os := strings.ToLower(currentOS)
	if plat == "multi" || plat == "" {
		return true
	}
	// Handle "linux/windows" style
	parts := strings.Split(plat, "/")
	for _, p := range parts {
		if strings.TrimSpace(p) == os {
			return true
		}
	}
	return false
}

// computeExploitRisk categorises an exploit as GREEN, YELLOW, or RED.
func computeExploitRisk(e rag.ExploitEntry) string {
	// Heuristic: CVSS >= 7.0 or impact rce/lpe with high privileges => RED
	if e.CVSS >= 7.0 || (e.Impact == "rce" && e.Privileges == "none") || e.Impact == "lpe" {
		return "RED"
	}
	// CVSS >= 4.0 or dos/info with potential expansion => YELLOW
	if e.CVSS >= 4.0 || e.Impact == "dos" || e.Impact == "rce" {
		return "YELLOW"
	}
	return "GREEN"
}

// displayExploitDebrief shows the full exploit details in a structured format.
func displayExploitDebrief(e rag.ExploitEntry, justification string) {
	bold := color.New(color.FgCyan, color.Bold).SprintFunc()
	agentCore.GetUX().PrintSystemMessage("=== Exploit Debrief ===")
	agentCore.GetUX().PrintData(fmt.Sprintf("%s %s", bold("ID:"), e.ID))
	agentCore.GetUX().PrintData(fmt.Sprintf("%s %s", bold("CVE:"), e.CVE))
	agentCore.GetUX().PrintData(fmt.Sprintf("%s %s", bold("Platform:"), e.Platform))
	agentCore.GetUX().PrintData(fmt.Sprintf("%s %.1f", bold("CVSS Score:"), e.CVSS))
	agentCore.GetUX().PrintData(fmt.Sprintf("%s %s", bold("Impact:"), e.Impact))
	agentCore.GetUX().PrintData(fmt.Sprintf("%s %s", bold("Privileges:"), e.Privileges))
	agentCore.GetUX().PrintData(fmt.Sprintf("%s %s", bold("Detection:"), e.Detection))
	agentCore.GetUX().PrintData(fmt.Sprintf("%s %s", bold("Blast Radius:"), e.BlastRadius))
	agentCore.GetUX().PrintData(fmt.Sprintf("%s %s", bold("AI Justification:"), justification))
	agentCore.GetUX().PrintAIMessage("Alternatives (quieter paths):", false)
	for _, alt := range e.Alternatives {
		agentCore.GetUX().PrintData(fmt.Sprintf("  • %s", alt))
	}
	agentCore.GetUX().PrintSuccess("Helix :: GRID STATUS :: CLEAR")
}

// displayExploitFallback shows the AI response when no matching exploit entry is found.
func displayExploitFallback(id, justification string) {
	color.Cyan("AI suggested exploit: %s", id)
	if justification != "" {
		color.Cyan("Justification: %s", justification)
	}
	color.Yellow("Full exploit metadata not available in the knowledge base.")
	color.Yellow("You may still evaluate the suggestion manually.")
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

// -------------------------------------------------------
// /exploit – AI‑driven exploit suggestion (Phase 3.5 DB)
// -------------------------------------------------------
func handleExploitCommand(input string) {
	args := strings.TrimSpace(strings.TrimPrefix(input, "/exploit"))
	args = strings.Trim(args, `"'`)

	if args == "" {
		color.Red("Usage: /exploit <target description or vulnerability>")
		return
	}

	color.Cyan("Guardian – searching knowledge base...")

	if ragSystem == nil || !ragSystem.IsInitialized() {
		color.Red("Exploit knowledge base not available.")
		return
	}

	// First try the SQLite semantic search
	var entries []rag.KnowledgeEntry
	if ragSystem.GetDB() != nil {
		var err error
		entries, err = ragSystem.SemanticSearch(args, 10)
		if err != nil {
			color.Yellow("Semantic search issue: %v", err)
		}
	}

	// Fallback to hardcoded entries if DB returned nothing
	if len(entries) == 0 {
		allHardcoded := ragSystem.GetAllExploitEntries()
		if len(allHardcoded) == 0 {
			color.Red("No exploit entries available (neither DB nor hardcoded).")
			return
		}
		// Filter hardcoded entries by OS compatibility
		var compatible []rag.ExploitEntry
		for _, e := range allHardcoded {
			if isPlatformCompatible(e.Platform, env.OSName) {
				compatible = append(compatible, e)
			}
		}
		if len(compatible) == 0 {
			color.Red("No exploits available for your current OS (%s).", env.OSName)
			return
		}
		// Build context and let AI choose
		var sb strings.Builder
		sb.WriteString("Candidate exploits (filtered for current OS):\n")
		for _, e := range compatible {
			sb.WriteString(fmt.Sprintf("%s (%s): %s\n", e.ID, e.CVE, e.Description))
		}
		contextStr := sb.String()
		prompt := fmt.Sprintf(
			`You are Helix's Exploit Suggestor & Guardian.

Given the user's request and the list of candidate exploits, select the most appropriate one.
If no candidate clearly fits the request, output "NONE" as the exploit ID.

User Request: %s

%s

INSTRUCTIONS (STRICT):
- Output ONLY the exploit ID (e.g., EDB-12345) or "NONE" on the first line.
- The first line must contain ONLY the ID or "NONE". No other text.
- On the second line, output a ONE‑SENTENCE justification for your choice.
- NO markdown, NO backticks, NO extra text.

Now output your selection:`, args, contextStr,
		)
		selectCfg := ai.ModelConfig{Temperature: 0.4, TopP: 0.9, TopK: 40, MaxTokens: 80}
		resp, err := ai.RunModelWithConfig(prompt, selectCfg)
		if err != nil {
			color.Red("AI selection failed: %v", err)
			return
		}
		selectedID, justification := extractExploitIDAndJustification(resp)
		if strings.EqualFold(selectedID, "none") || selectedID == "" {
			color.Yellow("No suitable exploit found.")
			return
		}
		entry, found := ragSystem.GetExploitByID(selectedID)
		if !found {
			color.Red("Selected exploit not found: %s", selectedID)
			return
		}
		displayExploitDebrief(entry, justification)
		return
	}

	// DB entries: let AI select the best KnowledgeEntry
	var contextBuf strings.Builder
	contextBuf.WriteString("Candidate exploits from knowledge base:\n")
	for _, e := range entries {
		contextBuf.WriteString(fmt.Sprintf("%s (%s): %s\n", e.SourceID, e.Title, e.Description))
	}
	contextStr := contextBuf.String()
	prompt := fmt.Sprintf(
		`You are Helix's Exploit Suggestor & Guardian.

Given the user's request and the list of candidate exploits, select the most appropriate one.
If no candidate clearly fits the request, output "NONE" as the exploit ID.

User Request: %s

%s

INSTRUCTIONS (STRICT):
- Output ONLY the exploit ID (e.g., EDB-12345) or "NONE" on the first line.
- The first line must contain ONLY the ID or "NONE". No other text.
- On the second line, output a ONE‑SENTENCE justification for your choice.
- NO markdown, NO backticks, NO extra text.

Now output your selection:`, args, contextStr,
	)
	selectCfg := ai.ModelConfig{Temperature: 0.4, TopP: 0.9, TopK: 40, MaxTokens: 80}
	resp, err := ai.RunModelWithConfig(prompt, selectCfg)
	if err != nil {
		color.Red("AI selection failed: %v", err)
		return
	}
	selectedID, justification := extractExploitIDAndJustification(resp)
	if strings.EqualFold(selectedID, "none") || selectedID == "" {
		color.Yellow("No suitable exploit found.")
		return
	}

	// Try to get full details from hardcoded set first, else fallback to DB info
	if e, ok := ragSystem.GetExploitByID(selectedID); ok {
		displayExploitDebrief(e, justification)
		return
	}
	// DB-only entry: show basic info
	color.Cyan("Selected exploit: %s", selectedID)
	if justification != "" {
		color.Cyan("Justification: %s", justification)
	}
	color.Yellow("Full metadata not available; basic info shown.")
	if agentCore != nil {
		agentCore.GetUX().PrintSuccess("Helix :: GRID STATUS :: CLEAR")
	} else {
		color.Green("Helix :: GRID STATUS :: CLEAR")
	}
}

// extractExploitIDAndJustification parses the AI response, falling back to a
// regex to extract a valid exploit ID if the model returns extra text.
func extractExploitIDAndJustification(resp string) (id, justification string) {
	lines := strings.SplitN(strings.TrimSpace(resp), "\n", 2)
	id = strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		justification = strings.TrimSpace(lines[1])
	}
	id = strings.Trim(id, "`'\". ")

	// Fallback: if the ID doesn't look like an exploit ID, try to find one in the response
	if !strings.HasPrefix(id, "EDB-") && !strings.HasPrefix(id, "CVE-") && !strings.EqualFold(id, "none") {
		re := regexp.MustCompile(`(EDB-\d+|CVE-\d{4}-\d+)`)
		if match := re.FindString(resp); match != "" {
			id = match
		}
	}
	return
}
