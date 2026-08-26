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

	"helix/internal/agent"
	"helix/internal/ai"
	"helix/internal/audio"
	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/confinement"
	"helix/internal/deps"
	"helix/internal/diagnostics"
	"helix/internal/edge"
	"helix/internal/hooks"
	"helix/internal/ollama"
	"helix/internal/providers/llamacpp"
	"helix/internal/rag"
	"helix/internal/session"
	"helix/internal/shell"
	"helix/internal/speech"
	"helix/internal/utils"
	"helix/internal/ux"

	"github.com/fatih/color"
)

// -------------------------------------------------------
// /about — Helix philosophy & the creation
// -------------------------------------------------------

func handleAbout() {
	// The banner, the identity zone and its glitch are RenderAbout's, and stay
	// exactly as they are. Everything below is the part that was a flat wall of
	// text — and, until now, three closing rules with no opening rule above
	// them, so each section ended with a horizon that had come from nowhere.
	shell.RenderAbout(config.HelixVersion)

	aboutSection("the philosophy",
		"Helix inverts the terminal: instead of forcing humans to speak machine, "+
			"the machine learns to speak human. One prompt accepts shell, git, "+
			"packages, and plain thought — no mode switching.",
		"Every action passes through a safety-first pipeline: validation, risk "+
			"tiers, sandbox confinement, and typed confirmations for the dangerous "+
			"paths. Power without recklessness.",
		"Knowledge is local and explainable — MAN pages, CVEs, MITRE ATT&CK — "+
			"retrieved, cited, and defended. Helix thinks like the red team so it "+
			"can fight for the blue.")

	printAboutInstall()

	fmt.Println(shell.PanelTitle("the creation"))
	for _, l := range shell.PanelWrap(
		"Helix is designed and built by Nahasat Nibir — an AI Engineer crafting "+
			"intelligent, high-performance developer tools and AI-powered systems "+
			"in Go and Rust.", shell.Muted) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelGap())
	w := shell.KVWidth("GITHUB", "LINKEDIN", "ARTSTATION")
	for _, link := range [][2]string{
		{"GITHUB", "https://github.com/Nibir1"},
		{"LINKEDIN", "https://www.linkedin.com/in/nibir-1/"},
		{"ARTSTATION", "https://www.artstation.com/nibir"},
	} {
		fmt.Println(shell.KV(link[0], shell.Value(link[1]), w))
	}
	fmt.Println(shell.PanelEnd())
	fmt.Println()
}

// aboutSection renders one titled block of prose.
//
// Wrapped to the panel rather than hand-broken. The paragraphs used to carry
// their own line breaks at roughly 65 columns, so they were ragged on a wide
// terminal and unchanged on a narrow one — the one width they were correct at
// was whichever the author happened to have.
func aboutSection(title string, paragraphs ...string) {
	fmt.Println(shell.PanelTitle(title))
	for i, para := range paragraphs {
		if i > 0 {
			fmt.Println(shell.PanelGap())
		}
		for _, l := range shell.PanelWrap(para, shell.Muted) {
			fmt.Println(l)
		}
	}
	fmt.Println(shell.PanelEnd())
}

// printAboutInstall answers the question /about could not: what is THIS copy of
// Helix actually able to do right now.
//
// The philosophy says what Helix is for and the creation says who made it;
// between them there was nothing about the machine in front of you. A reader
// who has just been told Helix speaks, sees and reasons should be able to see,
// in the same breath, which of those are switched on here.
func printAboutInstall() {
	fmt.Println(shell.PanelTitle("this install"))

	w := shell.KVWidth("MIND", "HARNESS", "HEARING", "SIGHT", "CONFINEMENT")

	mind := shell.Badge(shell.StateWarn, "no provider") + shell.Muted("  /setup")
	if cfg.Provider != "" {
		model := cfg.ProviderModel
		if model == "" {
			model = "default model"
		}
		mind = shell.Badge(shell.StateGood, cfg.Provider) + shell.Muted("  ") + shell.Value(model)
	}
	fmt.Println(shell.KV("MIND", mind, w))

	harness := shell.Badge(shell.StateIdle, "single-shot")
	if agentCore != nil && agentCore.Agentic {
		harness = shell.Badge(shell.StateGood, "agentic") +
			shell.Muted(fmt.Sprintf("  observes and self-corrects, %d steps", agenticStepBudget()))
	}
	fmt.Println(shell.KV("HARNESS", harness, w))
	fmt.Println(shell.KV("HEARING", blackBoxHearingLine(), w))
	fmt.Println(shell.KV("SIGHT", blackBoxEyesLine(), w))
	fmt.Println(shell.KV("CONFINEMENT",
		shell.Value(sandboxMode())+shell.Muted("  ·  approval: "+agentPermission()), w))
	fmt.Println(shell.PanelEnd())
}

