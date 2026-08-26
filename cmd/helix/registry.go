// cmd/helix/registry.go
// Purpose: ONE table describing every Helix slash command — its name, aliases,
// usage, help text, category, and handler.
//
// Why a table instead of a switch plus a hand-written /help: the two used to
// drift. /help documented "/provider <name>" and "/model <id>" while the switch
// only accepted "/provider use <name>" and "/model use <id>", so the documented
// form silently did nothing. Dispatch, help, tab completion, and the
// did-you-mean suggester now all read this table, which makes that class of
// drift unrepresentable.
package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"helix/internal/audio"
	"helix/internal/shell"
)

// cmdArgs is the parsed form of one slash-command invocation.
//
// It exists because the old handlers each did
// strings.TrimPrefix(input, "/thing") against the RAW input line. That is wrong
// twice over: "/CD /tmp" left the target as "/CD /tmp" (the dispatcher
// lowercases the verb, TrimPrefix does not), and an alias like "/intel" never
// matched its canonical prefix at all. Parsing once, here, removes both.
type cmdArgs struct {
	// Name is the canonical command name, e.g. "/git" — even when the user
	// typed an alias or different case.
	Name string

	// Raw is the original input line, for handlers that need it verbatim.
	Raw string

	// Rest is everything after the command word, trimmed.
	Rest string

	// Fields is Rest split on whitespace.
	Fields []string
}

// Arg returns the i-th argument, or "" when absent.
func (c cmdArgs) Arg(i int) string {
	if i < 0 || i >= len(c.Fields) {
		return ""
	}
	return c.Fields[i]
}

// Sub returns the lowercased first argument — the subcommand, for the many
// commands shaped like "/thing <on|off|status>".
func (c cmdArgs) Sub() string { return strings.ToLower(c.Arg(0)) }

// Lower returns the whole argument string, lowercased and trimmed.
func (c cmdArgs) Lower() string { return strings.ToLower(c.Rest) }

// From returns the arguments from index i onward, rejoined with single spaces.
func (c cmdArgs) From(i int) string {
	if i < 0 || i >= len(c.Fields) {
		return ""
	}
	return strings.Join(c.Fields[i:], " ")
}

// Shift returns the arguments with the first word removed, renamed to the
// subcommand that consumed it.
//
// It exists for /blackbox, which folded eight top-level commands into one verb:
// the subcommand handlers were written against a cmdArgs whose Fields START at
// their own arguments, and re-deriving that by hand at each call site is how
// the old TrimPrefix bugs got in.
func (c cmdArgs) Shift() cmdArgs {
	if len(c.Fields) == 0 {
		return cmdArgs{Name: c.Name, Raw: c.Raw}
	}
	rest := strings.Join(c.Fields[1:], " ")
	return cmdArgs{
		Name:   c.Name + " " + strings.ToLower(c.Fields[0]),
		Raw:    c.Raw,
		Rest:   rest,
		Fields: c.Fields[1:],
	}
}

// Empty reports whether the command was given no arguments.
func (c cmdArgs) Empty() bool { return c.Rest == "" }

// Count is the number of arguments.
func (c cmdArgs) Count() int { return len(c.Fields) }

// Command categories, in the order /help prints them.
const (
	catCore      = "CORE & NAVIGATION"
	catSession   = "SESSION & CONTEXT"
	catHarness   = "AGENTIC HARNESS"
	catDev       = "CODE & REPOSITORY"
	catAI        = "AI & PROVIDERS"
	catKnowledge = "RAG & KNOWLEDGE BASE"
	catSecurity  = "SECURITY, RECON & STEALTH"
	catVoice     = "VOICE & PERCEPTION"
	catUtil      = "UTILITIES"
	catDanger    = "DANGER ZONE"
)

// categoryOrder fixes the display order of the sections above.
func categoryOrder() []string {
	return []string{
		catCore, catSession, catHarness, catDev, catAI,
		catKnowledge, catSecurity, catVoice, catUtil, catDanger,
	}
}

