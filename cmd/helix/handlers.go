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
			fmt.Println("  " + shell.Fg(shell.HexMuted, "Current Identity: ") + shell.Fg(shell.HexPrimary, currentName))
			newName := strings.TrimSpace(commands.AskLine("  Enter your name (or leave blank to cancel)"))
			if newName != "" {
				cfg.UserPrefs.UserName = newName
				if err := cfg.SavePreferences(); err != nil {
					uiFail("preferences", "could not be saved: "+err.Error())
				} else {
					shell.SetUserName(newName)
					fmt.Println("  " + shell.Fg(shell.HexSecondary, "✔ Identity updated to: ") + shell.Fg(shell.HexPrimary, newName))
				}
			}
		case "2":
			if err := runNativeSetup(); err != nil {
				uiFail("provider setup", err.Error())
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
		// utils.IsDebugMode reads the ENV, which is the state that actually
		// governs logging — cfg.UserPrefs.DebugMode is only what it was set
		// from, and the two diverge the moment HELIX_DEBUG is exported by hand.
		uiToggle("DEBUG", utils.IsDebugMode(), "verbose logging is on",
			"only the essentials are logged", "/debug <on|off>")
		return
	}
	switch c.Sub() {
	case "on", "enable":
		utils.SetDebugMode(true)
		_ = os.Setenv("HELIX_DEBUG", "1")
		cfg.UserPrefs.DebugMode = true
		uiOK("debug", "on — verbose logging")
	case "off", "disable":
		utils.SetDebugMode(false)
		_ = os.Unsetenv("HELIX_DEBUG")
		cfg.UserPrefs.DebugMode = false
		uiIdle("debug", "off — only the essentials are logged")
	default:
		uiFail(c.Arg(0), "is not a /debug setting")
		uiUsage("/debug <on|off>")
		return
	}
	_ = cfg.SavePreferences()
}

// -------------------------------------------------------
// STATUS / SANDBOX / CD / GIT
// -------------------------------------------------------

// handleStatus reports the session at a glance.
//
// Panelized like every other report. It was a flat stack of cyan lines with
// box-drawing pseudo-tree glyphs, in which "Stealth Mode: ENABLED" and
// "Typewrite-All: DISABLED" carried identical weight — so the two lines that
// actually decide how much happens without being asked (approval posture and
// the agentic harness) had to be hunted for.
func handleStatus() {
	w := shell.KVWidth("KNOWLEDGE", "PROVIDER", "APPROVAL", "HARNESS", "MEMORY", "PROJECT")

	fmt.Println(shell.PanelTitle("session"))

	// Approval posture and the harness first: they decide how much happens
	// without being asked, which makes them the most consequential state here
	// and the reason to open with them rather than with index counts.
	if agentCore != nil {
		mode := agentCore.Permission()
		state := shell.StateGood
		if mode != agent.PermissionAsk {
			state = shell.StateWarn
		}
		fmt.Println(shell.KV("APPROVAL", shell.Badge(state, string(mode))+
			shell.Muted("  "+mode.Describe()), w))

		if agentCore.Agentic {
			fmt.Println(shell.KV("HARNESS", shell.Badge(shell.StateGood, "agentic")+
				shell.Muted(fmt.Sprintf("  observes and self-corrects, %d steps",
					agenticStepBudget())), w))
		} else {
			fmt.Println(shell.KV("HARNESS", shell.Badge(shell.StateIdle, "single-shot")+
				shell.Muted("  /agentic on lets Helix self-correct"), w))
		}
	}

	fmt.Println(shell.KV("PROVIDER", shell.Value(ai.ActiveProviderName())+
		shell.Muted("  ·  ")+shell.Value(ai.ActiveModel()), w))
	fmt.Println(shell.KV("SANDBOX", shell.Muted(sandboxMode())+
		shell.Muted("  ·  confinement: "+confinement.BackendName()), w))

	fmt.Println(shell.PanelGap())
	fmt.Println(shell.KV("INDEX", ragIndexLine(), w))
	fmt.Println(shell.KV("KNOWLEDGE", knowledgeLine(), w))

	fmt.Println(shell.PanelGap())
	if agentCore != nil && agentCore.Session != nil {
		fmt.Println(shell.KV("MEMORY", shell.Value(fmt.Sprintf("%d/%d turns",
			agentCore.Session.Len(), agentCore.Session.Capacity())), w))
	}
	if usage := ai.Usage(); usage.Calls > 0 {
		fmt.Println(shell.KV("SPEND", shell.Value(fmt.Sprint(usage.Calls))+
			shell.Muted(fmt.Sprintf(" calls  ·  %d failed  ·  ~%d est. tokens  ·  /cost",
				usage.Failures, usage.EstTotalTokens())), w))
	}
	if todoList != nil {
		counts := todoList.Counts()
		open := counts[session.TodoPending] + counts[session.TodoInProgress] +
			counts[session.TodoBlocked]
		if open > 0 || counts[session.TodoDone] > 0 {
			fmt.Println(shell.KV("TASKS", shell.Value(fmt.Sprint(open))+
				shell.Muted(fmt.Sprintf(" open  ·  %d done  ·  /todo", counts[session.TodoDone])), w))
		}
	}
	if _, path, ok := loadProjectContext(); ok {
		fmt.Println(shell.KV("PROJECT", shell.Badge(shell.StateGood, "loaded")+
			shell.Muted("  "+path), w))
	} else {
		fmt.Println(shell.KV("PROJECT", shell.Badge(shell.StateIdle, "none here")+
			shell.Muted("  /init writes HELIX.md"), w))
	}

	// Toggles last, on one line each only when they are OFF the default —
	// twelve rows of "DISABLED" is what made this screen unreadable.
	fmt.Println(shell.PanelGap())
	fmt.Println(shell.KV("TOGGLES", sessionToggleLine(), w))
	if agentCore != nil {
		if n := agentCore.HookCount(); n > 0 {
			fmt.Println(shell.KV("HOOKS", shell.Value(fmt.Sprint(n))+
				shell.Muted(" loaded  ·  /hooks"), w))
		}
	}
	fmt.Println(shell.PanelEnd())
}

