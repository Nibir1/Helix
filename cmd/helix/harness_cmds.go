// cmd/helix/harness_cmds.go
// Purpose: the commands that make the agentic harness inspectable and
// steerable — /plan, /permissions, /todo, /tools, /hooks, /undo, /config,
// /version.
//
// The theme: a harness you cannot see is a harness you cannot trust. Each
// command here answers one question the shell previously left to guesswork —
// what would this do, how much am I allowed to run without being asked, what
// work is outstanding, what tools exist, what local policy is in force.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"helix/internal/agent"
	"helix/internal/ai"
	"helix/internal/audio"
	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/confinement"
	"helix/internal/edge"
	"helix/internal/hooks"
	"helix/internal/session"
	"helix/internal/shell"
	"helix/internal/speech"
	"helix/internal/utils"

	"github.com/fatih/color"
)

// todoList is the process-wide task list, shared with the agent so the planner
// sees the same tasks the user edits.
var todoList *session.TodoList

// requireAgent reports whether the agent is available, explaining the situation
// when it is not.
//
// A session whose agent failed to build still reaches the prompt (by design —
// /doctor and /purge must stay usable), so every command that needs the agent
// has to say so rather than dereferencing nil.
func requireAgent() bool {
	if agentCore != nil {
		return true
	}
	color.Red("This command needs the agent, which is not available in this session.")
	color.Yellow("Run /doctor to see what failed to start.")
	return false
}

// -------------------------------------------------------
// /plan
// -------------------------------------------------------

func handlePlanCommand(c cmdArgs) {
	if !requireAgent() {
		return
	}
	if c.Empty() {
		color.Red("Usage: /plan <request>")
		color.Yellow("Shows the steps a request would run, without executing any of them.")
		return
	}

	plan, err := agentCore.PlanPreview(c.Rest)
	if err != nil {
		color.Red("Planning failed: %v", err)
		return
	}
	if plan == nil || len(plan.Steps) == 0 {
		color.Yellow("The planner produced no steps for that request.")
		return
	}

	fmt.Println()
	color.Cyan("=== Plan (nothing executed) ===")
	color.Cyan("Intent: %s · %d step(s)", plan.Intent, len(plan.Steps))
	for i, step := range plan.Steps {
		fmt.Printf("  %s %s\n",
			shell.Fg(shell.HexTertiary, fmt.Sprintf("%d.", i+1)),
			shell.Fg(shell.HexSecondary, step.Tool+describeStepAction(step)))
		if step.Command != "" {
			fmt.Printf("     %s %s\n", shell.Fg(shell.HexMuted, "$"),
				shell.Fg(shell.HexText, step.Command))
			risk, reasons := commands.AnalyzeShellRisk(step.Command)
			fmt.Printf("     %s\n", shell.Fg(shell.HexMuted, "risk: "+riskLabel(risk)))
			for _, r := range reasons {
				fmt.Printf("       %s\n", shell.Fg(shell.HexMuted, "• "+r))
			}
		}
		if step.Message != "" {
			fmt.Printf("     %s\n", shell.Fg(shell.HexText, truncStr(step.Message, 160)))
		}
		for k, v := range step.Args {
			fmt.Printf("     %s\n", shell.Fg(shell.HexMuted, fmt.Sprintf("%s = %s", k, truncStr(v, 120))))
		}
	}
	fmt.Println()
	color.Yellow("This is a preview. Retype the request without /plan to run it,")
	color.Yellow("or use /permissions plan to keep the whole session read-only.")
}

func describeStepAction(step ai.PlanStep) string {
	if step.Action == "" {
		return ""
	}
	return " · " + step.Action
}

// riskLabel renders a risk tier with what the tier means for approval.
func riskLabel(r commands.ShellRiskLevel) string {
	switch r {
	case commands.ShellRiskHigh:
		return "HIGH — blocked, cannot be confirmed"
	case commands.ShellRiskMedium:
		return "medium — asks before running"
	default:
		return "low — runs without asking"
	}
}

// -------------------------------------------------------
// /permissions
// -------------------------------------------------------