// command is one registry entry.
type command struct {
	// Name is the canonical form, including the leading slash.
	Name string

	// Aliases are alternate spellings that dispatch to the same handler.
	Aliases []string

	// Usage is the one-line signature shown by /help (defaults to Name).
	Usage string

	// Summary is the one-line description in the /help table.
	Summary string

	// Detail is the expanded explanation shown by "/help <command>". Keep it
	// to what the summary cannot say: argument meanings, safety consequences,
	// and what the command will NOT do.
	Detail []string

	Category string
	Handler  func(cmdArgs)

	// Hidden keeps a command out of the /help table while leaving it callable
	// (self-test and developer entry points).
	Hidden bool

	// VoiceOK marks the command as reachable from the voice channel.
	//
	// Still default-deny in the type — a new command is silent until someone
	// decides otherwise — but the LINE has moved. Live mode (/blackbox on) is
	// meant to reach the whole shell by speaking, so the question each command
	// now answers is not "is this important enough to allow" but "would a
	// misheard phrase or a voice on the radio do damage that cannot be undone".
	//
	// Eight commands still answer yes, and they are the whole denied set:
	//
	//	/purge /rag-reset   destroy data outright
	//	/scan               fires traffic at a third party
	//	/commit             writes history (git is never voice-reachable)
	//	/config /stealth    move the approval or privacy posture
	//	/hooks              installs policy that later runs on its own
	//	/setup              would have you dictate API keys aloud
	//	/init               writes HELIX.md, which is planner context from then on
	//
	// Everything else is reachable, and a refusal is SPOKEN, so a misheard
	// phrase never silently does something else (ADR-005).
	VoiceOK bool

	// VoiceReadOnly restricts voice to the command's ARGUMENT-FREE form.
	//
	// Some commands report state with no arguments and change it with them:
	// /permissions prints the posture, /permissions auto widens it. Splitting
	// them at the dispatcher — rather than inside each handler — keeps the voice
	// policy in one place, where it can be read and tested as a whole.
	VoiceReadOnly bool
}

// UsageLine is the signature to display.
func (c command) UsageLine() string {
	if c.Usage != "" {
		return c.Usage
	}
	return c.Name
}

var (
	registry    []command
	registryMap map[string]*command
)

// registerCommands builds the registry. Called from init(); split out so tests
// can assert the table's own invariants.
func registerCommands() []command {
	return append(append(append(append(
		coreCommands(),
		sessionCommands()...),
		devCommands()...),
		harnessCommands()...),
		legacyCommands()...)
}

func init() {
	registry = registerCommands()
	registryMap = make(map[string]*command, len(registry)*2)
	for i := range registry {
		cmd := &registry[i]
		registryMap[cmd.Name] = cmd
		for _, alias := range cmd.Aliases {
			registryMap[alias] = cmd
		}
	}
	// The line editor completes on the same names the dispatcher accepts, so a
	// new command is completable the moment it is registered.
	shell.SetSlashCommands(commandNames())
}

// voiceDeniedCommandNames lists the commands voice cannot reach, read from the
// registry itself so the spoken-vocabulary report can never drift from the
// policy it describes.
func voiceDeniedCommandNames() []string {
	var out []string
	for _, cmd := range registry {
		if cmd.Hidden || cmd.VoiceOK {
			continue
		}
		out = append(out, cmd.Name)
	}
	sort.Strings(out)
	return out
}

// commandNames returns every dispatchable name (canonical + aliases), sorted.
func commandNames() []string {
	out := make([]string, 0, len(registryMap))
	for name := range registryMap {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// lookupCommand resolves a typed verb to its registry entry.
func lookupCommand(verb string) (*command, bool) {
	cmd, ok := registryMap[strings.ToLower(verb)]
	return cmd, ok
}

// -------------------------------------------------------
// MASTER DISPATCHER
// -------------------------------------------------------

// handleSlashCommand routes one slash command. It returns true when the input
// was a Helix command (handled or reported as unknown) and false when it should
// fall through to the normal pipeline.
func handleSlashCommand(input string) bool {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return false
	}
	verb := trimmed
	rest := ""
	if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
		verb, rest = trimmed[:i], strings.TrimSpace(trimmed[i+1:])
	}

	cmd, ok := lookupCommand(verb)
	if !ok {
		// Absolute-path executables (/usr/bin/git, ./script) are NOT Helix
		// control commands; let the normal pipeline handle them. Real Helix
		// commands never contain a second slash.
		if strings.Contains(strings.TrimPrefix(verb, "/"), "/") {
			return false
		}
		handleUnknownSlashCommand(verb)
		return true
	}

	cmd.Handler(cmdArgs{
		Name:   cmd.Name,
		Raw:    trimmed,
		Rest:   rest,
		Fields: strings.Fields(rest),
	})
	return true
}