// sessionToggleLine renders the on/off switches as one line.
//
// Each used to own a row and say "DISABLED", so the default configuration
// produced four lines of nothing-is-happening. Only what is ON is named; when
// everything is at its default the line says so once.
func sessionToggleLine() string {
	var on []string
	if audio.IsEnabled() {
		on = append(on, "audio")
	}
	if agentCore != nil && agentCore.IsStealthEnabled() {
		on = append(on, "stealth")
	}
	if cfg.UserPrefs.TypewriteAll {
		on = append(on, "typewrite-all")
	}
	if utils.IsDebugMode() {
		on = append(on, "debug")
	}
	if len(on) == 0 {
		return shell.Muted("all at defaults")
	}
	return shell.Value(strings.Join(on, shell.Muted("  ·  ")))
}

// ragIndexLine summarises the MAN-page index.
func ragIndexLine() string {
	if ragSystem == nil {
		return shell.Badge(shell.StateWarn, "not initialized")
	}
	stats := ragSystem.GetSystemStats()
	pages, docs := stats["indexed_pages"], stats["total_documents"]
	state := shell.StateGood
	status := ragSystem.GetInitializationStatus()
	if !strings.EqualFold(status, "COMPLETED") {
		state = shell.StateWarn
	}
	return shell.Badge(state, strings.ToLower(status)) +
		shell.Muted(fmt.Sprintf("  %v MAN pages  ·  %v vector documents", pages, docs))
}

// knowledgeLine summarises the threat-intelligence corpus.
func knowledgeLine() string {
	if ragSystem == nil || ragSystem.GetDB() == nil {
		return shell.Badge(shell.StateWarn, "not initialized")
	}
	stats := ragSystem.GetSystemStats()
	last := rag.KnowledgeLastUpdate(ragSystem.GetDB())
	if last == "" {
		last = "never — auto-bootstraps in the background when online"
	}
	// One line, no embedded newline: KV wraps its value itself, and a raw "\n"
	// here would escape the gutter instead of hanging under the value column.
	return shell.Value(fmt.Sprintf("%v", stats["db_cves"])) +
		shell.Muted(fmt.Sprintf(" CVEs  ·  %v exploits  ·  %v KEV  ·  %v MITRE  ·  updated %s",
			stats["db_exploits"], stats["db_kev"], stats["db_mitre"], last))
}

func handleSandboxCommand(c cmdArgs) {
	if sandbox == nil {
		uiFail("sandbox", "is not available in this session")
		return
	}
	if c.Empty() {
		sandbox.PrintStatus()
		uiUsage("/sandbox <off|current|strict>")
		uiDetail("off — no directory confinement")
		uiDetail("current — confine to the current directory tree")
		uiDetail(fmt.Sprintf("strict — current-dir confinement plus kernel confinement (%s)",
			confinement.BackendName()))
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
		uiFail(mode, "is not a sandbox mode")
		uiUsage("/sandbox <off|current|strict>")
	}
}

func handleChangeDirectory(c cmdArgs) {
	targetDir := c.Rest
	if targetDir == "" {
		current, _ := os.Getwd()
		fmt.Println(shell.KV("DIRECTORY", shell.Value(current), shell.KVWidth("DIRECTORY")))
		return
	}
	if err := sandbox.ChangeDirectory(targetDir); err != nil {
		uiFail("cd", err.Error())
	}
}

func handleGitCommand(c cmdArgs) {
	request := c.Rest
	if request == "" {
		uiUsage("/git <natural-language git operation>")
		uiDetail("e.g. /git commit everything with a sensible message")
		uiDetail("e.g. /git show me what changed on this branch")
		return
	}
	if gitManager == nil {
		uiFail("git", "is not available in this session")
		return
	}
	if err := gitManager.HandleGitRequest(request); err != nil {
		uiFail("git", err.Error())
	}
}

// -------------------------------------------------------
// RAG STATUS / REINDEX / RESET / REBUILD
// -------------------------------------------------------

// handleRAGStatus reports the MAN-page index.
//
// Deliberately does NOT query the knowledge DB: that avoids hanging while the
// knowledge bootstrap or an update is running, which is the whole reason this
// is a separate command from /knowledge-status.
func handleRAGStatus() {
	fmt.Println(shell.PanelTitle("man page index"))
	if ragSystem == nil {
		fmt.Println(shell.PanelLine(shell.Badge(shell.StateBad, "not initialized")))
		fmt.Println(shell.PanelEnd())
		return
	}

	stats := ragSystem.GetRAGStats()
	statusText := ragSystem.GetInitializationStatus()
	initialized, _ := stats["initialized"].(bool)
	indexedPages := stats["indexed_pages"]

	w := shell.KVWidth("STATUS", "MAN PAGES", "VECTORS", "COMMANDS")

	state := shell.StateWarn
	detail := "indexing, or partially complete"
	switch {
	case initialized:
		state, detail = shell.StateGood, "ready for retrieval"
	case pagesIndexed(indexedPages) == 0:
		state, detail = shell.StateBad, "no MAN pages indexed yet"
	}
	fmt.Println(shell.KV("STATUS", shell.Badge(state, strings.ToLower(statusText))+
		shell.Muted("  "+detail), w))
	fmt.Println(shell.KV("MAN PAGES", shell.Value(fmt.Sprintf("%v", indexedPages)), w))

	if initialized {
		if totalDocs, ok := stats["total_documents"]; ok {
			fmt.Println(shell.KV("VECTORS", shell.Value(fmt.Sprintf("%v", totalDocs)), w))
		}
		if unique, ok := stats["unique_commands"]; ok {
			fmt.Println(shell.KV("COMMANDS", shell.Value(fmt.Sprintf("%v", unique))+
				shell.Muted("  distinct"), w))
		}
	}
	fmt.Println(shell.PanelEnd())
}

// pagesIndexed reads the page count out of the stats map, which is typed any.
func pagesIndexed(v any) int {
	if n, ok := v.(int); ok {
		return n
	}
	return 0
}