func handlePermissionsCommand(c cmdArgs) {
	if !requireAgent() {
		return
	}
	current := agentCore.Permission()

	if c.Empty() {
		fmt.Println()
		color.Cyan("⚡ APPROVAL POSTURE")
		for _, m := range agent.PermissionModes() {
			marker := "  "
			label := fmt.Sprintf("%-10s", string(m))
			paint := shell.Fg(shell.HexText, label)
			if m == current {
				marker = shell.Fg(shell.HexSecondary, "▸ ")
				paint = shell.Fg(shell.HexPrimary, label)
			}
			fmt.Printf("  %s%s %s\n", marker, paint, shell.Fg(shell.HexMuted, m.Describe()))
		}
		fmt.Println()
		color.Yellow("The mode layers on top of the risk tiers; it never replaces them.")
		color.Yellow("High-risk commands stay blocked in every mode, typed confirmations stay")
		color.Yellow("typed, the sandbox still validates, and voice input stays capped.")
		color.Cyan("Change it with /permissions <%s>", permissionModeList())
		fmt.Println()
		return
	}

	// Voice may READ the posture but never change it.
	//
	// This is the same reasoning as the typed-confirmation deny list (ADR-005):
	// a transcript arrives with user authority and no proof of who spoke, and the
	// posture decides how much runs WITHOUT being asked. A misheard phrase must
	// not be able to widen that.
	if voiceModeActive {
		color.Yellow("Changing the approval posture by voice is refused.")
		color.Yellow("Current mode: %s — %s", current, current.Describe())
		color.Cyan("Type /permissions %s in the terminal to change it.", strings.ToLower(c.Rest))
		return
	}

	mode, ok := agent.ParsePermissionMode(c.Rest)
	if !ok {
		color.Red("Unknown mode: %s", c.Rest)
		color.Yellow("Valid modes: %s", permissionModeList())
		return
	}
	if mode == current {
		color.Cyan("Already in %s mode — %s", mode, mode.Describe())
		return
	}

	// Loosening the posture is a decision worth stating out loud; tightening it
	// never needs a confirmation.
	if mode == agent.PermissionAuto {
		color.Yellow("auto mode answers the medium-risk prompt for you.")
		color.Yellow("High risk is still blocked and typed confirmations are still typed,")
		color.Yellow("but commands that would have asked will now simply run.")
		if !commands.AskForConfirmation("Switch to auto?") {
			color.Yellow("Left in %s mode.", current)
			return
		}
	}

	if !agentCore.SetPermission(mode) {
		color.Red("Could not apply mode %s.", mode)
		return
	}
	cfg.UserPrefs.Permission = string(mode)
	_ = cfg.SavePreferences()
	color.Green("Permission mode: %s — %s", mode, mode.Describe())
}

// -------------------------------------------------------
// /todo
// -------------------------------------------------------

func handleTodoCommand(c cmdArgs) {
	if todoList == nil {
		color.Red("Task list is not available in this session.")
		return
	}

	switch c.Sub() {
	case "", "list", "ls":
		printTodoList()

	case "add", "new", "+":
		text := c.From(1)
		item, err := todoList.Add(text)
		if err != nil {
			color.Red("%v", err)
			color.Yellow("Usage: /todo add <what needs doing>")
			return
		}
		color.Green("Added task %d: %s", item.ID, item.Text)

	case "done", "complete", "x":
		setTodoState(c, session.TodoDone, "Completed")
	case "start", "doing", "wip":
		setTodoState(c, session.TodoInProgress, "In progress")
	case "block", "blocked":
		setTodoState(c, session.TodoBlocked, "Blocked")
	case "open", "reopen", "pending":
		setTodoState(c, session.TodoPending, "Reopened")

	case "rm", "remove", "del", "delete":
		id, ok := todoID(c.Arg(1))
		if !ok {
			return
		}
		if err := todoList.Remove(id); err != nil {
			color.Red("%v", err)
			return
		}
		color.Green("Removed task %d.", id)

	case "prune":
		n, err := todoList.PruneDone()
		if err != nil {
			color.Red("%v", err)
			return
		}
		if n == 0 {
			color.Cyan("No completed tasks to prune.")
			return
		}
		color.Green("Pruned %d completed task(s).", n)

	case "clear", "reset":
		if len(todoList.Items()) == 0 {
			color.Cyan("Task list is already empty.")
			return
		}
		if !commands.AskForConfirmation("Delete every task, including unfinished ones?") {
			color.Yellow("Clear cancelled.")
			return
		}
		if err := todoList.Clear(); err != nil {
			color.Red("%v", err)
			return
		}
		color.Green("Task list cleared.")

	default:
		color.Red("Unknown subcommand: %s", c.Arg(0))
		color.Yellow("Usage: /todo [add <text>|start <id>|done <id>|block <id>|open <id>|rm <id>|prune|clear]")
	}
}

// setTodoState moves a task and reports the result. The optional trailing text
// after the ID is kept as a note, which is where "why is this blocked?" lives.
func setTodoState(c cmdArgs, state session.TodoState, verb string) {
	id, ok := todoID(c.Arg(1))
	if !ok {
		return
	}
	item, err := todoList.SetState(id, state, c.From(2))
	if err != nil {
		color.Red("%v", err)
		return
	}
	color.Green("%s task %d: %s", verb, item.ID, item.Text)
	if item.Note != "" {
		color.Cyan("  note: %s", item.Note)
	}
}