// -------------------------------------------------------
// /help
// -------------------------------------------------------

// handleHelp renders the command table, or one command's detail when given an
// argument. "/help git" and "/help /git" are the same request.
// helpIndexWidth is the command column in the /help index: the longest command
// NAME, with no argument syntax in it.
//
// The index used to print full usage lines, and the widest —
// "/blackbox [on|off|status|setup|look|eyes|wake|tts|say|log|stats]" — is 64
// columns. Sizing a shared column to that is absurd, and the old code instead
// clamped the padding, so nine commands started their description at a
// different column from the other forty-seven. That is what looked like
// overlapping text.
//
// Measured before choosing: names-only renders the 56 commands in 70 lines with
// nothing truncated, while keeping usage and truncating to fit costs the same
// 70-71 lines AND mangles fifteen signatures mid-syntax. An index answers "what
// commands exist"; "/help <command>" answers "how is it spelled", and it can be
// complete because it has the whole width to itself.
func helpIndexWidth() int {
	w := 0
	for _, c := range registry {
		if c.Hidden {
			continue
		}
		if n := len([]rune(c.Name)); n > w {
			w = n
		}
	}
	return w
}

func handleHelp(c cmdArgs) {
	if !c.Empty() {
		printCommandDetail(c.Arg(0))
		return
	}

	fmt.Println(shell.PanelTitle("helix native shell"))
	for _, l := range shell.PanelWrap(
		"SOS PROTOCOL · AI-native shell, agentic harness, natural language, MAN "+
			"pages, threat intelligence. Type a command, a shell line, or plain "+
			"English. Arguments live in /help <command>.",
		shell.Muted) {
		fmt.Println(l)
	}

	byCategory := map[string][]command{}
	for _, cmd := range registry {
		if cmd.Hidden {
			continue
		}
		byCategory[cmd.Category] = append(byCategory[cmd.Category], cmd)
	}
	for _, cat := range categoryOrder() {
		cmds := byCategory[cat]
		if len(cmds) == 0 {
			continue
		}
		fmt.Println(shell.PanelGap())
		fmt.Println(shell.PanelSection(cat))
		for _, cmd := range cmds {
			helpLine(cmd.Name, cmd.Summary)
		}
	}

	fmt.Println(shell.PanelGap())
	fmt.Println(shell.PanelSection("getting more"))
	helpLine("/help <command>", "Full detail for one command, with its arguments")
	helpLine("Tab", "Complete a slash command or a path at the prompt")
	helpLine("→ (right arrow)", "Accept the ghost-text suggestion from history")

	fmt.Println(shell.PanelGap())
	fmt.Println(shell.PanelSection("tips"))
	for _, tip := range [][2]string{
		{"PLAIN ENGLISH", "Just type it — \"find large files and delete them\""},
		{"AGENTIC MODE", "/agentic on lets Helix observe results and self-correct; " +
			"/plan <task> previews without running"},
		{"THREAT INTEL", "A free NVD API key cuts knowledge sync from ~40min to ~10min: " +
			"nvd.nist.gov/developers/request-an-api-key, then export NVD_API_KEY"},
	} {
		// Wrapped rather than columnar: these are sentences, and the fixed
		// two-column layout they used to share with the command index forced
		// them to be short enough to fit rather than long enough to help.
		fmt.Println(shell.KV(tip[0], shell.Muted(tip[1]),
			shell.KVWidth("PLAIN ENGLISH", "AGENTIC MODE", "THREAT INTEL")))
	}

	fmt.Println(shell.PanelGap())
	fmt.Println(shell.PanelSection("prompt anatomy"))
	printPromptAnatomy()
	fmt.Println(shell.PanelEnd())
}