func handleRAGReindex() {
	if ragSystem == nil {
		uiFail("knowledge base", "not initialized")
		return
	}
	uiIdle("reindexing", "rebuilding the knowledge index")
	ctx, cancel := context.WithCancel(context.Background())
	unreg := utils.RegisterOperation(cancel)
	err := ragSystem.RebuildWithProgressCtx(ctx)
	unreg()
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			uiIdle("cancelled", "the index is unchanged")
			return
		}
		uiFail("reindex", err.Error())
		return
	}
	uiOK("reindexed", "the knowledge index is current")
}

func handleRAGReset() {
	if ragSystem == nil {
		uiFail("knowledge base", "not initialized")
		return
	}
	uiWarn("destructive", "this deletes every knowledge index and embedding cache on disk")
	if !commands.AskForConfirmation("Wipe all RAG vector data?") {
		uiIdle("cancelled", "nothing was deleted")
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		uiFail("home directory", err.Error())
		return
	}
	ragDir := filepath.Join(home, ".helix", "rag_index")
	if err := os.RemoveAll(ragDir); err != nil {
		uiFail("reset", err.Error())
		return
	}
	uiOK("deleted", ragDir)
	// The running system still holds the state it loaded at boot, so it will
	// keep answering retrievals from memory. Saying "reset completed" and
	// stopping there made that look like a working empty index.
	uiDetail("The running index still holds what it loaded at startup.")
	uiUsage("/rag-rebuild indexes again now  ·  /reboot restarts for a clean load")
}

func handleRAGRebuild() {
	if ragSystem == nil {
		uiFail("knowledge base", "not initialized")
		uiDetail("Start Helix normally first so the index is created, then run /rag-rebuild.")
		return
	}
	uiWarn("destructive", "this deletes every cached embedding and rebuilds every index")
	if !commands.AskForConfirmation("Proceed with full rebuild now?") {
		uiIdle("cancelled", "nothing was deleted")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	unreg := utils.RegisterOperation(cancel)
	err := ragSystem.RebuildWithProgressCtx(ctx)
	unreg()
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			uiIdle("cancelled", "the index is unchanged")
			return
		}
		uiFail("rebuild", err.Error())
		return
	}
	uiOK("rebuilt", "the new index is live")
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
		uiOK("dry-run", "on — commands are printed, never executed")
		uiDetail("For a whole session that only plans, /permissions plan is the stronger form.")
	} else {
		uiIdle("dry-run", "off — commands run normally")
	}
}

func checkOnlineStatus() {
	uiIdle("checking", "internet connectivity")
	if utils.IsOnline(3 * time.Second) {
		uiOK("online", "real-time capabilities available")
	} else {
		uiWarn("offline", "local providers only")
	}
}

func testBasicAI() {
	uiIdle("smoke test", ai.ActiveProviderName()+"  ·  "+ai.ActiveModel())
	think := ux.NewThinker("HELIX :: SMOKE TEST")
	think.Start()
	started := time.Now()
	resp, err := ai.RunModel("Reply with exactly: hello world")
	elapsed := time.Since(started)
	think.Stop()

	if err != nil {
		uiFail("failed", fmt.Sprintf("after %v: %v", elapsed.Round(time.Millisecond), err))
		uiUsage("/provider-status shows whether the brain is reachable at all")
		return
	}
	resp = strings.TrimSpace(resp)
	if resp == "" {
		// An empty 200 is a real and confusing failure mode on reasoning models
		// that burn their token budget before emitting anything.
		uiWarn("empty answer", fmt.Sprintf("the model replied in %v and returned nothing", elapsed.Round(time.Millisecond)))
		uiDetail("That usually means the token budget was consumed before any output.")
		return
	}
	uiOK("ok", fmt.Sprintf("%v  ·  %s", elapsed.Round(time.Millisecond), truncStr(resp, 120)))
}

func handleStealthCommand(c cmdArgs) {
	if c.Empty() {
		if agentCore != nil {
			uiToggle("STEALTH", agentCore.IsStealthEnabled(),
				"memory only — nothing is written to history", "history is written as usual", "")
		}
		uiUsage("/stealth <on|off>")
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
		uiFail("agent", "is not available in this session")
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
		uiOK("agentic", fmt.Sprintf("on — self-corrects across up to %d follow-ups", agenticStepBudget()))
		uiDetail("Every follow-up plan still passes the full safety pipeline.")
	case "off", "disable":
		agentCore.Agentic = false
		cfg.UserPrefs.AgenticMode = false
		_ = cfg.SavePreferences()
		uiIdle("agentic", "off — single-shot planning")
	default:
		uiToggle("AGENTIC", agentCore != nil && agentCore.Agentic,
			fmt.Sprintf("on — up to %d self-correcting follow-ups", agenticStepBudget()),
			"off — single-shot planning", "/agentic <on|off|steps <n>>")
	}
}