// todoID parses a task ID, reporting the problem rather than defaulting.
func todoID(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		color.Red("Which task? Give its ID — /todo lists them.")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		color.Red("%q is not a task ID. IDs are the numbers shown by /todo.", raw)
		return 0, false
	}
	return id, true
}

func printTodoList() {
	items := todoList.Items()
	if len(items) == 0 {
		color.Cyan("No tasks. Add one with /todo add <what needs doing>.")
		color.Yellow("Open tasks are shown to the planner, so the harness can pick up where it stopped.")
		return
	}

	fmt.Println()
	color.Cyan("⚡ TASKS")
	for _, it := range items {
		paint := shell.HexText
		switch it.State {
		case session.TodoDone:
			paint = shell.HexMuted
		case session.TodoInProgress:
			paint = shell.HexSecondary
		case session.TodoBlocked:
			paint = shell.HexRectifier
		}
		fmt.Printf("  %s %s %s\n",
			shell.Fg(shell.HexMuted, fmt.Sprintf("%3d", it.ID)),
			shell.Fg(paint, it.State.Symbol()),
			shell.Fg(paint, it.Text))
		if it.Note != "" {
			fmt.Printf("      %s\n", shell.Fg(shell.HexMuted, "note: "+it.Note))
		}
	}

	counts := todoList.Counts()
	fmt.Println()
	color.Cyan("%d pending · %d in progress · %d blocked · %d done",
		counts[session.TodoPending], counts[session.TodoInProgress],
		counts[session.TodoBlocked], counts[session.TodoDone])
	fmt.Println()
}

// -------------------------------------------------------
// /tools
// -------------------------------------------------------

// handleToolsCommand lists the planner's tool vocabulary with each tool's live
// availability and the gate that stands in front of it.
func handleToolsCommand() {
	type toolRow struct {
		name      string
		purpose   string
		gate      string
		available bool
		detail    string
	}

	rows := []toolRow{
		{
			name: "response", purpose: "Answer in prose without acting",
			gate: "none — text only", available: true,
		},
		{
			name: "shell", purpose: "Run a shell command",
			gate:      "validation → risk tiers → sandbox → hooks",
			available: true,
			detail:    fmt.Sprintf("sandbox: %s · posture: %s", sandboxMode(), agentCore.Permission()),
		},
		{
			name: "git", purpose: "Repository operations",
			gate:      "typed confirmation for destructive actions; never by voice",
			available: insideGitRepo(),
			detail:    gitToolDetail(),
		},
		{
			name: "package", purpose: "Install, update, remove packages",
			gate:      "package safety check → confirmation",
			available: true,
		},
		{
			name: "recon", purpose: "Reconnaissance against an authorized target",
			gate:      "written-scope authorization required",
			available: agentCore != nil && len(agentCore.ListAuthorizedReconTargets()) > 0,
			detail:    reconToolDetail(),
		},
		{
			name: "web", purpose: "Search or fetch a public page (read-only)",
			gate:      "public-address guard; retrieved text has zero authority",
			available: true,
		},
		{
			name: "vision", purpose: "Look through the camera and describe one frame",
			gate:      "/blackbox eyes opt-in; one in-memory frame per turn, never on disk",
			available: agentCore != nil && agentCore.VisionAvailable(),
			detail:    visionRouteDescription(),
		},
	}

	fmt.Println(shell.PanelTitle("harness tools"))
	fmt.Println(shell.PanelLine(shell.Muted(
		"the vocabulary is CLOSED — a tool outside this list is dropped from the plan,")))
	fmt.Println(shell.PanelLine(shell.Muted(
		"never dispatched. That is the security property, not a limitation.")))
	fmt.Println(shell.PanelGap())

	for _, r := range rows {
		state := shell.Badge(shell.StateGood, "ready")
		if !r.available {
			state = shell.Badge(shell.StateIdle, "idle")
		}
		// Padding goes INSIDE the color call: a %-9s applied to an already
		// colored string counts the escape bytes and pads to nothing visible.
		// Name, state and purpose on ONE line: the state is the thing being
		// scanned for, and putting it on its own row made the reader's eye
		// travel down instead of across.
		fmt.Println(shell.PanelLine(
			shell.Fg(shell.HexAmber, fmt.Sprintf("%-9s", r.name)) + " " +
				shell.PadVisible(state, 9) + " " + shell.Fg(shell.HexText, r.purpose)))
		fmt.Println(shell.PanelLine("                    " + shell.Muted("gate: "+r.gate)))
		if r.detail != "" {
			fmt.Println(shell.PanelLine("                    " + shell.Muted(r.detail)))
		}
		fmt.Println(shell.PanelGap())
	}

	fmt.Println(shell.PanelSection("planner"))
	w := shell.KVWidth("TRANSPORT", "HARNESS", "HOOKS")
	fmt.Println(shell.KV("TRANSPORT", shell.Value(ai.PlannerTransport()), w))
	if agentCore != nil {
		agentic := shell.Badge(shell.StateIdle, "off") + shell.Muted("  single-shot planning")
		if agentCore.Agentic {
			agentic = shell.Badge(shell.StateGood, "on") +
				shell.Muted(fmt.Sprintf("  step budget %d", agenticStepBudget()))
		}
		fmt.Println(shell.KV("HARNESS", agentic, w))
		fmt.Println(shell.KV("HOOKS", shell.Value(fmt.Sprintf("%d loaded", agentCore.HookCount())), w))
	}
	fmt.Println(shell.PanelEnd())
}