// printPromptAnatomy shows what each block of the prompt means.
//
// Both halves. It used to describe only the LEFT prompt and label the git
// branch "telemetry", while the RIGHT prompt — the clock and the
// Helix/Red Team/name ribbon, which is the most colourful thing on screen —
// went unexplained entirely. Half an explanation of the one element a user
// looks at on every single line.
func printPromptAnatomy() {
	left := shell.Seg(shell.HexPrimary, shell.HexVoid, " HELIX ") +
		shell.Muted(" identity  ") +
		shell.Seg(shell.HexSecondary, shell.HexText, " ~/path ") +
		shell.Muted(" where you are  ") +
		shell.Seg(shell.HexGrid, shell.HexTertiary, " main ") +
		shell.Muted(" git branch")
	right := shell.Seg(shell.HexGrid, shell.HexTertiary, " 12:00:00 ") +
		shell.Muted(" clock  ") +
		shell.Seg(shell.HexPrimary, shell.HexVoid, " Helix ") +
		shell.Seg(shell.HexRectifier, shell.HexText, " Red Team ") +
		shell.Muted(" mode  ") +
		shell.Seg(shell.HexSecondary, shell.HexText, " you ") +
		shell.Muted(" who is asking")

	w := shell.KVWidth("LEFT", "RIGHT", "PROMPT")
	fmt.Println(shell.KV("LEFT", left, w))
	fmt.Println(shell.KV("RIGHT", right, w))
	fmt.Println(shell.KV("PROMPT", shell.Fg(shell.HexRectifier, "❯")+
		shell.Muted("  red means Helix is waiting for you; the branch block "+
			"appears only inside a git repository"), w))
}

// printCommandDetail renders one command's expanded help.
func printCommandDetail(name string) {
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	cmd, ok := lookupCommand(name)
	if !ok {
		// Same screen as a mistyped command at the prompt. It used to be a
		// separate, plainer one, so the identical mistake looked different
		// depending on which door you came through.
		printUnknownCommand(name)
		return
	}

	// The panel, like every other report. This drew its own 60-column rule
	// under a title that could be 64 columns wide, so the underline was
	// visibly shorter than the thing it underlined.
	fmt.Println(shell.PanelTitle(strings.TrimPrefix(cmd.Name, "/")))

	w := shell.KVWidth("USAGE", "ALIASES", "CATEGORY")
	fmt.Println(shell.KV("USAGE", shell.Value(cmd.UsageLine()), w))
	if len(cmd.Aliases) > 0 {
		fmt.Println(shell.KV("ALIASES",
			shell.Muted(strings.Join(cmd.Aliases, "  ·  ")), w))
	}
	fmt.Println(shell.KV("CATEGORY", shell.Muted(cmd.Category), w))
	fmt.Println(shell.PanelGap())
	for _, l := range shell.PanelWrap(cmd.Summary, func(t string) string {
		return shell.Fg(shell.HexText, t)
	}) {
		fmt.Println(l)
	}

	if len(cmd.Detail) > 0 {
		fmt.Println(shell.PanelGap())
		for _, line := range cmd.Detail {
			if line == "" {
				fmt.Println(shell.PanelGap())
				continue
			}
			// Detail lines are hand-laid tables in several commands
			// (/blackbox's subcommand list is a padded column), so they are
			// printed as written rather than re-wrapped, which would destroy
			// the alignment their author put there.
			fmt.Println(shell.PanelLine(shell.Fg(shell.HexText, line)))
		}
	}
	fmt.Println(shell.PanelEnd())
	fmt.Println()
}

func helpLine(cmd, desc string) {
	// Truncate to the shared column, then let KV align and wrap.
	//
	// The old version padded to a fixed 30 and clamped the pad at 2 when the
	// command was longer — so nine commands started their description at a
	// different column from the other forty-seven, which is what made the index
	// look like it was overlapping. And nothing wrapped: the widest row was 124
	// columns against a 76-column rule, so it broke at the terminal edge and
	// restarted outside the gutter.
	w := helpIndexWidth()
	fmt.Println(shell.KV(
		shell.Fg(shell.HexAmber, shell.Truncate(cmd, w)),
		shell.Fg(shell.HexText, desc),
		w,
	))
}

// -------------------------------------------------------
// Unknown command → did-you-mean
// -------------------------------------------------------

func handleUnknownSlashCommand(cmd string) {
	audio.PlayError()
	printUnknownCommand(cmd)
}