// setAgenticBudget retunes the harness step budget for this session and
// persists it, so a long multi-step task does not need a rebuild to get more
// iterations than the default.
func setAgenticBudget(raw string) {
	if strings.TrimSpace(raw) == "" {
		fmt.Println(shell.KV("STEP BUDGET", shell.Value(fmt.Sprint(agenticStepBudget())),
			shell.KVWidth("STEP BUDGET")))
		uiUsage(fmt.Sprintf("/agentic steps <1-%d>", maxAgenticStepBudget))
		return
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxAgenticStepBudget {
		uiFail("step budget", fmt.Sprintf("must be a whole number between 1 and %d", maxAgenticStepBudget))
		return
	}
	agentCore.MaxAgenticSteps = n
	cfg.UserPrefs.AgenticSteps = n
	_ = cfg.SavePreferences()
	uiOK("step budget", fmt.Sprint(n))
	if !agentCore.Agentic {
		uiDetail("The harness is currently off — /agentic on uses it.")
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
		uiFail("session memory", "is not available in this session")
		return
	}

	switch action {
	case "show", "list", "ls", "":
		turns := agentCore.Session.Recent(agentCore.Session.Len())
		fmt.Println(shell.PanelTitle("conversation memory"))
		if len(turns) == 0 {
			fmt.Println(shell.PanelLine(shell.Muted(
				"empty — nothing from this session is being replayed to the planner")))
			fmt.Println(shell.PanelEnd())
			return
		}
		// PanelWrap, not PanelLine: this sentence is 78 columns and PanelLine
		// does not wrap, so it escaped the frame.
		for _, l := range shell.PanelWrap(fmt.Sprintf(
			"%d of %d turns, oldest first — replayed to the planner as zero-authority data",
			len(turns), agentCore.Session.Capacity()), shell.Muted) {
			fmt.Println(l)
		}
		fmt.Println(shell.PanelGap())

		w := shell.KVWidth("00:00:00")
		for _, t := range turns {
			channel := t.Channel
			if channel == "" {
				channel = "text"
			}
			// The channel rides with the timestamp rather than taking a column:
			// it matters (a voice turn carries reduced authority) but it is one
			// word, and a column of "text" would be four rows of noise.
			fmt.Println(shell.KV(t.Timestamp.Format("15:04:05"),
				shell.Muted(channel+"  ")+shell.Value(truncStr(t.UserText, 90)), w))
			if t.Reply != "" {
				fmt.Println(shell.KV("", shell.Muted("↳ "+truncStr(t.Reply, 90)), w))
			}
		}
		fmt.Println(shell.PanelEnd())
	case "clear", "wipe", "reset":
		if !commands.AskForConfirmation("Clear all conversation memory?") {
			uiIdle("cancelled", "the conversation is unchanged")
			return
		}
		// Archive before wiping, exactly as /clear does: a transcript is cheap
		// to keep and impossible to get back.
		turns := agentCore.SessionTurns()
		archived, aerr := session.SaveSnapshot("memory-clear", turns)
		if aerr != nil {
			uiWarn("archive", "could not be written first: "+aerr.Error())
		}
		if err := agentCore.Session.Clear(); err != nil {
			uiFail("clear", err.Error())
			return
		}
		uiOK("cleared", fmt.Sprintf("%d turn(s)", len(turns)))
		if archived != "" {
			uiUsage("/resume " + archived + "   restores what was cleared")
		}
	default:
		uiUsage("/memory <show|clear>")
	}
}

// -------------------------------------------------------
// RECON
// -------------------------------------------------------
func handleQuickScan(c cmdArgs) {
	if c.Empty() {
		fmt.Println(shell.PanelTitle("scan"))
		fmt.Println(shell.Step(shell.StateWarn, "authorization required",
			"written scope is the record that this was a permitted engagement, "+
				"and Helix will not scan without it"))
		fmt.Println(shell.PanelGap())
		for _, l := range shell.Table([]string{"command", "does"}, [][]string{
			{shell.Value("/scan authorize <target> --reason \"<scope>\""), shell.Muted("record authorization")},
			{shell.Value("/scan status"), shell.Muted("list authorized targets")},
			{shell.Value("/scan revoke <target>"), shell.Muted("withdraw authorization")},
			{shell.Value("/scan <target>"), shell.Muted("scan an authorized target")},
		}) {
			fmt.Println(l)
		}
		fmt.Println(shell.PanelEnd())
		return
	}
	if agentCore == nil {
		uiFail("agent", "not initialized")
		return
	}
	switch c.Sub() {
	case "authorize":
		if c.Count() < 2 {
			uiUsage("/scan authorize <target> --reason \"<written scope>\"")
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
			uiUsage("/scan revoke <target>")
			return
		}
		if agentCore.RevokeRecon(c.Arg(1)) {
			uiOK("withdrawn", c.Arg(1)+" is no longer authorized")
		} else {
			uiIdle(c.Arg(1), "was not authorized")
		}
	case "status":
		targets := agentCore.ListAuthorizedReconTargets()
		if len(targets) == 0 {
			uiIdle("no targets", "nothing is authorized for reconnaissance")
			return
		}
		fmt.Println(shell.PanelTitle("authorized targets"))
		for target, reason := range targets {
			fmt.Println(shell.KV(target, shell.Muted(reason), shell.KVWidth(target)))
		}
	default:
		target := c.Arg(0)
		if !agentCore.IsReconTargetAuthorized(target) {
			uiFail(target, "is not authorized for reconnaissance")
			uiUsage("/scan authorize " + target + " --reason \"<written scope>\"")
			return
		}

		toolName := "nmap"

		// Phase 15 Fix: Show reasoning progress bar during the scan
		think := ux.NewThinker("HELIX :: SCANNING")
		think.Start()
		result, err := agentCore.RunReconTool(toolName, "-sV", target)
		think.Stop()

		if err != nil {
			uiFail("recon", err.Error())
			return
		}

		// Auto-install missing recon tools
		if result.Error != nil && strings.Contains(strings.ToLower(result.Error.Error()), "not found") {
			uiWarn(toolName, "is not installed")
			if commands.AskForConfirmation(fmt.Sprintf("Install %s now using system package manager?", toolName)) {
				if installErr := agentCore.InstallTool(toolName); installErr != nil {
					uiFail("install", installErr.Error())
					return
				}
				uiOK(toolName, "installed — retrying the scan")

				think2 := ux.NewThinker("HELIX :: SCANNING")
				think2.Start()
				result, err = agentCore.RunReconTool(toolName, "-sV", target)
				think2.Stop()

				if err != nil {
					uiFail("recon retry", err.Error())
					return
				}
			} else {
				uiIdle("skipped", "nothing was scanned")
				return
			}
		}

		if result.Error != nil {
			uiFail("recon tool", result.Error.Error())
		}
		uiOK("complete", result.Elapsed.String())
		if result.Raw != "" {
			fmt.Println(result.Raw)
		} else if len(result.Parsed) > 0 {
			summary, _ := json.MarshalIndent(result.Parsed, "", "  ")
			fmt.Println(shell.PanelSection("results"))
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
		uiUsage("/explain <command or technique description>")
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
		uiFail("the model", err.Error())
		uiUsage("/provider-status shows whether the brain is reachable")
		return
	}
	cleaned := cleanDebrief(strings.TrimSpace(resp))
	if cleaned == "" {
		uiWarn("empty answer", "the model returned nothing")
		uiDetail("Try rephrasing, or check /provider-status.")
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
		uiFail("models", err.Error())
		return
	}
	fmt.Println(shell.PanelTitle("models"))
	for i, model := range models {
		if i >= 50 {
			fmt.Println(shell.PanelLine(shell.Muted(fmt.Sprintf("… and %d more", len(models)-50))))
			break
		}
		fmt.Println(shell.PanelLine(shell.Value(model.ID)))
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
		text = strings.Replace(text, h, shell.Fg(shell.HexSecondary, h), 1)
	}
	return text
}

// -------------------------------------------------------
// KNOWLEDGE BASE
// -------------------------------------------------------

func handleKnowledgeUpdate() {
	if ragSystem == nil || ragSystem.GetDB() == nil {
		uiFail("knowledge database", "not available")
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
			uiIdle("cancelled", "the knowledge base is unchanged")
			return
		}
		if errors.Is(err, rag.ErrOffline) {
			uiWarn("offline", "the knowledge update needs internet connectivity")
			return
		}
		uiFail("update", err.Error())
		return
	}
	uiOK("updated", "the knowledge base is current")
}

// handleKnowledgeStats reports the threat-intelligence corpus.
func handleKnowledgeStats() {
	fmt.Println(shell.PanelTitle("threat intelligence"))
	if ragSystem == nil || ragSystem.GetDB() == nil {
		fmt.Println(shell.PanelLine(shell.Badge(shell.StateBad, "database unavailable")))
		fmt.Println(shell.PanelEnd())
		return
	}

	stats := ragSystem.GetSystemStats()
	w := shell.KVWidth("CVES", "EXPLOITS", "KEV (CISA)", "MITRE", "LAST UPDATE")

	for _, row := range []struct{ label, key string }{
		{"CVES", "db_cves"},
		{"EXPLOITS", "db_exploits"},
		{"KEV (CISA)", "db_kev"},
		{"MITRE", "db_mitre"},
	} {
		if v, ok := stats[row.key]; ok {
			fmt.Println(shell.KV(row.label, shell.Value(fmt.Sprintf("%v", v)), w))
		}
	}

	if last := rag.KnowledgeLastUpdate(ragSystem.GetDB()); last != "" {
		fmt.Println(shell.KV("LAST UPDATE", shell.Value(last), w))
	} else {
		fmt.Println(shell.KV("LAST UPDATE", shell.Badge(shell.StateIdle, "never")+
			shell.Muted("  auto-bootstraps in the background when online"), w))
	}
	fmt.Println(shell.PanelEnd())
}

func handleVulnCommand(c cmdArgs) {
	query := strings.Trim(c.Rest, `"'`)
	if query == "" {
		uiUsage("/vuln <CVE-ID|EDB-ID|MITRE-T-ID|search query>")
		return
	}
	if ragSystem == nil || ragSystem.GetDB() == nil {
		uiFail("knowledge database", "not available")
		return
	}
	db := ragSystem.GetDB()

	if strings.HasPrefix(strings.ToUpper(query), "CVE-") {
		exact, err := rag.LookupVulnByID(db, query)
		if err == nil && len(exact) > 0 {
			displayVulnEntries(exact)
			return
		}

		uiWarn("not in the local database", strings.ToUpper(query)+
			" is outside the rolling 119-day window — fetching from NVD")

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
			uiFail("fetch", fetchErr.Error())
		}

		uiUsage("/knowledge-update syncs the full NVD data")
	} else {
		exact, err := rag.LookupVulnByID(db, query)
		if err == nil && len(exact) > 0 {
			displayVulnEntries(exact)
			return
		}
	}

	entries, err := rag.SearchVulns(db, query, 5)
	if err != nil {
		uiFail("search", err.Error())
		return
	}
	if len(entries) == 0 {
		uiIdle("no matches", "nothing in the intelligence set matches that")
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

// displayVulnEntries renders threat intelligence as one panel per finding.
//
// It was a bold-cyan "ID: … Source: … Title: …" stack in which the CVSS score
// and the CISA KEV flag — the two fields that decide whether you stop what you
// are doing — carried exactly the weight of the source name. Severity is a
// badge now, so the screen can be triaged by colour before it is read.
func displayVulnEntries(entries []rag.VulnIntel) {
	fmt.Println(shell.PanelTitle("vulnerability intelligence"))
	w := shell.KVWidth("DESCRIPTION", "PATCH", "DETECTION", "SOURCE", "SEVERITY")

	for i, e := range entries {
		if i > 0 {
			fmt.Println(shell.PanelGap())
		}
		fmt.Println(shell.KV(e.ID, shell.Value(e.Title), w))
		fmt.Println(shell.KV("SOURCE", shell.Muted(e.SourceType), w))

		if e.CVSS > 0 || e.KEV {
			fmt.Println(shell.KV("SEVERITY", vulnSeverityLine(e), w))
		}
		if e.Description != "" {
			fmt.Println(shell.KV("DESCRIPTION", shell.Muted(e.Description), w))
		}
		if e.Detection != "" {
			fmt.Println(shell.KV("DETECTION", shell.Muted(e.Detection), w))
		}
		if e.PatchGuidance != "" {
			fmt.Println(shell.KV("PATCH", shell.Muted(e.PatchGuidance), w))
		}
	}
	fmt.Println(shell.PanelEnd())
	uiDetail("Defensive use only: prioritise patching and detection.")
}

// vulnSeverityLine renders CVSS and the KEV flag as one triage cell.
//
// KEV outranks the score, deliberately: "known exploited in the wild" is a
// statement about reality and a CVSS number is a statement about theory, so a
// KEV entry is red whatever it scores.
func vulnSeverityLine(e rag.VulnIntel) string {
	state := shell.StateIdle
	switch {
	case e.KEV || e.CVSS >= 9:
		state = shell.StateBad
	case e.CVSS >= 7:
		state = shell.StateWarn
	case e.CVSS > 0:
		state = shell.StateGood
	}

	label := "unscored"
	if e.CVSS > 0 {
		label = fmt.Sprintf("CVSS %.1f", e.CVSS)
	}
	line := shell.Badge(state, label)
	if e.KEV {
		line += shell.Muted("  ·  ") + shell.Fg(shell.HexRectifier, "CISA KEV")
		if e.KEVAction != "" {
			line += shell.Muted("  " + e.KEVAction)
		}
	}
	return line
}

func handleKnowledgeReindex() {
	if ragSystem == nil || ragSystem.GetDB() == nil {
		uiFail("knowledge database", "not available")
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
		uiFail("reindex", err.Error())
		return
	}
	count, cerr := rag.FTSCount(db)
	if cerr != nil {
		uiWarn("reindexed", "but the row count could not be read: "+cerr.Error())
		return
	}
	uiOK("reindexed", fmt.Sprintf("%d rows", count))
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

// printEdgeSection renders the appliance diagnostics as its own panel (P10.3).
//
// It exists because the two Linux edge gotchas fail SILENTLY: a CGO-free build
// is structurally mute however the TTS provider is configured, and kernel
// confinement degrades to none on an old kernel without stopping anything. On a
// headless board those are invisible until something important does not happen,
// so /doctor states them outright, with the fix attached.
//
// It used to print between two panels as a flat green stack starting at column
// zero — twelve lines of equal weight, framed by nothing, with a `--- Edge
// appliance ---` heading in the middle of a screen where every other heading is
// a chip. Panelling it also removed a duplication that the flat layout hid: the
// confinement backend is already a row on the DOCTOR panel directly above, so
// it is repeated here ONLY when there is a caveat attached to it.
func printEdgeSection() {
	rep := edge.Collect()

	fmt.Println(shell.PanelTitle("appliance"))
	w := shell.KVWidth("PLATFORM", "AUDIO", "CONFINEMENT", "RECORDER", "THERMALS")

	platform := shell.Value(rep.OS + "/" + rep.Arch)
	if rep.Board != "" {
		platform += shell.Muted("  ·  " + rep.Board)
	}
	fmt.Println(shell.KV("PLATFORM", platform, w))

	// Build flavor — the audio_cgo gotcha (docs/edge_deployment.md §3.1).
	if rep.SpeechSupported {
		fmt.Println(shell.KV("AUDIO", shell.Badge(shell.StateGood, rep.AudioBackend), w))
	} else {
		fmt.Println(shell.KV("AUDIO", shell.Badge(shell.StateWarn, rep.AudioBackend)+
			shell.Muted("  no config change will produce sound — this needs a rebuild"), w))
		fmt.Println(shell.StepCommand("sudo apt install -y libasound2-dev && " +
			"CGO_ENABLED=1 go build -tags audio_cgo ./cmd/helix"))
	}

	// Only when it is not simply the backend name the DOCTOR panel already
	// carried. A caveat is news; repeating a row verbatim two lines later is not.
	if rep.Note != "" {
		fmt.Println(shell.KV("CONFINEMENT", shell.Badge(shell.StateWarn, rep.Confinement), w))
		for _, l := range shell.StepDetail(rep.Note, shell.Muted) {
			fmt.Println(l)
		}
	}

	// Microphone capture (CGO-free; sox/ffmpeg shell-out per ADR-003).
	if rec, err := speech.DetectRecorder(); err == nil {
		fmt.Println(shell.KV("RECORDER", shell.Badge(shell.StateGood, rec), w))
	} else {
		fmt.Println(shell.KV("RECORDER", shell.Badge(shell.StateWarn, "none found")+
			shell.Muted("  install sox for voice input"), w))
		fmt.Println(shell.StepCommand("sudo apt install -y sox"))
	}

	printEdgeThermals(rep, w)

	// Endpoint collisions BEFORE the reachability probes below: a probe that
	// reports "reachable" on a port owned by a different service is the most
	// misleading line /doctor can print, and this explains it.
	reportEndpointConflicts()

	printEdgeSidecars()
	fmt.Println(shell.PanelEnd())
}

// printEdgeSidecars probes the local services this device is configured to
// depend on. Only LOCAL providers are listed: a cloud endpoint being reachable
// is a network fact, already covered by the Network line above.
//
// It distinguishes a sidecar that is DOWN from one that is merely STANDBY, which
// it did not before — and speech.ProviderStatusRow's own doc comment says why
// that matters: "Out-of-chain providers are not probed, so their Healthy=false
// means standby, not down." This printer ignored InChain and rendered every one
// of them as a yellow "unreachable", so a perfectly healthy machine that had
// simply not chosen csm-local or kokoro-local got two warnings about services it
// was never using. A warning that fires on the normal case is a warning nobody
// reads.
func printEdgeSidecars() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	report := speech.Status(ctx)
	var rows []speech.ProviderStatusRow
	for _, group := range [][]speech.ProviderStatusRow{report.STTStatus, report.TTSStatus} {
		for _, row := range group {
			if row.Local {
				rows = append(rows, row)
			}
		}
	}
	if len(rows) == 0 {
		return
	}

	fmt.Println(shell.PanelGap())
	fmt.Println(shell.PanelSection("local sidecars"))

	labels := make([]string, 0, len(rows))
	for _, r := range rows {
		labels = append(labels, r.Name)
	}
	w := shell.KVWidth(labels...)
	for _, r := range rows {
		switch {
		case r.Healthy:
			fmt.Println(shell.KV(r.Name, shell.Badge(shell.StateGood, "reachable"), w))
		case !r.InChain:
			fmt.Println(shell.KV(r.Name, shell.Badge(shell.StateIdle, "standby")+
				shell.Muted("  not in the active chain"), w))
		default:
			fmt.Println(shell.KV(r.Name, shell.Badge(shell.StateWarn, "unreachable")+
				shell.Muted("  "+r.HealthDetail), w))
		}
	}

	printOfflineBrain(ctx, w)
}

// printOfflineBrain reports the local model that answers when the cloud does not.
//
// P11.2/P11.3: configured but unreachable or unpulled is the failure that only
// shows up during an outage, when it is too late to discover it.
func printOfflineBrain(ctx context.Context, w int) {
	cfg, err := config.DefaultConfig()
	if err != nil {
		return
	}
	fb := cfg.LLM.Fallback
	if !fb.FallbackEnabled() {
		fmt.Println(shell.KV("offline llm", shell.Badge(shell.StateIdle, "disabled"), w))
		return
	}
	provider := fb.Provider
	if provider == "" {
		provider = config.LLMDefaults().Fallback.Provider
	}

	if provider != "ollama" {
		// llama.cpp is a user-managed sidecar with no pull API; reachability is
		// the only thing Helix can honestly assert.
		p, gerr := ai.GetProviderByName(provider)
		if gerr != nil {
			return
		}
		if herr := p.HealthCheck(ctx); herr == nil {
			fmt.Println(shell.KV("offline llm", shell.Badge(shell.StateGood, "reachable")+
				shell.Muted("  "+provider), w))
		} else {
			fmt.Println(shell.KV("offline llm", shell.Badge(shell.StateWarn, "unreachable")+
				shell.Muted("  "+provider+"  ·  "+herr.Error()), w))
		}
		return
	}

	client := ollama.NewClient()
	if herr := client.Health(ctx); herr != nil {
		fmt.Println(shell.KV("offline llm", shell.Badge(shell.StateWarn, "unreachable")+
			shell.Muted("  ollama  ·  "+herr.Error()), w))
		return
	}
	// Deliberately NOT falling back to ai.ActiveModel(): that is the CLOUD
	// model, and borrowing its name produced the advice
	// "run `ollama pull deepseek-v4-flash-vision-exp`" — a model id that
	// exists in no Ollama registry, so the one actionable line in the
	// offline-brain check was a command guaranteed to fail. An unset
	// fallback model is unset; say that instead of inventing one.
	if fb.Model == "" {
		fmt.Println(shell.KV("offline llm", shell.Badge(shell.StateWarn, "no model set")+
			shell.Muted("  ollama is running, but nothing will answer offline"), w))
		suggestOllamaModel(ctx, client)
		return
	}
	if ollamaHasModel(ctx, client, fb.Model) {
		fmt.Println(shell.KV("offline llm", shell.Badge(shell.StateGood, "ready")+
			shell.Muted("  ollama  ·  "+fb.Model), w))
		return
	}
	fmt.Println(shell.KV("offline llm", shell.Badge(shell.StateWarn, "not pulled")+
		shell.Muted("  "+fb.Model+" is configured but absent"), w))
	fmt.Println(shell.StepCommand("ollama pull " + fb.Model))
	suggestOllamaModel(ctx, client)
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
		fmt.Println(shell.StepCommand("ollama pull llama3.2"))
		fmt.Println(shell.StepCommand("/config fallback-model llama3.2"))
		return
	}
	names := make([]string, 0, len(installed))
	for _, m := range installed {
		names = append(names, m.ID)
		if len(names) == 4 {
			break
		}
	}
	for _, l := range shell.StepDetail("this Ollama has: "+strings.Join(names, ", "), shell.Muted) {
		fmt.Println(l)
	}
	fmt.Println(shell.StepCommand("/config fallback-model " + names[0]))
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
func printEdgeThermals(rep edge.Report, w int) {
	if rep.ThermalC <= 0 {
		if rep.ThermalErr != "" {
			// Not a warning: "sensors are Linux-only" is the expected answer on
			// a Mac, and colouring it like a problem is why the flat version had
			// three shades of nothing-is-wrong.
			fmt.Println(shell.KV("THERMALS", shell.Badge(shell.StateIdle, rep.ThermalErr), w))
		}
		return
	}
	reading := fmt.Sprintf("%.1f°C", rep.ThermalC)
	state := shell.StateGood
	if rep.ThermalC >= 80 {
		state = shell.StateWarn
	}
	value := shell.Badge(state, reading) + shell.Muted("  "+edge.ThermalVerdict(rep.ThermalC))
	if rep.Throttled {
		value += shell.Muted("  ·  firmware is capping sustained load")
	}
	fmt.Println(shell.KV("THERMALS", value, w))
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
		shell.KVWidth("REACHABILITY", "ANSWERING")))
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
		displayProviderStatus()
	case "use":
		if c.Count() < 2 {
			uiFail("/provider use", "needs a provider name")
			uiUsage("/provider use <name>", "registered: "+strings.Join(ai.ListProviders(), ", "))
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
		uiFail(c.Arg(0), "is not a provider or a /provider subcommand")
		uiUsage("/provider [status|list|use <name>|<name>]",
			"registered: "+strings.Join(ai.ListProviders(), ", "))
	}
}

