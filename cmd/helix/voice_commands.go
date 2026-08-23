// cmd/helix/voice_commands.go
// Purpose: reach the whole command surface by speaking.
//
// The gap this closes: a spoken transcript never contains a "/", so in voice
// mode the entire slash-command surface was unreachable. Voice could hold a
// conversation and run planner-generated shell steps, but it could not ask for
// /status, look at a /diff, add a /todo, run a /plan, or search the /web —
// which is most of what the harness is for.
//
// Two rules shape the design:
//
//  1. Default-deny (ADR-005). Voice is an untrusted channel: a transcript
//     carries user authority with no proof of who spoke. Only commands marked
//     VoiceOK in the registry are reachable, and a refusal is SPOKEN, so a
//     misheard phrase never silently does something else.
//  2. Speak the outcome. A command whose entire output goes to the terminal is
//     useless to someone not looking at it, so each route can contribute a
//     short spoken answer read from live state.
package main

import (
	"fmt"
	"strconv"
	"strings"

	"helix/internal/ai"
	"helix/internal/session"
)

// voiceRoute maps spoken phrases onto a command invocation.
type voiceRoute struct {
	// Phrases are spoken prefixes, matched longest-first after normalization.
	Phrases []string

	// Command is the slash command to run. Anything the user said after the
	// matched phrase is appended as arguments.
	Command string

	// FixedArgs are inserted before the spoken remainder, so one command can
	// back several phrases ("mark task 3 done" → /todo done 3).
	FixedArgs string

	// ArgsFirst puts the spoken remainder BEFORE FixedArgs. Needed for phrases
	// where the argument comes mid-sentence ("mark task three as done").
	ArgsFirst bool

	// RequiresArg refuses the route when nothing was said after the phrase,
	// rather than running a command that would just print its usage.
	RequiresArg bool

	// ArgLimit caps how many spoken words become arguments (0 = all).
	//
	// Needed because English puts the verb at both ends: "mark task three DONE"
	// leaves "3 done" after the phrase "mark task", and passing that to
	// /todo done produced "/todo done 3 done". Limiting to one word takes the
	// ID and discards the echo.
	ArgLimit int

	// Speak returns the spoken answer, read from live state after the command
	// ran. Nil falls back to a generic acknowledgement.
	Speak func() string
}