// printUnknownCommand is the single "no such command" screen.
//
// One renderer, because there were two and they disagreed: typing "/nosuch"
// produced a red "⚠ UNRECOGNIZED SIGNAL" with gutter bars, while
// "/help nosuch" produced two bare indented lines with no gutter at all — the
// same error, two presentations, neither framed. The gutter bars were the worse
// half: a gutter is the inside edge of a panel, and there was no panel, so they
// were an edge belonging to nothing.
//
// Deliberately short. This is the most-printed error in the shell — every typo
// reaches it — so it says what was not found, the one thing most likely to have
// been meant, and where the list is. Nothing else.
func printUnknownCommand(cmd string) {
	fmt.Println(shell.PanelTitle("unknown command"))

	w := shell.KVWidth("TYPED", "MOVED", "DID YOU MEAN", "ALL COMMANDS")
	fmt.Println(shell.KV("TYPED", shell.Badge(shell.StateBad, cmd), w))

	// A verb that worked yesterday deserves better than "did you mean". The
	// eight voice/vision commands folded into /blackbox, and this is where a
	// user's muscle memory lands.
	if note, moved := blackBoxMigrationNote(cmd); moved {
		fmt.Println(shell.KV("MOVED", shell.Value(note), w))
		fmt.Println(shell.PanelEnd())
		return
	}

	suggestions := suggestCommands(cmd, 3)
	if len(suggestions) > 0 {
		// Colour each item, do NOT wrap the joined string.
		// shell.Value(strings.Join(items, shell.Muted(sep))) looks right and is
		// not: the separator's reset terminates the outer colour, so only the
		// FIRST suggestion renders styled and the rest come out plain.
		coloured := make([]string, len(suggestions))
		for i, sg := range suggestions {
			coloured[i] = shell.Value(sg)
		}
		fmt.Println(shell.KV("DID YOU MEAN",
			strings.Join(coloured, shell.Muted("  ·  ")), w))
	}
	// Pointing at /help twice is noise when /help is itself the suggestion,
	// which is exactly what a mistyped "/hel" produces.
	if !slices.Contains(suggestions, "/help") {
		fmt.Println(shell.KV("ALL COMMANDS", shell.Muted("/help"), w))
	}
	fmt.Println(shell.PanelEnd())
}

// suggestCommands returns up to max close matches for a mistyped command.
//
// Prefix and substring matches come first (a truncated or half-remembered name
// is the common case), then edit distance for genuine typos.
func suggestCommands(input string, max int) []string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" || max <= 0 {
		return nil
	}
	stem := strings.TrimPrefix(input, "/")
	if stem == "" {
		return nil
	}

	type scored struct {
		name  string
		score int // lower is better
	}
	var candidates []scored
	seen := map[string]bool{}

	for _, cmd := range registry {
		if cmd.Hidden || seen[cmd.Name] {
			continue
		}
		name := strings.TrimPrefix(cmd.Name, "/")
		best := -1
		switch {
		case strings.HasPrefix(name, stem):
			best = 0
		case strings.Contains(name, stem), strings.Contains(stem, name):
			best = 1
		default:
			// The threshold scales with the typed length so short names do not
			// match everything: "ls" should not suggest half the registry.
			d := editDistance(stem, name)
			limit := 2
			if len(stem) <= 4 {
				limit = 1
			}
			if d <= limit {
				best = 2 + d
			}
		}
		if best >= 0 {
			candidates = append(candidates, scored{name: cmd.Name, score: best})
			seen[cmd.Name] = true
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score < candidates[j].score
		}
		return candidates[i].name < candidates[j].name
	})
	out := make([]string, 0, max)
	for _, c := range candidates {
		if len(out) == max {
			break
		}
		out = append(out, c.name)
	}
	return out
}

// editDistance is the restricted Damerau-Levenshtein distance over runes:
// insertions, deletions, substitutions, and ADJACENT TRANSPOSITIONS each cost 1.
//
// Transposition has to cost 1 rather than 2, because a swapped pair is the most
// common typo there is. Under plain Levenshtein "/hlep" sits distance 2 from
// "/help" — past the threshold a four-letter stem can safely use — so the
// suggester stayed silent on the single most likely mistake.
//
// Three rolling rows: O(len(a)·len(b)) time, O(len(b)) space.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}

	prev2 := make([]int, len(br)+1) // row i-2
	prev := make([]int, len(br)+1)  // row i-1
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			best := min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				if t := prev2[j-2] + 1; t < best {
					best = t
				}
			}
			cur[j] = best
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