func sandboxMode() string {
	if sandbox == nil {
		return "unavailable"
	}
	return sandbox.ModeString()
}

func gitToolDetail() string {
	if !insideGitRepo() {
		return "not inside a git repository right now"
	}
	branch, _ := runRead(".", "git", "branch", "--show-current")
	if branch == "" {
		branch = "detached HEAD"
	}
	return "branch: " + branch
}

func reconToolDetail() string {
	if agentCore == nil {
		return ""
	}
	targets := agentCore.ListAuthorizedReconTargets()
	if len(targets) == 0 {
		return "no authorized targets — /scan authorize <target> --reason \"<scope>\""
	}
	names := make([]string, 0, len(targets))
	for t := range targets {
		names = append(names, t)
	}
	return fmt.Sprintf("%d authorized: %s", len(names), strings.Join(names, ", "))
}

// -------------------------------------------------------
// /hooks
// -------------------------------------------------------

func handleHooksCommand(c cmdArgs) {
	set, err := hooks.Load()
	if err != nil {
		color.Red("Could not load hooks: %v", err)
		color.Yellow("Fix or remove the file, then run /hooks again. Until it loads, NO hooks run.")
		return
	}

	switch c.Sub() {
	case "", "list", "ls", "status":
		printHooks(set)

	case "events":
		color.Cyan("Hook events: %s", hookEventList())
		color.Cyan("pre-shell / pre-git can DENY a step when the hook is blocking.")
		color.Cyan("post-* hooks are informational: the action already happened.")

	case "add":
		addHookInteractive(set)

	case "rm", "remove", "del", "delete":
		if c.Count() < 2 {
			color.Red("Usage: /hooks rm <name>")
			return
		}
		if err := set.Remove(c.From(1)); err != nil {
			color.Red("%v", err)
			return
		}
		color.Green("Removed hook %q.", c.From(1))

	case "on", "enable":
		toggleHook(set, c.From(1), true)
	case "off", "disable":
		toggleHook(set, c.From(1), false)

	case "test":
		testHook(c)

	default:
		color.Red("Unknown subcommand: %s", c.Arg(0))
		color.Yellow("Usage: /hooks [list|events|add|rm <name>|on <name>|off <name>|test <event> <command>]")
	}
}

func printHooks(set *hooks.Set) {
	fmt.Println()
	color.Cyan("⚡ LOCAL POLICY HOOKS")
	color.Cyan("Config: %s", set.Path())
	if len(set.Hooks) == 0 {
		fmt.Println()
		color.Yellow("No hooks configured. Hooks are opt-in and come only from that file —")
		color.Yellow("nothing a model produces can define one.")
		color.Cyan("Add one with /hooks add, or see the events with /hooks events.")
		fmt.Println()
		return
	}

	fmt.Println()
	for _, h := range set.Hooks {
		state := shell.Fg(shell.HexSecondary, "on ")
		if h.Disabled {
			state = shell.Fg(shell.HexMuted, "off")
		}
		mode := "observe"
		if h.Blocking {
			mode = "BLOCKING"
		}
		fmt.Printf("  %s %s %s\n", state,
			shell.Fg(shell.HexAmber, fmt.Sprintf("%-16s", h.Name)),
			shell.Fg(shell.HexMuted, fmt.Sprintf("%s · %s · timeout %s",
				h.Event, mode, h.Timeout())))
		if h.Match != "" {
			fmt.Printf("      %s\n", shell.Fg(shell.HexMuted, "match: /"+h.Match+"/"))
		}
		fmt.Printf("      %s\n", shell.Fg(shell.HexText, "$ "+h.Command))
	}
	fmt.Println()
	color.Yellow("A blocking pre-hook that exits non-zero DENIES the step. Hooks run after")
	color.Yellow("every built-in gate, so they can only subtract permission, never grant it.")
	fmt.Println()
}