// agentPermission reports the approval posture without assuming an agent.
func agentPermission() string {
	if agentCore == nil {
		return "unknown"
	}
	return string(agentCore.Permission())
}

// -------------------------------------------------------
// /setup — Unified Setup Wizard
// -------------------------------------------------------

func handleSetup() {
	for {
		fmt.Println(shell.PanelTitle("setup"))
		for _, l := range shell.Menu(setupMenuItems()) {
			fmt.Println(l)
		}
		fmt.Println(shell.PanelEnd())
		choice := strings.TrimSpace(commands.AskLine(shell.Prompt("option", "1-5")))
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
		case "3":
			runDependencySetup(false)
		case "4":
			handleVoiceSetup()
		case "5", "q", "exit", "":
			return
		default:
			fmt.Println("  " + shell.Fg(shell.HexRectifier, "Invalid selection."))
		}
	}
}

// setupMenuItems describes each stage by its CURRENT state, so the wizard
// shows what is already done instead of asking the user to remember.
func setupMenuItems() []shell.MenuItem {
	name := cfg.UserPrefs.UserName
	if name == "" {
		name = "not set"
	}
	provider := "none selected"
	if cfg.Provider != "" {
		provider = cfg.Provider
		if cfg.ProviderModel != "" {
			provider += " / " + cfg.ProviderModel
		}
	}
	var pkgs, pkgTag string
	if missing := deps.Missing(); len(missing) == 0 {
		pkgs, pkgTag = "all present", "done"
	} else {
		names := make([]string, 0, len(missing))
		for _, d := range missing {
			names = append(names, d.Name)
		}
		pkgs, pkgTag = "missing: "+strings.Join(names, ", "), "action needed"
	}
	voice, voiceTag := "not configured", ""
	if reg := speech.Default(); reg != nil && len(reg.STTChain()) > 0 {
		voice, voiceTag = strings.Join(reg.STTChain(), " → "), "done"
	}

	return []shell.MenuItem{
		{Label: "identity", Note: name, Good: cfg.UserPrefs.UserName != ""},
		{Label: "ai provider", Note: provider, Good: cfg.Provider != ""},
		{Label: "system packages", Note: pkgs, Tag: pkgTag, Good: pkgTag == "done"},
		{Label: "voice", Note: voice, Tag: voiceTag, Good: voiceTag == "done"},
		{Label: "exit", Note: "back to the shell"},
	}
}

// -------------------------------------------------------
// /debug — Toggle Debug Logging
// -------------------------------------------------------