// switchProvider activates a provider and persists the choice.
func switchProvider(name string) {
	name = strings.ToLower(strings.TrimSpace(name))
	if err := useProviderInteractive(name); err != nil {
		uiFail("provider", err.Error())
		return
	}
	cfg.Provider = name
	cfg.ProviderModel = ai.ActiveModel()
	_ = cfg.SavePreferences()
	uiOK(name, "is answering  ·  "+ai.ActiveModel())
}

func handleModelCommand(c cmdArgs) {
	if c.Empty() {
		fmt.Println(shell.KV("MODEL", shell.Value(ai.ActiveModel())+
			shell.Muted("  on "+ai.ActiveProviderName()), shell.KVWidth("MODEL")))
		uiUsage("/model [list|use <model-id>|<model-id>]")
		return
	}
	switch c.Sub() {
	case "list", "ls":
		listAvailableModels()
	case "use":
		if c.Count() < 2 {
			uiUsage("/model use <model-id>")
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
		uiUsage("/model use <model-id>")
		return
	}
	if err := useModelInteractive(ai.ActiveProviderName(), model); err != nil {
		uiFail("model", err.Error())
		return
	}
	cfg.ProviderModel = ai.ActiveModel()
	_ = cfg.SavePreferences()
	uiOK(ai.ActiveModel(), "is the active model")
}

// displayProviderStatus lists every registered provider and its key state.
//
// It reads ProviderStatusRows rather than ProviderStatus, which returns
// pre-formatted " - " separated strings — the same re-parse-your-own-output
// mistake internal/metrics exists to avoid, and the reason this screen was the
// last flat `=== Provider Status ===` stack in the shell. Twelve identically
// cyan lines meant the one fact a reader opens it for, which provider is
// answering and whether it has a key, had to be hunted for.
func displayProviderStatus() {
	rows := ai.ProviderStatusRows()
	active := ai.ActiveProviderName()

	fmt.Println(shell.PanelTitle("providers"))
	if len(rows) == 0 {
		fmt.Println(shell.PanelLine(shell.Muted("no providers registered")))
		fmt.Println(shell.PanelEnd())
		return
	}

	// A self-fitting table rather than KV rows: three facts per provider across
	// twelve providers is a grid, and hanging the display name off the end of a
	// KV value made every row a different length with the state buried in the
	// middle of it.
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		var state string
		switch {
		case r.KeyState == "configured":
			state = shell.Badge(shell.StateGood, "key saved")
		case r.Local:
			state = shell.Badge(shell.StateIdle, "local")
		default:
			state = shell.Badge(shell.StateIdle, "no key")
		}
		name := shell.Muted(r.Display)
		if r.Active {
			// The one row a reader is looking for, so it is the one row that
			// carries the heading colour.
			name = shell.Fg(shell.HexSecondary, r.Display+"  ← active")
		}
		table = append(table, []string{shell.Value(r.Name), state, name})
	}
	for _, l := range shell.Table([]string{"provider", "key", "name"}, table) {
		fmt.Println(l)
	}

	fmt.Println(shell.PanelGap())
	if active == "" {
		fmt.Println(shell.Step(shell.StateWarn, "no active provider", "/setup picks one"))
		fmt.Println(shell.PanelEnd())
		return
	}
	brain := shell.Value(active)
	if m := ai.ActiveModel(); m != "" {
		brain += shell.Muted("  ·  " + m)
	}
	fmt.Println(shell.KV("ANSWERING", brain, shell.KVWidth("REACHABILITY", "ANSWERING")))
	// /provider status and /provider-status are separate handlers; both must
	// answer "can the selected brain actually be reached?", or which one the user
	// happened to type decides whether they find out.
	printActiveProviderHealth()
	fmt.Println(shell.PanelEnd())
}