func addHookInteractive(set *hooks.Set) {
	color.Cyan("New hook. Blank input at any prompt cancels.")

	name := strings.TrimSpace(commands.AskLine("Name"))
	if name == "" {
		color.Yellow("Cancelled.")
		return
	}
	color.Cyan("Events: %s", hookEventList())
	eventRaw := strings.TrimSpace(commands.AskLine("Event"))
	event, ok := hooks.ValidEvent(eventRaw)
	if !ok {
		color.Red("%q is not a valid event. Valid: %s", eventRaw, hookEventList())
		return
	}
	command := strings.TrimSpace(commands.AskLine("Shell command to run"))
	if command == "" {
		color.Yellow("Cancelled.")
		return
	}
	match := strings.TrimSpace(commands.AskLine("Match pattern (regexp, blank = every occurrence)"))

	blocking := false
	if event == hooks.PreShell || event == hooks.PreGit {
		color.Yellow("A blocking hook DENIES the step when it exits non-zero.")
		blocking = commands.AskForConfirmation("Make this hook blocking?")
	}

	h := hooks.Hook{Name: name, Event: event, Match: match, Command: command, Blocking: blocking}
	if err := set.Add(h); err != nil {
		color.Red("%v", err)
		return
	}
	color.Green("Added hook %q on %s.", name, event)
	// The live agent holds its own loaded copy; without this the new hook would
	// not fire until the next restart, which is precisely when someone would
	// assume it was already guarding them.
	reloadAgentHooks()
}

func toggleHook(set *hooks.Set, name string, enabled bool) {
	if strings.TrimSpace(name) == "" {
		color.Red("Usage: /hooks %s <name>", map[bool]string{true: "on", false: "off"}[enabled])
		return
	}
	if err := set.SetEnabled(name, enabled); err != nil {
		color.Red("%v", err)
		return
	}
	if enabled {
		color.Green("Hook %q enabled.", name)
	} else {
		color.Yellow("Hook %q disabled.", name)
	}
	reloadAgentHooks()
}