func handleDebugCommand(c cmdArgs) {
	if c.Empty() {
		current := "OFF"
		if utils.IsDebugMode() {
			current = "ON"
		}
		color.Cyan("Debug mode is currently: %s", current)
		color.Yellow("Usage: /debug <on|off>")
		return
	}
	switch c.Sub() {
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
		color.Red("Unknown debug setting: %s", c.Arg(0))
		color.Yellow("Usage: /debug <on|off>")
		return
	}
	_ = cfg.SavePreferences()
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

	// Agentic harness + approval posture. These two decide how much happens
	// without being asked, which makes them the most consequential lines here.
	if agentCore != nil {
		mode := agentCore.Permission()
		line := fmt.Sprintf("  Permission mode: %s — %s", mode, mode.Describe())
		if mode == agent.PermissionAsk {
			color.Cyan(line)
		} else {
			color.Yellow(line)
		}
		if agentCore.Agentic {
			color.Green("  Agentic harness: ON (step budget %d)", agenticStepBudget())
		} else {
			color.Yellow("  Agentic harness: OFF (single-shot planning)")
		}
		if n := agentCore.HookCount(); n > 0 {
			color.Cyan("  Local policy hooks: %d loaded (/hooks)", n)
		}
	}

	// Task list — open work the planner can see.
	if todoList != nil {
		counts := todoList.Counts()
		open := counts[session.TodoPending] + counts[session.TodoInProgress] +
			counts[session.TodoBlocked]
		if open > 0 {
			color.Cyan("  Tasks: %d open, %d done (/todo)", open, counts[session.TodoDone])
		} else if counts[session.TodoDone] > 0 {
			color.Cyan("  Tasks: all %d complete (/todo prune to clear)", counts[session.TodoDone])
		}
	}

	// Project context.
	if _, path, ok := loadProjectContext(); ok {
		color.Cyan("  Project context: %s", path)
	} else {
		color.Yellow("  Project context: none here (/init writes HELIX.md)")
	}

	// Conversation memory and session spend.
	if agentCore != nil && agentCore.Session != nil {
		color.Cyan("  Conversation memory: %d/%d turns",
			agentCore.Session.Len(), agentCore.Session.Capacity())
	}
	if usage := ai.Usage(); usage.Calls > 0 {
		color.Cyan("  Model calls: %d (%d failed) · ~%d est. tokens (/cost)",
			usage.Calls, usage.Failures, usage.EstTotalTokens())
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

func handleSandboxCommand(c cmdArgs) {
	if sandbox == nil {
		color.Red("Sandbox is not available in this session.")
		return
	}
	if c.Empty() {
		sandbox.PrintStatus()
		color.Yellow("Usage: /sandbox <off|current|strict>")
		color.Yellow("  off      no directory confinement")
		color.Yellow("  current  confine to the current directory tree")
		color.Yellow("  strict   current-dir confinement plus kernel confinement (%s)",
			confinement.BackendName())
		return
	}
	mode := c.Sub()
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

func handleChangeDirectory(c cmdArgs) {
	targetDir := c.Rest
	if targetDir == "" {
		current, _ := os.Getwd()
		color.Cyan("Current directory: %s", current)
		return
	}
	if err := sandbox.ChangeDirectory(targetDir); err != nil {
		color.Red("Failed to change directory: %v", err)
	}
}

func handleGitCommand(c cmdArgs) {
	request := c.Rest
	if request == "" {
		color.Red("Usage: /git <natural-language git operation>")
		color.Yellow("Examples: /git commit everything with a sensible message")
		color.Yellow("          /git show me what changed on this branch")
		return
	}
	if gitManager == nil {
		color.Red("Git manager is not available in this session.")
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
	color.Yellow("This deletes every RAG index and embedding cache on disk.")
	if !commands.AskForConfirmation("Wipe all RAG vector data?") {
		color.Yellow("RAG reset cancelled.")
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		color.Red("Cannot resolve the home directory: %v", err)
		return
	}
	ragDir := filepath.Join(home, ".helix", "rag_index")
	if err := os.RemoveAll(ragDir); err != nil {
		color.Red("Failed to reset RAG data: %v", err)
		return
	}
	color.Green("RAG data deleted from %s.", ragDir)
	// The running system still holds the state it loaded at boot, so it will
	// keep answering retrievals from memory. Saying "reset completed" and
	// stopping there made that look like a working empty index.
	color.Yellow("The running RAG system still holds what it loaded at startup.")
	color.Yellow("Run /rag-rebuild to index again now, or restart Helix for a clean load.")
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
	// The agent holds its own copy of the exec config, so flipping the shell's
	// struct alone left dry-run advertised as ENABLED while the agent kept
	// executing. Push it through.
	if agentCore != nil {
		agentCore.SetDryRun(execConfig.DryRun)
	}
	if execConfig.DryRun {
		color.Yellow("Dry-run mode ENABLED — commands are printed, never executed")
		color.Cyan("For a whole session that only plans, /permissions plan is the stronger form.")
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
	color.Cyan("Smoke testing %s (%s)…", ai.ActiveProviderName(), ai.ActiveModel())
	think := ux.NewThinker("HELIX :: SMOKE TEST")
	think.Start()
	started := time.Now()
	resp, err := ai.RunModel("Reply with exactly: hello world")
	elapsed := time.Since(started)
	think.Stop()

	if err != nil {
		color.Red("Smoke test FAILED after %v: %v", elapsed.Round(time.Millisecond), err)
		color.Yellow("Run /provider-status to see whether the brain is reachable at all.")
		return
	}
	resp = strings.TrimSpace(resp)
	if resp == "" {
		// An empty 200 is a real and confusing failure mode on reasoning models
		// that burn their token budget before emitting anything.
		color.Yellow("The model answered in %v but returned NOTHING.", elapsed.Round(time.Millisecond))
		color.Yellow("That usually means the token budget was consumed before any output.")
		return
	}
	color.Green("Smoke test OK in %v: %s", elapsed.Round(time.Millisecond), truncStr(resp, 120))
}

func handleStealthCommand(c cmdArgs) {
	if c.Empty() {
		if agentCore != nil {
			color.Cyan("Stealth mode: %v", agentCore.IsStealthEnabled())
		}
		color.Yellow("Usage: /stealth <on|off>")
		return
	}
	switch c.Sub() {
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
func handleAgenticCommand(c cmdArgs) {
	if agentCore == nil {
		color.Red("Agent is not available in this session.")
		return
	}
	// /agentic steps <n> retunes the budget without leaving the command: the
	// budget was previously a compile-time constant the help text advertised
	// but nothing could change.
	if c.Sub() == "steps" || c.Sub() == "budget" {
		setAgenticBudget(c.Arg(1))
		return
	}
	switch c.Lower() {
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

// setAgenticBudget retunes the harness step budget for this session and
// persists it, so a long multi-step task does not need a rebuild to get more
// iterations than the default.
func setAgenticBudget(raw string) {
	if strings.TrimSpace(raw) == "" {
		color.Cyan("Agentic step budget: %d. Set it with /agentic steps <1-20>.", agenticStepBudget())
		return
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxAgenticStepBudget {
		color.Red("Step budget must be a whole number between 1 and %d.", maxAgenticStepBudget)
		return
	}
	agentCore.MaxAgenticSteps = n
	cfg.UserPrefs.AgenticSteps = n
	_ = cfg.SavePreferences()
	color.Green("Agentic step budget set to %d.", n)
	if !agentCore.Agentic {
		color.Yellow("The harness is currently OFF — run /agentic on to use it.")
	}
}

// maxAgenticStepBudget caps the harness. Each iteration is a full planner call
// plus execution, so an unbounded budget is an unbounded bill and an unbounded
// blast radius.
const maxAgenticStepBudget = 20

func agenticStepBudget() int {
	if agentCore != nil && agentCore.MaxAgenticSteps > 0 {
		return agentCore.MaxAgenticSteps
	}
	return 4
}

func handleMemoryCommand(c cmdArgs) {
	action := "show"
	if !c.Empty() {
		action = c.Sub()
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
		// Archive before wiping, exactly as /clear does: a transcript is cheap
		// to keep and impossible to get back.
		turns := agentCore.SessionTurns()
		archived, aerr := session.SaveSnapshot("memory-clear", turns)
		if aerr != nil {
			color.Yellow("Could not archive the conversation first: %v", aerr)
		}
		if err := agentCore.Session.Clear(); err != nil {
			color.Red("Memory clear failed: %v", err)
			return
		}
		color.Green("Conversation memory cleared (%d turn(s)).", len(turns))
		if archived != "" {
			color.Cyan("Archived as %s — restore it with /resume %s", archived, archived)
		}
	default:
		color.Yellow("Usage: /memory <show|clear>")
	}
}

// -------------------------------------------------------
// RECON
// -------------------------------------------------------
func handleQuickScan(c cmdArgs) {
	if c.Empty() {
		color.Cyan("Usage:")
		color.Cyan("  /scan authorize <target> --reason \"<written scope>\"   record authorization")
		color.Cyan("  /scan status                                          list authorized targets")
		color.Cyan("  /scan revoke <target>                                 withdraw authorization")
		color.Cyan("  /scan <target>                                        scan an authorized target")
		color.Yellow("Authorization is required first: written scope is the record that this")
		color.Yellow("was a permitted engagement, and Helix will not scan without it.")
		return
	}
	if agentCore == nil {
		color.Red("Agent not initialized")
		return
	}
	switch c.Sub() {
	case "authorize":
		if c.Count() < 2 {
			color.Red("Usage: /scan authorize <target> --reason \"<written scope>\"")
			return
		}
		target := c.Arg(1)
		reason := "manual authorization"
		for i, arg := range c.Fields {
			if strings.EqualFold(arg, "--reason") && i+1 < len(c.Fields) {
				// Strip the quotes the shell would normally have removed:
				// --reason "web app pentest" arrived as a quoted first token and
				// was recorded with the quote characters embedded.
				reason = strings.Trim(strings.Join(c.Fields[i+1:], " "), `"'`)
				break
			}
		}
		agentCore.AuthorizeRecon(target, reason)
	case "revoke", "deauthorize":
		if c.Count() < 2 {
			color.Red("Usage: /scan revoke <target>")
			return
		}
		if agentCore.RevokeRecon(c.Arg(1)) {
			color.Yellow("Authorization withdrawn for %s.", c.Arg(1))
		} else {
			color.Yellow("%s was not authorized.", c.Arg(1))
		}
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
		target := c.Arg(0)
		if !agentCore.IsReconTargetAuthorized(target) {
			color.Red("Target %q is not authorized for reconnaissance.", target)
			color.Yellow("Authorize it first: /scan authorize %s --reason \"<written scope>\"", target)
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
func handleExplainCommand(c cmdArgs) {
	args := c.Rest
	if args == "" {
		color.Red("Usage: /explain <command or technique description>")
		return
	}
	if !requireAgent() {
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
	resp, err := agentCore.AskModel("HELIX :: REASONING", prompt, explainConfig)
	if err != nil {
		color.Red("AI call failed: %v", err)
		color.Yellow("Run /provider-status to see whether the brain is reachable.")
		return
	}
	cleaned := cleanDebrief(strings.TrimSpace(resp))
	if cleaned == "" {
		color.Yellow("The AI model returned an empty explanation. Try rephrasing the request or checking /provider-status.")
		return
	}
	// PrintAnswer, not the raw UX call: this routes through the same seam as
	// every other reply, so the debrief is spoken when TTS is on and recorded in
	// session memory instead of vanishing.
	agentCore.PrintAnswer(cleaned)
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

func handleVulnCommand(c cmdArgs) {
	query := strings.Trim(c.Rest, `"'`)
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

// handleDoctor renders the full local diagnostic.
//
// Panelized like every other report. It was the last flat `=== Helix Doctor ===`
// stack: twenty-odd cyan lines in which "Database ping failed" and "Shell: zsh"
// carried identical weight, so the one line a person opened /doctor to find had
// to be hunted for. State now lives in the badge colour and the label column
// does the scanning.
func handleDoctor() {
	w := shell.KVWidth("CONFIG", "DATABASE", "PROVIDER", "NETWORK", "CONFINEMENT",
		"DAEMON", "HOOKS", "PROJECT", "CRASH REPORTS")

	fmt.Println(shell.PanelTitle("doctor"))

	if home, err := os.UserHomeDir(); err != nil {
		fmt.Println(shell.KV("CONFIG", shell.Badge(shell.StateBad, "unreadable")+
			shell.Muted("  "+err.Error()), w))
	} else {
		helixDir := filepath.Join(home, ".helix")
		if fi, serr := os.Stat(helixDir); serr == nil && fi.IsDir() {
			fmt.Println(shell.KV("CONFIG", shell.Badge(shell.StateGood, "ok")+
				shell.Muted("  "+helixDir), w))
		} else {
			fmt.Println(shell.KV("CONFIG", shell.Badge(shell.StateBad, "missing")+
				shell.Muted("  "+helixDir), w))
		}
	}

	if ragSystem != nil && ragSystem.GetDB() != nil {
		db := ragSystem.GetDB()
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := db.PingContext(pingCtx)
		pingCancel()
		if err != nil {
			fmt.Println(shell.KV("DATABASE", shell.Badge(shell.StateBad, "ping failed")+
				shell.Muted("  "+err.Error()), w))
		} else {
			fmt.Println(shell.KV("DATABASE", shell.Badge(shell.StateGood, "connected")+
				shell.Muted(fmt.Sprintf("  knowledge schema v%d", rag.SchemaVersion(db))), w))
		}
	}

	fmt.Println(shell.KV("PROVIDER", shell.Value(string(ai.GetProvider()))+
		shell.Muted(fmt.Sprintf("  ·  local model loaded: %v", ai.ModelIsLoaded())), w))

	if utils.IsOnline(3 * time.Second) {
		fmt.Println(shell.KV("NETWORK", shell.Badge(shell.StateGood, "online"), w))
	} else {
		fmt.Println(shell.KV("NETWORK", shell.Badge(shell.StateWarn, "offline")+
			shell.Muted("  local providers only"), w))
	}

	host := shell.Value(env.OSName) + shell.Muted("  ·  "+env.Shell)
	if sandbox != nil {
		host += shell.Muted("  ·  sandbox: " + sandbox.ModeString())
	}
	fmt.Println(shell.KV("HOST", host, w))
	fmt.Println(shell.KV("CONFINEMENT", shell.Muted(confinement.BackendName()), w))
	fmt.Println(shell.PanelEnd())

	// BlackBox P10.3: the edge-appliance picture.
	printEdgeSection()

	fmt.Println(shell.PanelTitle("environment"))

	// BlackBox Phase 4: Living AI daemon presence.
	if daemonRunning() {
		fmt.Println(shell.KV("DAEMON", shell.Badge(shell.StateGood, "running")+
			shell.Muted("  Living AI"), w))
	} else {
		fmt.Println(shell.KV("DAEMON", shell.Badge(shell.StateIdle, "not running")+
			shell.Muted("  helix daemon starts it"), w))
	}

	// A hook file that failed to parse means NO hooks run, which is invisible
	// until something the user believed was guarded goes through. State it here
	// rather than only at startup.
	if set, herr := hooks.Load(); herr != nil {
		fmt.Println(shell.KV("HOOKS", shell.Badge(shell.StateBad, "config failed")+
			shell.Muted("  NO hooks are active — fix or remove the file; /hooks shows its path"), w))
		for _, l := range shell.PanelWrap(herr.Error(), shell.Muted) {
			fmt.Println(l)
		}
	} else if len(set.Hooks) == 0 {
		fmt.Println(shell.KV("HOOKS", shell.Badge(shell.StateIdle, "none configured"), w))
	} else {
		blocking, disabled := 0, 0
		for _, h := range set.Hooks {
			if h.Disabled {
				disabled++
				continue
			}
			if h.Blocking {
				blocking++
			}
		}
		fmt.Println(shell.KV("HOOKS", shell.Badge(shell.StateGood, fmt.Sprintf("%d loaded", len(set.Hooks)))+
			shell.Muted(fmt.Sprintf("  %d blocking  ·  %d disabled", blocking, disabled)), w))
	}

	if _, path, ok := loadProjectContext(); ok {
		fmt.Println(shell.KV("PROJECT", shell.Badge(shell.StateGood, "loaded")+
			shell.Muted("  "+path), w))
	} else {
		fmt.Println(shell.KV("PROJECT", shell.Badge(shell.StateIdle, "none here")+
			shell.Muted("  /init writes HELIX.md"), w))
	}

	if summaries := diagnostics.ListReports(); len(summaries) > 0 {
		fmt.Println(shell.KV("CRASH REPORTS", shell.Badge(shell.StateWarn,
			fmt.Sprintf("%d pending", len(summaries)))+
			shell.Muted("  local-only  ·  /purge deletes them"), w))
		for _, sm := range summaries {
			fmt.Println(shell.PanelLine(shell.Muted("  " + sm.Time + " — " + sm.Reason)))
			fmt.Println(shell.PanelLine(shell.Muted("  " + sm.Path)))
		}
	} else {
		fmt.Println(shell.KV("CRASH REPORTS", shell.Badge(shell.StateGood, "none")+
			shell.Muted("  telemetry-free"), w))
	}
	fmt.Println(shell.PanelEnd())
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

	// Endpoint collisions BEFORE the reachability probes below: a probe that
	// reports "reachable" on a port owned by a different service is the most
	// misleading line /doctor can print, and this explains it.
	reportEndpointConflicts()

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
		// Deliberately NOT falling back to ai.ActiveModel(): that is the CLOUD
		// model, and borrowing its name produced the advice
		// "run `ollama pull deepseek-v4-flash-vision-exp`" — a model id that
		// exists in no Ollama registry, so the one actionable line in the
		// offline-brain check was a command guaranteed to fail. An unset
		// fallback model is unset; say that instead of inventing one.
		if fb.Model == "" {
			color.Yellow("Offline LLM (ollama): running, but no fallback model is configured.")
			suggestOllamaModel(ctx, client)
			return
		}
		if ollamaHasModel(ctx, client, fb.Model) {
			color.Green("Offline LLM (ollama): ready — %s", fb.Model)
		} else {
			color.Yellow("Offline LLM (ollama): %q is configured but not pulled — run `ollama pull %s`",
				fb.Model, fb.Model)
			suggestOllamaModel(ctx, client)
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
// suggestOllamaModel names models this Ollama actually has, so the follow-up is
// a command that can succeed.
//
// Reporting what is INSTALLED beats suggesting something to pull: the user with
// a running Ollama usually already has a model, and the fix is one config line
// rather than a download.
func suggestOllamaModel(ctx context.Context, client *ollama.Client) {
	installed, err := client.ListModels(ctx)
	if err != nil || len(installed) == 0 {
		color.Yellow("  → pull one, then set it:  ollama pull llama3.2  ·  /config fallback-model llama3.2")
		return
	}
	names := make([]string, 0, len(installed))
	for _, m := range installed {
		names = append(names, m.ID)
		if len(names) == 4 {
			break
		}
	}
	color.Yellow("  → this Ollama has: %s", strings.Join(names, ", "))
	color.Yellow("  → set one:  /config fallback-model %s", names[0])
}

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

// handleProviderStatus reports every provider and which one is answering.
//
// Rendered through the panel system like every other report. It was the last
// flat `=== Provider Status ===` stack of identical cyan lines, in which the
// one fact a reader is looking for — which provider is active and whether it
// can be reached — carried no more visual weight than nine rows of "API key
// missing" for providers they have never configured.
func handleProviderStatus() {
	rows := ai.ProviderStatusRows()

	fmt.Println(shell.PanelTitle("providers"))
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		state := shell.Badge(shell.StateIdle, "no key")
		switch {
		case r.Local:
			state = shell.Badge(shell.StateGood, "local") + shell.Muted("  no key needed")
		case r.KeyState == "configured":
			state = shell.Badge(shell.StateGood, "key set")
		}
		mark := shell.Muted("")
		if r.Active {
			mark = shell.Badge(shell.StateGood, "active")
		}
		cells = append(cells, []string{shell.Value(r.Name), shell.Muted(r.Display), state, mark})
	}
	if len(cells) == 0 {
		fmt.Println(shell.PanelLine(shell.Muted("no providers registered")))
	} else {
		for _, l := range shell.Table([]string{"provider", "name", "key", ""}, cells) {
			fmt.Println(l)
		}
	}
	fmt.Println(shell.PanelGap())

	w := shell.KVWidth("ACTIVE", "REACHABILITY", "OFFLINE", "PLANNER")
	fmt.Println(shell.KV("ACTIVE", shell.Value(ai.ActiveProviderName())+
		shell.Muted("  ·  ")+shell.Value(ai.ActiveModel()), w))
	fmt.Println(shell.KV("REACHABILITY", activeProviderHealthLine(), w))

	// BlackBox P11.2: when the breaker is engaged the ACTIVE row above already
	// names the LOCAL model, which would otherwise look like the user's own
	// choice. Say why.
	fallback := shell.Muted(ai.FailoverStatus())
	if ai.LocalFallbackActive() {
		fallback = shell.Badge(shell.StateWarn, "engaged") + shell.Muted("  "+ai.FailoverStatus())
	}
	fmt.Println(shell.KV("OFFLINE", fallback, w))

	// BlackBox P8.7: which mechanism carries the plan. Worth surfacing because
	// it explains a real behavior difference — native tool calling removes the
	// JSON-repair retries the prompt path needs.
	fmt.Println(shell.KV("PLANNER", shell.Muted(ai.PlannerTransport()), w))
	fmt.Println(shell.PanelEnd())
}

// printActiveProviderHealth probes the active provider and says whether it can
// actually answer.
//
// The listing above it only reports API-key state, which says nothing at all
// about a local provider: an llama.cpp with nothing listening on its port showed
// up as "local/no key (active)" while the startup banner had just said "every
// planner and chat request will fail". A status command that cannot tell those
// two situations apart is the same defect the old /voice-status had.
//
// Probing here is deliberate and matches /blackbox status: this is an explicit
// diagnostic the user asked for, not the turn loop (which reads the recorded
// state instead). The result is recorded, so the next GRID STATUS line agrees
// with what this printed.
func printActiveProviderHealth() {
	fmt.Println(shell.KV("REACHABILITY", activeProviderHealthLine(),
		shell.KVWidth("REACHABILITY")))
}

// activeProviderHealthLine probes the active provider and renders one honest
// line: whether it can actually answer, and what to do when it cannot.
//
// The key listing says nothing about this. An llama.cpp with nothing on its
// port shows as "local/no key (active)" while every request fails — the same
// readiness lie /blackbox status exists to prevent, one subsystem over.
func activeProviderHealthLine() string {
	if ai.ActiveProviderName() == "" {
		return shell.Badge(shell.StateWarn, "no provider") +
			shell.Muted("  /provider use <name>")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := ai.CheckActiveProvider(ctx); err != nil {
		hint := activeProviderHint(err)
		detail := err.Error()
		if len(hint) > 0 {
			detail = hint[0]
		}
		return shell.Badge(shell.StateBad, "unreachable") + shell.Muted("  "+detail)
	}
	return shell.Badge(shell.StateGood, "ok")
}

// activeProviderHint returns the actionable follow-up for an unreachable active
// provider. llama.cpp gets its dedicated diagnosis (a foreign service on the
// port reads very differently from nothing listening); everything else gets the
// generic pointer, since a cloud outage has no local fix.
func activeProviderHint(err error) []string {
	if ai.ActiveProviderName() != llamacpp.Name {
		return []string{"Check connectivity and the API key, or /provider use <name> to switch."}
	}
	url := llamacpp.BaseURL(cfg.LLM.LlamaCppURL)
	_, hint := llamacpp.Diagnose(err, url)
	return strings.Split(hint, "\n")
}

func handleProviderCommand(c cmdArgs) {
	if c.Empty() {
		displayProviderStatus()
		return
	}
	switch c.Sub() {
	case "status":
		displayProviderStatus()
	case "list":
		color.Cyan("Registered providers: %s", strings.Join(ai.ListProviders(), ", "))
		color.Cyan("Active: %s (%s)", ai.ActiveProviderName(), ai.ActiveModel())
	case "use":
		if c.Count() < 2 {
			color.Red("Usage: /provider use <provider>")
			color.Yellow("Registered: %s", strings.Join(ai.ListProviders(), ", "))
			return
		}
		switchProvider(c.Arg(1))
	default:
		// /help has always documented "/provider <name>" while only
		// "/provider use <name>" worked, so the documented form silently did
		// nothing. Accept both: a bare argument that names a provider IS the
		// switch request, and anything else says so instead of staying quiet.
		name := c.Sub()
		if ai.HasProvider(name) {
			switchProvider(name)
			return
		}
		color.Red("Unknown provider or subcommand: %s", c.Arg(0))
		color.Yellow("Usage: /provider [status|list|use <name>|<name>]")
		color.Yellow("Registered: %s", strings.Join(ai.ListProviders(), ", "))
	}
}

// switchProvider activates a provider and persists the choice.
func switchProvider(name string) {
	name = strings.ToLower(strings.TrimSpace(name))
	if err := useProviderInteractive(name); err != nil {
		color.Red("Provider switch failed: %v", err)
		return
	}
	cfg.Provider = name
	cfg.ProviderModel = ai.ActiveModel()
	_ = cfg.SavePreferences()
	color.Green("Active provider: %s (%s)", name, ai.ActiveModel())
}

func handleModelCommand(c cmdArgs) {
	if c.Empty() {
		color.Cyan("Active model: %s (%s)", ai.ActiveModel(), ai.ActiveProviderName())
		color.Yellow("Usage: /model [list|use <model-id>|<model-id>]")
		return
	}
	switch c.Sub() {
	case "list", "ls":
		listAvailableModels()
	case "use":
		if c.Count() < 2 {
			color.Red("Usage: /model use <model-id>")
			return
		}
		switchModel(c.From(1))
	default:
		// Same drift as /provider: "/model <id>" was documented but unhandled.
		// A model ID is not enumerable offline, so a bare argument is taken as
		// the ID and the provider reports if it is wrong.
		switchModel(c.Rest)
	}
}

// switchModel activates a model on the current provider and persists it.
func switchModel(model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		color.Red("Usage: /model use <model-id>")
		return
	}
	if err := useModelInteractive(ai.ActiveProviderName(), model); err != nil {
		color.Red("Model switch failed: %v", err)
		return
	}
	cfg.ProviderModel = ai.ActiveModel()
	_ = cfg.SavePreferences()
	color.Green("Active model: %s", ai.ActiveModel())
}

func displayProviderStatus() {
	lines := ai.ProviderStatus()
	color.Cyan("=== Provider Status ===")
	for _, line := range lines {
		color.Cyan(line)
	}
	// /provider status and /provider-status are separate handlers; both must
	// answer "can the selected brain actually be reached?", or which one the user
	// happened to type decides whether they find out.
	color.Cyan("Active Provider: %s", ai.ActiveProviderName())
	printActiveProviderHealth()
}

func handleAudioCommand(c cmdArgs) {
	if c.Empty() {
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
	switch c.Sub() {
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
	default:
		color.Red("Unknown audio setting: %s", c.Arg(0))
		color.Yellow("Usage: /audio <on|off>")
	}
}

// -------------------------------------------------------
// CRASH DIAGNOSTICS (/crash)
// -------------------------------------------------------

func handleCrashCommand(c cmdArgs) {
	action := "list"
	if !c.Empty() {
		action = c.Sub()
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
		if c.Count() < 2 {
			color.Red("Usage: /crash view <number>")
			return
		}
		summaries := diagnostics.ListReports()
		if len(summaries) == 0 {
			color.Yellow("No crash reports to view.")
			return
		}

		idx, err := strconv.Atoi(c.Arg(1))
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
func handleTypewriteAllCommand(c cmdArgs) {
	if c.Empty() {
		current := "OFF"
		if cfg.UserPrefs.TypewriteAll {
			current = "ON"
		}
		color.Cyan("Typewrite-all mode is currently: %s", current)
		color.Yellow("Usage: /typewrite-all <on|off>")
		return
	}

	// agentCore is nil in a session that failed to build an agent; reading its
	// UX unconditionally turned a harmless toggle into a crash.
	var gui *ux.UX
	if agentCore != nil {
		gui = agentCore.GetUX()
	}

	switch c.Sub() {
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
		color.Red("Unknown setting: %s", c.Arg(0))
		color.Yellow("Usage: /typewrite-all <on|off>")
		return
	}
	_ = cfg.SavePreferences()
}