// voiceRoutes is the spoken vocabulary.
//
// Phrases are deliberately plain English rather than command names: someone
// speaking to a shell says "what's it costing me", not "slash cost". The
// literal "slash <name>" form is handled separately, as an escape hatch for
// anything not phrased here.
func voiceRoutes() []voiceRoute {
	return []voiceRoute{
		// --- state and orientation ---
		{
			Phrases: []string{"status", "grid status", "system status", "what's your status", "how are you doing"},
			Command: "/status",
			Speak:   spokenStatus,
		},
		{
			Phrases: []string{"run diagnostics", "diagnostics", "doctor", "self test", "check yourself"},
			Command: "/doctor",
			Speak:   func() string { return "Diagnostics finished. The terminal has the full report." },
		},
		{
			Phrases: []string{"what's this costing", "what is this costing", "what did that cost",
				"how many tokens", "usage report", "cost report", "show me the cost"},
			Command: "/cost",
			Speak:   spokenCost,
		},
		{
			Phrases: []string{"what do you remember", "show your memory", "conversation memory", "memory"},
			Command: "/memory",
			Speak:   spokenMemory,
		},
		{
			Phrases: []string{"how much context", "context budget", "context report", "context"},
			Command: "/context",
			Speak:   spokenMemory,
		},
		{
			Phrases: []string{"what tools do you have", "list your tools", "what can you do", "tools"},
			Command: "/tools",
			Speak:   spokenTools,
		},
		{
			Phrases: []string{"what version", "version"},
			Command: "/version",
		},
		{
			Phrases: []string{"are you online", "check connectivity", "are we online"},
			Command: "/online",
		},

		// --- the harness ---
		{
			Phrases:     []string{"plan", "make a plan for", "make a plan to", "how would you", "what would you do to"},
			Command:     "/plan",
			RequiresArg: true,
			Speak: func() string {
				return "That is the plan. Nothing ran. Say it again without the word plan to execute it."
			},
		},
		{
			Phrases: []string{"what's my approval mode", "what is my approval mode",
				"permission mode", "approval mode", "what are you allowed to do"},
			Command: "/permissions",
			Speak:   spokenPermission,
		},
		{
			Phrases: []string{"turn on agentic mode", "enable agentic mode", "agentic on", "self correct"},
			Command: "/agentic", FixedArgs: "on",
			Speak: func() string { return "Agentic mode on. I will observe results and correct myself." },
		},
		{
			Phrases: []string{"turn off agentic mode", "disable agentic mode", "agentic off"},
			Command: "/agentic", FixedArgs: "off",
			Speak: func() string { return "Agentic mode off. Single-shot planning." },
		},
		{
			Phrases: []string{"what's on my list", "what is on my list", "read my tasks", "read my todo",
				"my task list", "task list", "todo list", "what am i working on"},
			Command: "/todo",
			Speak:   spokenTodos,
		},
		{
			Phrases:     []string{"add a task", "add task", "new task", "remind me to", "put on my list"},
			Command:     "/todo",
			FixedArgs:   "add",
			RequiresArg: true,
			Speak:       spokenTodoCount,
		},
		{
			Phrases:     []string{"mark task", "finish task", "complete task", "task done"},
			Command:     "/todo",
			FixedArgs:   "done",
			ArgLimit:    1,
			RequiresArg: true,
			Speak:       spokenTodoCount,
		},
		{
			Phrases:     []string{"start task", "begin task", "working on task"},
			Command:     "/todo",
			FixedArgs:   "start",
			ArgLimit:    1,
			RequiresArg: true,
			Speak:       spokenTodoCount,
		},
		{
			Phrases:     []string{"block task", "task is blocked"},
			Command:     "/todo",
			FixedArgs:   "block",
			ArgLimit:    1,
			RequiresArg: true,
		},
		{
			Phrases: []string{"undo that", "undo", "undo the last thing", "take that back", "revert that"},
			Command: "/undo",
		},
		{
			Phrases: []string{"preview only", "don't actually run", "dry run", "enable dry run"},
			Command: "/dry-run",
		},

		// --- repository ---
		{
			Phrases: []string{"what changed", "show me the diff", "what did i change", "diff"},
			Command: "/diff",
			Speak:   func() string { return "The diff is on screen." },
		},
		{
			Phrases: []string{"review my changes", "review the diff", "code review", "review"},
			Command: "/review",
			Speak:   nil, // /review speaks its own answer through PrintAnswer
		},
		{
			Phrases:     []string{"search the web for", "look up", "search for", "google", "web search"},
			Command:     "/web",
			RequiresArg: true,
		},
		{
			Phrases:     []string{"explain", "what does this do", "is this safe"},
			Command:     "/explain",
			RequiresArg: true,
		},
		{
			Phrases:     []string{"look up vulnerability", "look up cve", "vulnerability report", "check cve"},
			Command:     "/vuln",
			RequiresArg: true,
		},

		// --- session ---
		{
			Phrases: []string{"summarize this conversation", "compact the conversation", "compact"},
			Command: "/compact",
			Speak:   func() string { return "The terminal is showing the summary for you to approve." },
		},
		{
			Phrases: []string{"save this conversation", "export the transcript", "export this"},
			Command: "/export",
			Speak:   func() string { return "Transcript exported. The path is on screen." },
		},

		// --- perception and speech ---
		{
			Phrases: []string{"open your eyes", "eyes on", "turn on your eyes", "start watching"},
			Command: "/blackbox", FixedArgs: "eyes on",
		},
		{
			Phrases: []string{"what do you see", "look at this", "look at the screen",
				"describe what you see", "take a look"},
			Command: "/blackbox", FixedArgs: "look",
			// The remainder becomes the question, so "look at this error message"
			// asks about the error rather than describing the room.
		},
		{
			Phrases: []string{"stop talking", "be quiet", "mute yourself", "stop speaking"},
			Command: "/blackbox", FixedArgs: "tts off",
			Speak: func() string { return "" }, // silence is the acknowledgement
		},
		{
			Phrases: []string{"start talking", "speak to me", "unmute"},
			Command: "/blackbox", FixedArgs: "tts on",
			Speak: func() string { return "Spoken replies back on." },
		},
		{
			Phrases: []string{"how's the microphone", "how is the microphone", "test the microphone",
				"microphone test", "can you hear me"},
			Command: "/mictest",
		},
		{
			Phrases: []string{"voice status", "speech status", "how's your voice", "how is your voice"},
			Command: "/blackbox", FixedArgs: "status",
		},
		{
			Phrases: []string{"stop listening for the wake word", "wake word off", "disable wake word"},
			Command: "/blackbox", FixedArgs: "wake off",
		},
		{
			Phrases: []string{"listen for the wake word", "wake word on", "enable wake word", "hands free"},
			Command: "/blackbox", FixedArgs: "wake on",
		},
	}
}