// testHook runs a command once with the hook environment populated, so a rule
// can be checked before it is trusted to block real work.
func testHook(c cmdArgs) {
	if c.Count() < 3 {
		color.Red("Usage: /hooks test <event> <command>")
		color.Yellow("Runs the command once with the HELIX_* hook variables set to sample values.")
		return
	}
	event, ok := hooks.ValidEvent(c.Arg(1))
	if !ok {
		color.Red("%q is not a valid event. Valid: %s", c.Arg(1), hookEventList())
		return
	}
	command := c.From(2)

	wd, _ := os.Getwd()
	set := &hooks.Set{Hooks: []hooks.Hook{{
		Name: "test", Event: event, Command: command, Blocking: false,
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), hooks.DefaultTimeout)
	defer cancel()
	results := set.Run(ctx, event, hooks.Context{
		Tool:    "shell",
		Command: "echo sample-command",
		Dir:     wd,
	})
	if len(results) == 0 {
		color.Yellow("The hook did not run.")
		return
	}
	r := results[0]
	fmt.Println()
	color.Cyan("exit %d", r.ExitCode)
	if r.Output != "" {
		fmt.Println(r.Output)
	}
	if r.ExitCode == 0 {
		color.Green("As a blocking pre-hook, this would ALLOW the step.")
	} else {
		color.Yellow("As a blocking pre-hook, this would DENY the step.")
	}
	fmt.Println()
}

// reloadAgentHooks re-reads the hook file into the live agent.
func reloadAgentHooks() {
	if agentCore == nil {
		return
	}
	set, err := hooks.Load()
	if err != nil {
		color.Yellow("Hooks changed on disk but could not be reloaded: %v", err)
		return
	}
	agentCore.Hooks = set
}

// -------------------------------------------------------
// /undo
// -------------------------------------------------------

func handleUndoCommand() {
	if !requireAgent() {
		return
	}
	// Previously reachable only by SAYING "undo that": a typed session had a
	// populated journal and no way to use it.
	agentCore.RequestUndo()
}

// -------------------------------------------------------
// /config
// -------------------------------------------------------

// configKey is one settable setting: how to read it, how to write it, and what
// it means. Secrets are deliberately absent — API keys go through /setup and the
// key store, never through a command whose arguments land in shell history.
type configKey struct {
	name  string
	help  string
	get   func() string
	set   func(string) error
	extra string // accepted values, shown in help
}

func configKeys() []configKey {
	return []configKey{
		{
			name: "name", help: "Your name, shown in the prompt", extra: "any text",
			get: func() string { return cfg.UserPrefs.UserName },
			set: func(v string) error {
				cfg.UserPrefs.UserName = v
				shell.SetUserName(v)
				return nil
			},
		},
		{
			name: "permission", help: "Approval posture", extra: permissionModeList(),
			get: func() string { return string(agentCore.Permission()) },
			set: func(v string) error {
				mode, ok := agent.ParsePermissionMode(v)
				if !ok {
					return fmt.Errorf("not a mode; want one of: %s", permissionModeList())
				}
				agentCore.SetPermission(mode)
				cfg.UserPrefs.Permission = string(mode)
				return nil
			},
		},
		{
			name: "agentic", help: "Iterative self-correcting harness", extra: "on | off",
			get: func() string { return onOff(cfg.UserPrefs.AgenticMode) },
			set: func(v string) error {
				on, err := parseOnOff(v)
				if err != nil {
					return err
				}
				cfg.UserPrefs.AgenticMode = on
				agentCore.Agentic = on
				return nil
			},
		},
		{
			name: "agentic-steps", help: "Harness step budget", extra: "1 - 20",
			get: func() string { return strconv.Itoa(agenticStepBudget()) },
			set: func(v string) error {
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 || n > maxAgenticStepBudget {
					return fmt.Errorf("want a whole number between 1 and %d", maxAgenticStepBudget)
				}
				agentCore.MaxAgenticSteps = n
				cfg.UserPrefs.AgenticSteps = n
				return nil
			},
		},
		{
			name: "typing-effect", help: "Animate AI replies", extra: "on | off",
			get: func() string { return onOff(cfg.UserPrefs.TypingEffect) },
			set: func(v string) error {
				on, err := parseOnOff(v)
				if err != nil {
					return err
				}
				cfg.UserPrefs.TypingEffect = on
				return nil
			},
		},
		{
			name: "typewrite-all", help: "Animate ALL output, not just AI", extra: "on | off",
			get: func() string { return onOff(cfg.UserPrefs.TypewriteAll) },
			set: func(v string) error {
				on, err := parseOnOff(v)
				if err != nil {
					return err
				}
				cfg.UserPrefs.TypewriteAll = on
				if gui := agentCore.GetUX(); gui != nil {
					gui.SetTypewriteAll(on)
				}
				return nil
			},
		},
		{
			name: "debug", help: "Verbose debug logging", extra: "on | off",
			get: func() string { return onOff(cfg.UserPrefs.DebugMode) },
			set: func(v string) error {
				on, err := parseOnOff(v)
				if err != nil {
					return err
				}
				cfg.UserPrefs.DebugMode = on
				utils.SetDebugMode(on)
				if on {
					_ = os.Setenv("HELIX_DEBUG", "1")
				} else {
					_ = os.Unsetenv("HELIX_DEBUG")
				}
				return nil
			},
		},
		{
			name: "audio", help: "Tonal audio feedback", extra: "on | off",
			get: func() string { return onOff(audio.IsEnabled()) },
			set: func(v string) error {
				on, err := parseOnOff(v)
				if err != nil {
					return err
				}
				audio.SetEnabled(on)
				return nil
			},
		},
		{
			name: "tts", help: "Automatic spoken responses", extra: "on | off",
			get: func() string { return onOff(speech.TTSEnabled()) },
			set: func(v string) error {
				on, err := parseOnOff(v)
				if err != nil {
					return err
				}
				speech.SetTTSEnabled(on)
				return nil
			},
		},
		{
			// Added because the status panel was TELLING people to run
			// "/config speech.tts.barge_in true", and /config takes a fixed
			// allowlist of short keys — there was no such key, so the one
			// instruction Helix gave for enabling the feature was a command it
			// would reject. Either the guidance or the key had to change; a
			// real key is the better half to add.
			name: "barge-in", help: "Interrupt a spoken reply between sentences",
			extra: "on | off",
			get:   func() string { return onOff(cfg.Speech.TTS.BargeIn) },
			set: func(v string) error {
				on, err := parseOnOff(v)
				if err != nil {
					return err
				}
				cfg.Speech.TTS.BargeIn = on
				// Takes effect now AND persists: a setting that needed a
				// restart to matter would be a worse answer than the config
				// file it replaces.
				speech.EnableBargeIn(on && voiceModeActive)
				return cfg.SavePreferences()
			},
		},
		{
			// Same reason as barge-in: /doctor was printing
			// "/config llm.fallback.model <name>", which is the config.json
			// path and not a /config key — so the fix it offered for a missing
			// offline brain was itself unrunnable. Found by the drift test
			// added alongside, in code written minutes earlier.
			name: "fallback-model", help: "Offline model used when the cloud fails",
			extra: "an installed Ollama model id",
			// Returns the value and nothing else. It used to return the literal
			// string "(unset)" for an empty model — a rendering decision baked
			// into an accessor, so every reader saw a configured-looking value
			// where there was none, and the settings table coloured it like a
			// real one. Absence is the renderer's to describe.
			get: func() string { return cfg.LLM.Fallback.Model },
			set: func(v string) error {
				v = strings.TrimSpace(v)
				if v == "" {
					return fmt.Errorf("want an Ollama model id, e.g. llama3.2")
				}
				cfg.LLM.Fallback.Model = v
				return cfg.SavePreferences()
			},
		},
		{
			name: "context-turns", help: "Turns of context given to a voice model",
			extra: "0 (off) - 12",
			get:   func() string { return fmt.Sprint(cfg.Speech.TTS.ContextTurns) },
			set: func(v string) error {
				n, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil || n < 0 || n > 12 {
					return fmt.Errorf("want a whole number between 0 and 12")
				}
				cfg.Speech.TTS.ContextTurns = n
				if voiceModeActive {
					speech.EnableConversationContext(n, cfg.Speech.TTS.ContextMaxBytes)
				}
				return cfg.SavePreferences()
			},
		},
		{
			name: "provider", help: "Active AI provider", extra: strings.Join(ai.ListProviders(), " | "),
			get: func() string { return ai.ActiveProviderName() },
			set: func(v string) error {
				if !ai.HasProvider(v) {
					return fmt.Errorf("not registered; want one of: %s", strings.Join(ai.ListProviders(), ", "))
				}
				switchProvider(v)
				return nil
			},
		},
		{
			name: "model", help: "Active model on that provider", extra: "a model id",
			get: func() string { return ai.ActiveModel() },
			set: func(v string) error {
				switchModel(v)
				return nil
			},
		},
		{
			name: "stt-url", help: "Local STT sidecar endpoint", extra: "a base URL, or blank to reset",
			get: func() string { return cfg.Speech.STT.BaseURL },
			set: func(v string) error {
				return applySpeechEndpoint(&cfg.Speech.STT.BaseURL, v)
			},
		},
		{
			name: "tts-url", help: "Local TTS sidecar endpoint", extra: "a base URL, or blank to reset",
			get: func() string { return cfg.Speech.TTS.BaseURL },
			set: func(v string) error {
				return applySpeechEndpoint(&cfg.Speech.TTS.BaseURL, v)
			},
		},
		{
			name: "sandbox", help: "Directory confinement", extra: "off | current | strict",
			get: sandboxMode,
			set: func(v string) error {
				mode, ok := parseSandboxMode(v)
				if !ok {
					return fmt.Errorf("want off, current, or strict")
				}
				sandbox.SetMode(mode)
				return nil
			},
		},
	}
}

func handleConfigCommand(c cmdArgs) {
	if !requireAgent() {
		return
	}
	keys := configKeys()

	if c.Empty() {
		printSettingsPanel(keys)
		return
	}

	name := strings.ToLower(c.Arg(0))
	var target *configKey
	for i := range keys {
		if keys[i].name == name {
			target = &keys[i]
			break
		}
	}
	if target == nil {
		color.Red("Unknown setting: %s", c.Arg(0))
		color.Yellow("Run /config with no argument to list every settable key.")
		return
	}

	if c.Count() == 1 {
		// The KEY is the label, so the row reads "AGENTIC  on" rather than
		// putting the value in front of the thing it belongs to.
		key := strings.ToUpper(target.name)
		w := shell.KVWidth(key, "DOES", "ACCEPTS")
		fmt.Println(shell.PanelTitle("setting"))
		fmt.Println(shell.KV(key, settingValue(target.get()), w))
		fmt.Println(shell.KV("DOES", shell.Muted(target.help), w))
		if target.extra != "" {
			fmt.Println(shell.KV("ACCEPTS", shell.Muted(target.extra), w))
		}
		fmt.Println(shell.PanelEnd())
		return
	}

	value := c.From(1)
	if err := target.set(value); err != nil {
		fmt.Println(shell.Step(shell.StateBad, target.name, "not set — "+err.Error()))
		if target.extra != "" {
			for _, l := range shell.StepDetail("accepts: "+target.extra, shell.Muted) {
				fmt.Println(l)
			}
		}
		return
	}
	if err := cfg.SavePreferences(); err != nil {
		fmt.Println(shell.Step(shell.StateWarn, target.name,
			"applied for this session, but saving failed: "+err.Error()))
		return
	}
	fmt.Println(shell.Step(shell.StateGood, target.name, orNone(target.get())))
}

// printSettingsPanel renders every settable key as one self-fitting table.
//
// The old rendering hand-padded three columns at %-15s and %-22s. That is the
// same defect /cost carried: a width chosen in source cannot know the terminal,
// so `deepseek-v4-flash-vision-exp` came back as `deepseek-v4-flash-vis…` on a
// 102-column screen with room to spare — a settings screen truncating the value
// it exists to report. shell.Table measures the content and shrinks the widest
// column only when the panel genuinely cannot hold it.
func printSettingsPanel(keys []configKey) {
	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, []string{k.name, settingValue(k.get()), shell.Muted(k.help)})
	}

	fmt.Println(shell.PanelTitle("settings"))
	for _, l := range shell.Table([]string{"setting", "value", "what it does"}, rows) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelGap())
	fmt.Println(shell.KV("STORED IN", shell.Muted(cfg.ConfigPath), shell.KVWidth("STORED IN")))
	fmt.Println(shell.PanelGap())

	// Not a footnote. Where a secret is allowed to be typed is a security
	// property of this screen, and it was previously two yellow lines outside
	// the frame that read like a caveat rather than a rule.
	fmt.Println(shell.Step(shell.StateWarn, "api keys",
		"are NOT settable here — /setup puts them in the key store, "+
			"so a secret never lands in shell history"))
	fmt.Println(shell.PanelEnd())
	fmt.Println(shell.Hint("/config <setting> <value>"))
}