func handleAudioCommand(c cmdArgs) {
	if c.Empty() {
		// The engine's readiness is a SECOND fact, not a parenthetical: audio
		// can be switched on and still produce nothing (SSH, a container, a
		// headless box), and "ON (NOT READY)" put the contradiction in brackets.
		on := audio.IsEnabled()
		detail := "tonal feedback plays"
		if on && !audio.IsReady() {
			detail = "switched on, but the sound engine is not available here"
		}
		uiToggle("AUDIO", on, detail, "no tonal feedback", "/audio <on|off>")
		return
	}
	switch c.Sub() {
	case "on", "enable":
		audio.SetEnabled(true)
		if err := audio.EnsureReady(true); err != nil {
			uiWarn("audio", "on, but the sound engine is unavailable: "+err.Error())
			uiDetail("Check the system output device and volume. SSH, Docker and " +
				"headless sessions have no local speaker.")
			return
		}
		uiOK("audio", "on — tonal feedback plays")
		audio.PlayAlert()
		time.Sleep(100 * time.Millisecond)
	case "off", "disable":
		audio.SetEnabled(false)
		uiIdle("audio", "off — no tonal feedback")
	default:
		uiFail(c.Arg(0), "is not an /audio setting")
		uiUsage("/audio <on|off>")
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
			uiOK("none pending", "telemetry-free — nothing left the machine")
			return
		}
		fmt.Println(shell.PanelTitle("crash reports"))
		for i, s := range summaries {
			fmt.Println(shell.KV(fmt.Sprintf("%d", i+1),
				shell.Value(s.Time)+shell.Muted("  "+s.Reason), 3))
			fmt.Println(shell.PanelLine("    " + shell.Muted(s.Path)))
		}
		fmt.Println()
		fmt.Println(shell.PanelEnd())
		uiUsage("/crash view <n>   the redacted stack trace")
		uiUsage("/crash clear      delete them, keeping config, keys and history")

	case "view", "show", "cat", "read":
		if c.Count() < 2 {
			uiUsage("/crash view <number>")
			return
		}
		summaries := diagnostics.ListReports()
		if len(summaries) == 0 {
			uiIdle("none pending", "there is nothing to view")
			return
		}

		idx, err := strconv.Atoi(c.Arg(1))
		if err != nil || idx < 1 || idx > len(summaries) {
			uiFail("no such report", fmt.Sprintf("valid numbers are 1-%d  ·  /crash list", len(summaries)))
			return
		}

		target := summaries[idx-1]
		data, err := os.ReadFile(target.Path)
		if err != nil {
			uiFail("read", err.Error())
			return
		}

		fmt.Println()
		fmt.Println(shell.PanelTitle("crash " + filepath.Base(target.Path)))
		var prettyJSON bytes.Buffer
		if json.Indent(&prettyJSON, data, "", "  ") == nil {
			fmt.Println(prettyJSON.String())
		} else {
			fmt.Println(string(data))
		}
		fmt.Println()
		fmt.Println(shell.PanelEnd())
		uiDetail("Every API key, token and secret is [REDACTED] before it is written.")

	case "clear", "clean", "rm", "delete":
		n, err := diagnostics.PurgeReports()
		if err != nil {
			uiFail("clear", err.Error())
			return
		}
		if n == 0 {
			uiIdle("none pending", "there is nothing to clear")
		} else {
			uiOK("cleared", fmt.Sprintf("%d report(s) — config, keys and history are intact", n))
		}

	default:
		uiUsage("/crash <list|view <number>|clear>")
	}
}

// -------------------------------------------------------
// /typewrite-all — Global Typewriter Effect Toggle
// -------------------------------------------------------
func handleTypewriteAllCommand(c cmdArgs) {
	if c.Empty() {
		uiToggle("TYPEWRITE-ALL", cfg.UserPrefs.TypewriteAll,
			"every line is animated", "only AI replies are animated",
			"/typewrite-all <on|off>")
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
		uiOK("typewrite-all", "on — every line is animated")
	case "off", "disable":
		cfg.UserPrefs.TypewriteAll = false
		if gui != nil {
			gui.SetTypewriteAll(false)
		}
		uiIdle("typewrite-all", "off — only AI replies are animated")
	default:
		uiFail(c.Arg(0), "is not a /typewrite-all setting")
		uiUsage("/typewrite-all <on|off>")
		return
	}
	_ = cfg.SavePreferences()
}
