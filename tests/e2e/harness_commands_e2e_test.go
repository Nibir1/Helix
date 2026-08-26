//go:build !windows

// tests/e2e/harness_commands_e2e_test.go
// Purpose: end-to-end coverage of the harness commands added to the registry —
// /permissions, /todo, /tools, /plan, /hooks, /clear, /resume, /export, /cost,
// /context, /config, /undo, /version — driven through the real binary under a
// PTY, with no real AI and no network.
//
// These are shell-level behaviors: whether a command is dispatched at all,
// whether it persists what it claims to persist, and whether the help text
// matches the code. Unit tests cover the logic; only this level catches a
// command that is unreachable, crashes on a nil dependency, or contradicts its
// own /help entry.
package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// unusedPlan is a planner reply the command tests never rely on: every command
// here must answer without a model call.
const commandsNoPlan = `{"intent":"chat","steps":[{"tool":"response","message":"unused"}]}`

func TestE2E_PermissionsCommandShowsAndSetsMode(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	if err := h.SendExpect("/permissions", "APPROVAL POSTURE", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	// Every mode must be listed, or the user cannot discover the one they want.
	for _, mode := range []string{"plan", "cautious", "ask", "auto"} {
		if err := h.Expect(mode, 5*time.Second); err != nil {
			t.Fatalf("mode %q missing from /permissions: %v", mode, err)
		}
	}
	// The guarantee that makes the feature safe must be stated where it is set.
	if err := h.Expect("High-risk commands stay blocked in every mode", 5*time.Second); err != nil {
		t.Fatal(err)
	}

	if err := h.SendExpect("/permissions plan", "Permission mode: plan", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	// And it must persist, so a posture survives the restart it was chosen for.
	cfgPath := filepath.Join(h.home, ".helix", "config.json")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(cfgPath)
		if err == nil && strings.Contains(string(data), `"permission": "plan"`) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("permission mode was not persisted to %s\n----- output -----\n%s", cfgPath, h.stripped())
}

func TestE2E_PermissionsRejectsUnknownMode(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	if err := h.SendExpect("/permissions definitely-not-a-mode", "Unknown mode", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("Valid modes:", 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

// TestE2E_PlanModeRefusesToExecute is the behavior that matters most: in plan
// mode a shell command must be described, not run.
func TestE2E_PlanModeRefusesToExecute(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	marker := filepath.Join(h.project, "plan-mode-should-not-create-this")
	if err := h.SendExpect("/permissions plan", "Permission mode: plan", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	// A direct shell command bypasses the planner but not the pipeline, so it
	// exercises the same gate a planned step would hit.
	if err := h.SendExpect("touch "+marker, "[plan] would execute", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("plan mode executed the command\n----- output -----\n%s", h.stripped())
	}
}

func TestE2E_TodoLifecyclePersists(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	if err := h.SendExpect("/todo", "No tasks", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/todo add wire up the parser", "Added task 1", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/todo add write its tests", "Added task 2", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/todo start 1", "In progress task 1", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/todo block 2 waiting on review", "Blocked task 2", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("note: waiting on review", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/todo done 1", "Completed task 1", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	// A bad ID must be reported, not silently ignored.
	if err := h.SendExpect("/todo done not-a-number", "is not a task ID", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/todo prune", "Pruned 1 completed task", 15*time.Second); err != nil {
		t.Fatal(err)
	}

	todoPath := filepath.Join(h.home, ".helix", "todo.json")
	h.ExpectFile(t, todoPath, 10*time.Second)
	data, err := os.ReadFile(todoPath)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		NextID int `json:"next_id"`
		Items  []struct {
			ID    int    `json:"id"`
			Text  string `json:"text"`
			State string `json:"state"`
			Note  string `json:"note"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("todo.json is not valid JSON: %v\n%s", err, data)
	}
	if len(file.Items) != 1 {
		t.Fatalf("expected 1 surviving task, got %d: %s", len(file.Items), data)
	}
	if file.Items[0].State != "blocked" || file.Items[0].Note != "waiting on review" {
		t.Errorf("surviving task = %+v, want the blocked one with its note", file.Items[0])
	}
	// Prune must keep the ID the user just read off the screen.
	if file.Items[0].ID != 2 {
		t.Errorf("surviving task id = %d, want 2 (prune must not renumber)", file.Items[0].ID)
	}
}

func TestE2E_ToolsListsClosedVocabulary(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	if err := h.SendExpect("/tools", "HARNESS TOOLS", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	// The vocabulary is a security property, so every member must be visible.
	for _, tool := range []string{"response", "shell", "git", "package", "recon", "web", "vision"} {
		if err := h.Expect(tool, 5*time.Second); err != nil {
			t.Fatalf("tool %q missing from /tools: %v", tool, err)
		}
	}
	if err := h.Expect("dropped from the plan", 5*time.Second); err != nil {
		t.Fatal("the closed-vocabulary rule must be stated")
	}
}

func TestE2E_HooksAddRunAndDeny(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	if err := h.SendExpect("/hooks", "LOCAL POLICY HOOKS", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("No hooks configured", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/hooks events", "pre-shell", 15*time.Second); err != nil {
		t.Fatal(err)
	}

	// /hooks test proves a rule before it is trusted to block real work.
	if err := h.SendExpect("/hooks test pre-shell exit 7", "exit 7", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("would DENY the step", 5*time.Second); err != nil {
		t.Fatal(err)
	}

	// Now install a blocking hook out of band and confirm it actually stops a
	// command the risk tiers would have allowed.
	hooksPath := filepath.Join(h.home, ".helix", "hooks.json")
	body := `{"hooks":[{"name":"no-touch","event":"pre-shell","match":"touch","command":"echo forbidden here >&2; exit 1","blocking":true}]}`
	if err := os.WriteFile(hooksPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// The live agent holds a loaded copy; /hooks on reloads it.
	if err := h.SendExpect("/hooks on no-touch", "enabled", 15*time.Second); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(h.project, "hook-should-block-this")
	if err := h.SendExpect("touch "+marker, "Blocked by local hook policy", 25*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("forbidden here", 5*time.Second); err != nil {
		t.Fatal("the hook's own message is the only explanation the user gets")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("the hook-denied command ran anyway\n----- output -----\n%s", h.stripped())
	}
}

// TestE2E_HooksReportBrokenConfigLoudly: a hook file that fails to parse means
// NO hooks run, which is invisible unless it is said out loud.
func TestE2E_HooksReportBrokenConfig(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	hooksPath := filepath.Join(h.home, ".helix", "hooks.json")
	if err := os.WriteFile(hooksPath, []byte(`{"hooks":[{"name":"x","event":"pre-nothing","command":"true"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/hooks", "Could not load hooks", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("NO hooks run", 5*time.Second); err != nil {
		t.Fatal("the consequence of a broken config must be stated")
	}
}

// TestE2E_ClearArchivesBeforeWiping: /clear is what people reach for when a
// session is confused, and losing the transcript is not what they asked for.
func TestE2E_ClearArchivesConversation(t *testing.T) {
	h := newHarness(t, `{"intent":"chat","steps":[{"tool":"response","message":"noted"}]}`)
	defer h.Close()

	// One real turn, so there is something to archive.
	if err := h.SendExpect("remember this detail please", "noted", 40*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/clear", "Conversation cleared", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("Archived as", 5*time.Second); err != nil {
		t.Fatal("a clear that archives nothing is a destructive clear")
	}
	if err := h.SendExpect("/memory", "Conversation memory is empty", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	// And the archive must be listed by /resume, or it is unreachable.
	if err := h.SendExpect("/resume", "Archived conversations", 15*time.Second); err != nil {
		t.Fatal(err)
	}

	sessionsDir := filepath.Join(h.home, ".helix", "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		t.Fatalf("no session archive directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the archive directory is empty")
	}
}

func TestE2E_ExportWritesTranscript(t *testing.T) {
	h := newHarness(t, `{"intent":"chat","steps":[{"tool":"response","message":"exported reply"}]}`)
	defer h.Close()

	if err := h.SendExpect("something worth exporting", "exported reply", 40*time.Second); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(h.project, "transcript.md")
	if err := h.SendExpect("/export "+target, "Exported", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	h.ExpectFile(t, target, 10*time.Second)

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "# Helix session transcript") {
		t.Errorf("transcript is missing its title:\n%s", body)
	}
	if !strings.Contains(body, "something worth exporting") {
		t.Errorf("transcript is missing the user turn:\n%s", body)
	}
	// The conversation must be quoted, so its own Markdown cannot restructure
	// the document.
	if !strings.Contains(body, "> something worth exporting") {
		t.Errorf("transcript content is not quoted:\n%s", body)
	}
}

// TestE2E_CostAndContextAreHonestAboutEstimates: the token numbers are derived
// from text length, and a report that presents them as measured is misleading.
func TestE2E_CostAndContextLabelEstimates(t *testing.T) {
	h := newHarness(t, `{"intent":"chat","steps":[{"tool":"response","message":"counted"}]}`)
	defer h.Close()

	if err := h.SendExpect("/cost", "No model calls yet", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("spend one model call", "counted", 40*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/cost", "SESSION MODEL USAGE", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("ESTIMATED", 5*time.Second); err != nil {
		t.Fatal("/cost must say the token counts are estimates")
	}
	if err := h.Expect("no price table", 5*time.Second); err != nil {
		t.Fatal("/cost must explain why it reports no money figure")
	}

	if err := h.SendExpect("/context", "CONTEXT BUDGET", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Conversation memory", "Open tasks", "Project context", "ESTIMATES"} {
		if err := h.Expect(want, 5*time.Second); err != nil {
			t.Fatalf("/context missing %q: %v", want, err)
		}
	}
}

func TestE2E_ConfigShowsAndSets(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	if err := h.SendExpect("/config", "SETTINGS", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	// Secrets must be excluded, and the reason said out loud.
	if err := h.Expect("API keys are NOT settable here", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/config agentic on", "agentic = on", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/config agentic", "agentic = on", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	// An invalid value must be refused with the accepted set, not coerced.
	if err := h.SendExpect("/config agentic maybe", "Cannot set agentic", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/config no-such-key", "Unknown setting", 15*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestE2E_UndoIsReachableByKeyboard(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	// The point of the command: before it existed, the undo journal was only
	// reachable by SAYING "undo that".
	if err := h.SendExpect("/undo", "Nothing reversible", 15*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestE2E_VersionReportsBuildFacts(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	if err := h.SendExpect("/version", "Helix", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Platform", "Audio output", "Confinement backend", "Slash commands"} {
		if err := h.Expect(want, 5*time.Second); err != nil {
			t.Fatalf("/version missing %q: %v", want, err)
		}
	}
}

// TestE2E_UnknownCommandSuggests: a dead end with no suggestion leaves the user
// with nothing to try.
func TestE2E_UnknownCommandSuggests(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	// One screen for both routes now: the typed-command error and
	// "/help <unknown>" render identically, titled UNKNOWN COMMAND.
	if err := h.SendExpect("/permissoins", "UNKNOWN COMMAND", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("DID YOU MEAN", 5*time.Second); err != nil {
		t.Fatal("a near-miss must be suggested")
	}
	if err := h.Expect("/permissions", 5*time.Second); err != nil {
		t.Fatal("the suggestion must name the command")
	}
}

// TestE2E_HelpDetailForOneCommand covers "/help <command>".
func TestE2E_HelpDetailForOneCommand(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	if err := h.SendExpect("/help permissions", "Approval posture", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	// "ALIASES" as a panel row label now, not the "Aliases:" prose prefix.
	if err := h.Expect("ALIASES", 5*time.Second); err != nil {
		t.Fatal("the detail view should list aliases")
	}
	// Without the slash, and for an alias, must work the same way.
	if err := h.SendExpect("/help /vuln", "vulnerability intelligence", 15*time.Second); err != nil {
		t.Fatal(err)
	}
}

// TestE2E_AliasesDispatch proves the registry's aliases are live, not decoration.
func TestE2E_AliasesDispatch(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	if err := h.SendExpect("/usage", "No model calls yet", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/mode", "APPROVAL POSTURE", 15*time.Second); err != nil {
		t.Fatal(err)
	}
}

// TestE2E_UppercaseCommandKeepsItsArgument is the TrimPrefix bug: "/CD <dir>"
// used to pass the whole line through as the target directory.
func TestE2E_UppercaseCommandKeepsItsArgument(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	sub := filepath.Join(h.project, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/CD "+sub, "subdir", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	// The old behavior echoed the verb back inside the failing path.
	if strings.Contains(h.stripped(), "/CD "+sub) && strings.Contains(h.stripped(), "Failed to change directory") {
		t.Fatalf("the command verb leaked into the argument\n----- output -----\n%s", h.stripped())
	}
}

// TestE2E_FoldedCommandsPointAtBlackBox: eight verbs were removed, so the
// thing that matters is not that they are gone but that typing one still tells
// you where it went. A did-you-mean list for a command that worked yesterday is
// the failure mode this replaces.
func TestE2E_FoldedCommandsPointAtBlackBox(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	for _, c := range []struct{ old, want string }{
		{"/voice", "/blackbox on"},
		{"/manual", "/blackbox off"},
		{"/eyes", "/blackbox eyes"},
	} {
		if err := h.SendExpect(c.old, c.want, 15*time.Second); err != nil {
			t.Errorf("%s does not point at %s: %v", c.old, c.want, err)
		}
	}

	// A genuinely unknown command keeps the ordinary did-you-mean path.
	if err := h.SendExpect("/nonsensecommand", "UNKNOWN COMMAND", 15*time.Second); err != nil {
		t.Fatal(err)
	}
}

// TestE2E_EyesLookIsReachableByKeyboard covers the explicit typed path to the
// camera. Before it existed, a typed session with the camera on had no way to
// use it — the conversational vision path requires the voice channel.
func TestE2E_EyesLookIsReachableByKeyboard(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	// Eyes are off by default, so the command must say so rather than silently
	// doing nothing.
	if err := h.SendExpect("/blackbox look", "Eyes are off", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	// And it must appear in the command's own help, or nobody will find it.
	if err := h.SendExpect("/help blackbox", "look [question]", 15*time.Second); err != nil {
		t.Fatal(err)
	}
}

// TestE2E_VoiceStatusReportsEndpointsAndVocabulary: the two things that were
// invisible when local speech misbehaved.
func TestE2E_VoiceStatusShowsSidecarsAndSpokenCommands(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	if err := h.SendExpect("/blackbox status", "BLACKBOX", 40*time.Second); err != nil {
		t.Fatal(err)
	}
	// A local sidecar row must name the address it is talking to.
	if err := h.Expect("endpoint: http://127.0.0.1", 20*time.Second); err != nil {
		t.Fatal("local sidecar rows must show their endpoint")
	}
	// The spoken vocabulary must be discoverable — there is no menu to read
	// when your hands are busy.
	if err := h.Expect("SPOKEN COMMANDS", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("must be typed", 10*time.Second); err != nil {
		t.Fatal("the voice deny-list must be stated where the vocabulary is listed")
	}
}