// settingValue renders a value, distinguishing "not set" from a real one.
//
// An unset setting rendered in the same amber as a configured one is how a
// screen full of values hides the two that are blank — which on this screen are
// the two most consequential (the offline fallback model, and the STT endpoint).
func settingValue(v string) string {
	if strings.TrimSpace(v) == "" {
		return shell.Muted("unset")
	}
	return shell.Value(v)
}

// applySpeechEndpoint validates and stores a sidecar base URL, then rebuilds the
// speech registry so the change takes effect in this session.
//
// Rebuilding matters: adapters capture their endpoint at construction, so
// without re-running speech.Init the new URL would only apply after a restart —
// and the user would reasonably conclude the setting did nothing.
func applySpeechEndpoint(field *string, value string) error {
	value = strings.TrimSpace(value)
	if value != "" && value != "reset" && value != "default" {
		u, err := url.Parse(value)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("want a full URL like http://127.0.0.1:8081")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
		}
		*field = strings.TrimSuffix(value, "/")
	} else {
		*field = ""
	}

	if err := speech.Init(speechConfigFrom(cfg.Speech)); err != nil {
		return fmt.Errorf("applied, but the speech engine failed to rebuild: %w", err)
	}
	return nil
}

func parseSandboxMode(v string) (commands.SandboxMode, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "disable", "none":
		return commands.SandboxDisabled, true
	case "current", "dir", "normal":
		return commands.SandboxCurrentDir, true
	case "strict", "tight", "restricted":
		return commands.SandboxStrict, true
	}
	return commands.SandboxDisabled, false
}