// matchVoiceCommand translates a spoken utterance into a slash-command line.
//
// Returns the command line, a spoken-answer function, and whether the utterance
// was a command at all. A false result means "this is ordinary input" — the
// planner handles it exactly as before.
func matchVoiceCommand(text string) (string, func() string, bool) {
	norm := normalizeSpoken(text)
	if norm == "" {
		return "", nil, false
	}

	// The literal escape hatch first: "slash status", "command tools". This is
	// how someone reaches a command that has no spoken phrase, and it must beat
	// phrase matching so "slash plan" is never read as the word "plan".
	if line, ok := matchSpokenSlashForm(norm); ok {
		return line, nil, true
	}

	best := -1
	var bestRoute voiceRoute
	var bestRest string

	routes := voiceRoutes()
	for i := range routes {
		for _, phrase := range routes[i].Phrases {
			rest, ok := matchPhrasePrefix(norm, phrase)
			if !ok {
				continue
			}
			// Longest phrase wins: "add a task" must beat a bare "task", and
			// "turn off agentic mode" must beat "agentic on".
			if len(phrase) > best {
				best = len(phrase)
				bestRoute = routes[i]
				bestRest = rest
			}
		}
	}
	if best < 0 {
		return "", nil, false
	}

	rest := strings.TrimSpace(stripFiller(bestRest))
	if bestRoute.ArgLimit > 0 {
		if fields := strings.Fields(rest); len(fields) > bestRoute.ArgLimit {
			rest = strings.Join(fields[:bestRoute.ArgLimit], " ")
		}
	}
	if bestRoute.RequiresArg && rest == "" {
		return "", nil, false
	}

	line := bestRoute.Command
	switch {
	case bestRoute.FixedArgs != "" && bestRoute.ArgsFirst:
		line = strings.TrimSpace(line + " " + rest + " " + bestRoute.FixedArgs)
	case bestRoute.FixedArgs != "":
		line = strings.TrimSpace(line + " " + bestRoute.FixedArgs + " " + rest)
	default:
		line = strings.TrimSpace(line + " " + rest)
	}
	return line, bestRoute.Speak, true
}

// matchSpokenSlashForm handles the explicit "slash <command> [args]" form.
func matchSpokenSlashForm(norm string) (string, bool) {
	for _, prefix := range []string{"slash ", "command ", "run command "} {
		if !strings.HasPrefix(norm, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(norm, prefix))
		if rest == "" {
			return "", false
		}
		fields := strings.Fields(rest)
		// STT renders hyphenated names as separate words ("voice status",
		// "dry run"), so try the multi-word joins before the single word.
		for take := min(3, len(fields)); take >= 1; take-- {
			name := strings.Join(fields[:take], "-")
			if _, ok := lookupCommand("/" + name); ok {
				return strings.TrimSpace("/" + name + " " + strings.Join(fields[take:], " ")), true
			}
		}
		// Also try the words as one token ("ragstatus" is unlikely, but
		// "voicestatus" comes up).
		if _, ok := lookupCommand("/" + strings.Join(fields, "")); ok {
			return "/" + strings.Join(fields, ""), true
		}
		return "", false
	}
	return "", false
}

// matchPhrasePrefix reports whether norm starts with phrase on a word boundary,
// returning the remainder.
//
// Word-boundary matching matters: without it "status" would match inside
// "statuses", and "plan" inside "planning a trip".
func matchPhrasePrefix(norm, phrase string) (string, bool) {
	if norm == phrase {
		return "", true
	}
	if strings.HasPrefix(norm, phrase+" ") {
		return norm[len(phrase)+1:], true
	}
	return "", false
}

// spokenFiller are words that survive normalization but carry no argument
// meaning, so they must not become part of a command's arguments.
var spokenFiller = []string{"please", "for me", "the", "a", "an", "as", "to", "is", "now"}

// stripFiller removes leading filler from a spoken argument. Only LEADING words
// are removed — "the" inside a search query is part of the query.
func stripFiller(rest string) string {
	for {
		trimmed := strings.TrimSpace(rest)
		changed := false
		for _, f := range spokenFiller {
			if trimmed == f {
				return ""
			}
			if strings.HasPrefix(trimmed, f+" ") {
				rest = trimmed[len(f)+1:]
				changed = true
				break
			}
		}
		if !changed {
			return trimmed
		}
	}
}

// spokenNumbers maps number words to digits, because a task ID spoken as
// "three" has to become "3" for /todo done to find it. STT engines differ on
// whether they emit digits or words for small numbers, so both must work.
var spokenNumbers = map[string]string{
	"zero": "0", "one": "1", "two": "2", "three": "3", "four": "4", "five": "5",
	"six": "6", "seven": "7", "eight": "8", "nine": "9", "ten": "10",
	"eleven": "11", "twelve": "12", "thirteen": "13", "fourteen": "14",
	"fifteen": "15", "sixteen": "16", "seventeen": "17", "eighteen": "18",
	"nineteen": "19", "twenty": "20",
}

// normalizeSpoken lowercases, strips punctuation, collapses whitespace, and
// converts number words to digits.
func normalizeSpoken(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))

	// Punctuation is dropped EXCEPT where it sits between two alphanumerics.
	// That distinction is load-bearing: a trailing period must go so "status."
	// matches the phrase "status", while the period in "go 1.25", the dot in
	// "config.json", and the slash in "cmd/helix" are part of what the user
	// asked about and must survive into the command's arguments.
	runes := []rune(lower)
	var b strings.Builder
	for i, r := range runes {
		switch {
		case isSpokenWordRune(r):
			b.WriteRune(r)
		case r == '\'':
			b.WriteRune(r) // keep contractions: "what's" must stay one word
		case isInnerPunct(r) && i > 0 && i < len(runes)-1 &&
			isSpokenWordRune(runes[i-1]) && isSpokenWordRune(runes[i+1]):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}

	fields := strings.Fields(b.String())
	for i, f := range fields {
		if digit, ok := spokenNumbers[f]; ok {
			fields[i] = digit
		}
	}
	return strings.Join(fields, " ")
}

func isSpokenWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// isInnerPunct lists the characters that carry meaning inside a token: version
// numbers, filenames, paths, and hyphenated identifiers.
func isInnerPunct(r rune) bool {
	switch r {
	case '.', '-', '_', '/', ':':
		return true
	}
	return false
}

// voiceCommandAllowed reports whether a command line may run from the voice
// channel, with the reason when it may not.
func voiceCommandAllowed(line string) (bool, string) {
	verb := line
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		verb = line[:i]
	}
	cmd, ok := lookupCommand(verb)
	if !ok {
		return false, fmt.Sprintf("%s is not a command I know", verb)
	}
	if !cmd.VoiceOK {
		return false, fmt.Sprintf(
			"%s cannot be run by voice. Type it in the terminal.", cmd.Name)
	}
	if cmd.VoiceReadOnly && strings.TrimSpace(strings.TrimPrefix(line, verb)) != "" {
		return false, fmt.Sprintf(
			"I can tell you what %s is, but changing it has to be typed.", cmd.Name)
	}
	if voiceStartsTranscriptLog(line) {
		return false, "Starting a transcript log has to be typed. " +
			"I can stop one whenever you ask."
	}
	return true, ""
}