func parseOnOff(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "true", "yes", "1", "enable", "enabled":
		return true, nil
	case "off", "false", "no", "0", "disable", "disabled":
		return false, nil
	}
	return false, fmt.Errorf("want on or off")
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// orNone is the plain-text form of settingValue, for output that carries no
// colour of its own. Both say the same word, because two vocabularies for the
// same state is how a screen ends up reporting "(unset)" in one row and "unset"
// in the next.
func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unset"
	}
	return s
}

// -------------------------------------------------------
// /version
// -------------------------------------------------------

func handleVersionCommand(cmdArgs) {
	rep := edge.Collect()
	fmt.Println()
	color.Cyan("Helix %s", config.HelixVersion)
	fmt.Printf("  %-22s %s/%s\n", "Platform", rep.OS, rep.Arch)
	if rep.Board != "" {
		fmt.Printf("  %-22s %s\n", "Board", rep.Board)
	}
	// The two build facts that change behavior invisibly.
	if rep.SpeechSupported {
		fmt.Printf("  %-22s %s\n", "Audio output", rep.AudioBackend)
	} else {
		fmt.Printf("  %-22s %s\n", "Audio output",
			shell.Fg(shell.HexRectifier, rep.AudioBackend+" — this build cannot speak"))
	}
	fmt.Printf("  %-22s %s\n", "Confinement backend", confinement.BackendName())
	fmt.Printf("  %-22s %s (%s)\n", "AI provider", ai.ActiveProviderName(), ai.ActiveModel())
	fmt.Printf("  %-22s %s\n", "Planner transport", ai.PlannerTransport())
	fmt.Printf("  %-22s %d registered\n", "Slash commands", len(registry))
	fmt.Println()
	color.Cyan("/doctor runs the full diagnostic, including sidecars and thermals.")
	fmt.Println()
}