// voiceStartsTranscriptLog reports whether a spoken command would BEGIN
// recording transcripts to disk.
//
// ADR-005 keeps /config and /stealth off the voice surface because they move
// the approval or privacy posture, and switching on a store of everything the
// microphone hears is squarely that. But the deny list is per-command and
// /blackbox has to stay voice-reachable — the "manual mode" safety valve lives
// on it — so the rule is applied to the subcommand instead of the command.
//
// The asymmetry is deliberate and matches the camera: voice may always turn
// recording OFF, because a privacy control should fail toward collecting less.
func voiceStartsTranscriptLog(line string) bool {
	fields := strings.Fields(strings.ToLower(line))
	if len(fields) < 3 {
		return false
	}
	if fields[0] != "/blackbox" && fields[0] != "/bb" {
		return false
	}
	switch fields[1] {
	case "log", "logs", "transcript":
	default:
		return false
	}
	return fields[2] == "on" || fields[2] == "enable"
}

// -------------------------------------------------------
// spoken answers
// -------------------------------------------------------

func spokenStatus() string {
	parts := []string{fmt.Sprintf("Running %s", ai.ActiveModel())}
	if agentCore != nil {
		parts = append(parts, fmt.Sprintf("approval mode %s", agentCore.Permission()))
		if agentCore.Agentic {
			parts = append(parts, "agentic mode on")
		}
	}
	if todoList != nil {
		counts := todoList.Counts()
		open := counts[session.TodoPending] + counts[session.TodoInProgress] + counts[session.TodoBlocked]
		if open > 0 {
			parts = append(parts, fmt.Sprintf("%d open task%s", open, plural(open)))
		}
	}
	return strings.Join(parts, ", ") + "."
}

func spokenCost() string {
	rep := ai.Usage()
	if rep.Calls == 0 {
		return "No model calls yet this session."
	}
	// Spoken, so round hard: "about eleven thousand" beats "11,431".
	return fmt.Sprintf("%d model call%s this session, roughly %s estimated tokens.%s",
		rep.Calls, plural(rep.Calls), roundedTokens(rep.EstTotalTokens()),
		failureNote(rep.Failures))
}

func failureNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" %d call%s failed.", n, plural(n))
}

// roundedTokens renders a token count the way a person would say it.
func roundedTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1f million", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%d thousand", n/1_000)
	default:
		return strconv.FormatInt(n, 10)
	}
}

func spokenMemory() string {
	if agentCore == nil || agentCore.Session == nil {
		return "Conversation memory is not available."
	}
	n := agentCore.Session.Len()
	if n == 0 {
		return "I have nothing in conversation memory yet."
	}
	return fmt.Sprintf("%d turn%s in memory, out of %d.",
		n, plural(n), agentCore.Session.Capacity())
}

func spokenPermission() string {
	if agentCore == nil {
		return "The agent is not available."
	}
	mode := agentCore.Permission()
	return fmt.Sprintf("Approval mode is %s. %s. Remember that by voice I am capped at medium risk whatever the mode.",
		mode, mode.Describe())
}

func spokenTools() string {
	return "I can run shell commands, git operations, package installs, authorized " +
		"reconnaissance, and read-only web searches. Every one goes through the " +
		"safety pipeline. The terminal lists them with their gates."
}

func spokenTodos() string {
	if todoList == nil {
		return "The task list is not available."
	}
	items := todoList.Items()
	var open []session.TodoItem
	for _, it := range items {
		if it.State != session.TodoDone {
			open = append(open, it)
		}
	}
	if len(open) == 0 {
		return "Nothing open on your list."
	}

	// Read at most three aloud; a long list read in full is unusable.
	const readAloud = 3
	spoken := open
	if len(spoken) > readAloud {
		spoken = spoken[:readAloud]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d open task%s. ", len(open), plural(len(open)))
	for _, it := range spoken {
		fmt.Fprintf(&b, "Task %d, %s. ", it.ID, it.Text)
	}
	if len(open) > readAloud {
		fmt.Fprintf(&b, "And %d more on screen.", len(open)-readAloud)
	}
	return b.String()
}

func spokenTodoCount() string {
	if todoList == nil {
		return ""
	}
	counts := todoList.Counts()
	open := counts[session.TodoPending] + counts[session.TodoInProgress] + counts[session.TodoBlocked]
	return fmt.Sprintf("Done. %d task%s open.", open, plural(open))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// -------------------------------------------------------
// dispatch
// -------------------------------------------------------

// dispatchVoiceCommand runs a spoken command and speaks the outcome.
//
// Returns true when the utterance was handled as a command (including a
// refusal), so the caller skips the planner entirely.
func dispatchVoiceCommand(text string) bool {
	line, speakFn, ok := matchVoiceCommand(text)
	if !ok {
		return false
	}

	if allowed, reason := voiceCommandAllowed(line); !allowed {
		// Spoken AND printed: a refusal the user cannot hear is the same as no
		// response at all when they are not looking at the terminal.
		printVoiceNotice(reason)
		speakNotice(reason)
		return true
	}

	printVoiceNotice("voice command → " + line)
	if !handleSlashCommand(line) {
		return false
	}

	if speakFn != nil {
		if answer := strings.TrimSpace(speakFn()); answer != "" {
			speakNotice(answer)
		}
		return true
	}
	// Commands that print their own answer through the agent (like /review)
	// have already spoken; the rest get a short acknowledgement so the user is
	// not left wondering whether anything happened.
	if !commandSpeaksForItself(line) {
		speakNotice("Done. The terminal has the details.")
	}
	return true
}

// commandSpeaksForItself reports whether the command routes its answer through
// the agent's own spoken-reply seam, so a second acknowledgement would talk over
// it.
func commandSpeaksForItself(line string) bool {
	switch {
	case strings.HasPrefix(line, "/review"),
		strings.HasPrefix(line, "/explain"),
		strings.HasPrefix(line, "/web"),
		strings.HasPrefix(line, "/blackbox"),
		strings.HasPrefix(line, "/bb"),
		strings.HasPrefix(line, "/mictest"):
		return true
	}
	return false
}

// speakNotice speaks a line regardless of the /tts toggle.
//
// The toggle governs whether ordinary REPLIES are spoken. A voice-command
// acknowledgement is different: the user just spoke to a shell they may not be
// looking at, and leaving that unanswered is the failure mode. The exception is
// the command that turned speech off — see the empty Speak on that route.
func speakNotice(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if !voiceModeActive {
		return
	}
	speakDirect(text)
}

// printVoiceNotice echoes what the voice channel decided, so a transcript of the
// session shows the translation from speech to command.
func printVoiceNotice(text string) {
	fmt.Printf("[voice] %s\n", text)
}

// voiceCommandVocabulary lists the spoken phrases, for /blackbox status and docs.
func voiceCommandVocabulary() []string {
	routes := voiceRoutes()
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		target := r.Command
		if r.FixedArgs != "" {
			target += " " + r.FixedArgs
		}
		out = append(out, fmt.Sprintf("%-34s %s", "\""+r.Phrases[0]+"\"", target))
	}
	return out
}

// assertVoiceRoutesValid is called from init to fail the build's tests — not the
// user's session — when a route names a command that does not exist or is not
// voice-reachable. A route pointing at a non-VoiceOK command would refuse at
// runtime, which is a silently useless phrase.
func assertVoiceRoutesValid() []error {
	var errs []error
	for _, r := range voiceRoutes() {
		if len(r.Phrases) == 0 {
			errs = append(errs, fmt.Errorf("route for %s has no phrases", r.Command))
		}
		cmd, ok := lookupCommand(r.Command)
		if !ok {
			errs = append(errs, fmt.Errorf("route %q names unknown command %s", r.Phrases[0], r.Command))
			continue
		}
		if !cmd.VoiceOK {
			errs = append(errs, fmt.Errorf(
				"route %q targets %s, which is not VoiceOK — it would always be refused",
				r.Phrases[0], cmd.Name))
		}
		for _, p := range r.Phrases {
			if p != normalizeSpoken(p) {
				errs = append(errs, fmt.Errorf(
					"phrase %q is not in normalized form (want %q) and can never match",
					p, normalizeSpoken(p)))
			}
		}
	}
	return errs
}
