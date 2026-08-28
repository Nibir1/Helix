package main

import (
	"strings"
	"testing"
)

// TestVoiceRoutesAreValid is the invariant that keeps the spoken vocabulary from
// containing phrases that can never work: a route naming a command that does not
// exist, or one that is not VoiceOK (and so would always be refused).
func TestVoiceRoutesAreValid(t *testing.T) {
	for _, err := range assertVoiceRoutesValid() {
		t.Error(err)
	}
}

func TestNormalizeSpoken(t *testing.T) {
	cases := map[string]string{
		"What's my status?":           "what's my status",
		"  STATUS  ":                  "status",
		"Add a task: fix the parser.": "add a task fix the parser",
		"mark task three done":        "mark task 3 done",
		"Task Seven is blocked":       "task 7 is blocked",
		"":                            "",
		"!!!":                         "",
		"multiple   inner    spaces":  "multiple inner spaces",
	}
	for in, want := range cases {
		if got := normalizeSpoken(in); got != want {
			t.Errorf("normalizeSpoken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchVoiceCommand(t *testing.T) {
	cases := []struct {
		spoken string
		want   string
	}{
		{"status", "/status"},
		{"what's your status", "/status"},
		{"grid status", "/status"},
		{"what's this costing", "/cost"},
		{"what's on my list", "/todo"},
		{"add a task fix the parser", "/todo add fix the parser"},
		{"add a task please fix the parser", "/todo add fix the parser"},
		{"remind me to call the vendor", "/todo add call the vendor"},
		{"mark task three done", "/todo done 3"},
		{"start task 2", "/todo start 2"},
		{"turn on agentic mode", "/agentic on"},
		{"turn off agentic mode", "/agentic off"},
		{"search the web for go 1.25 release notes", "/web go 1.25 release notes"},
		{"look up the raft consensus paper", "/web raft consensus paper"},
		{"plan a migration of the config loader", "/plan migration of the config loader"},
		{"what changed", "/diff"},
		{"review my changes", "/review"},
		{"undo that", "/undo"},
		{"what do you see", "/blackbox look"},
		{"look at this stack trace", "/blackbox look stack trace"},
		{"stop talking", "/blackbox tts off"},
		{"run diagnostics", "/doctor"},
		{"what tools do you have", "/tools"},
	}
	for _, tc := range cases {
		got, _, ok := matchVoiceCommand(tc.spoken)
		if !ok {
			t.Errorf("%q was not recognized as a command", tc.spoken)
			continue
		}
		if got != tc.want {
			t.Errorf("%q → %q, want %q", tc.spoken, got, tc.want)
		}
	}
}

// TestMatchVoiceCommandPrefersLongestPhrase: "add a task X" must not be read as
// the bare "task list" route, and "turn off agentic mode" must not match the
// shorter "agentic on".
func TestMatchVoiceCommandPrefersLongestPhrase(t *testing.T) {
	got, _, ok := matchVoiceCommand("task list")
	if !ok || got != "/todo" {
		t.Errorf("\"task list\" → %q (ok=%v), want /todo", got, ok)
	}
	got, _, ok = matchVoiceCommand("add a task write the docs")
	if !ok || got != "/todo add write the docs" {
		t.Errorf("longest phrase did not win: %q", got)
	}
}

// TestMatchVoiceCommandIgnoresOrdinarySpeech is the most important negative
// test: ordinary requests must still reach the planner, not be hijacked into a
// command.
func TestMatchVoiceCommandIgnoresOrdinarySpeech(t *testing.T) {
	ordinary := []string{
		"list the files in this directory",
		"create a python script that prints hello",
		"why is my build failing",
		"install ripgrep",
		"commit my changes with a good message",
		"delete the temp folder",
		"what is the capital of France",
		"",
		"   ",
	}
	for _, text := range ordinary {
		if line, _, ok := matchVoiceCommand(text); ok {
			t.Errorf("%q was hijacked into the command %q; it must reach the planner", text, line)
		}
	}
}

// TestVoiceCommandRequiringArgFallsThrough: "plan" alone is a word someone might
// say in conversation, so without an argument it must not become /plan.
func TestVoiceCommandRequiringArgFallsThrough(t *testing.T) {
	for _, text := range []string{"plan", "search for", "explain", "look up"} {
		if line, _, ok := matchVoiceCommand(text); ok {
			t.Errorf("%q became %q, but the route requires an argument", text, line)
		}
	}
}

func TestSpokenSlashForm(t *testing.T) {
	cases := map[string]string{
		"slash status":            "/status",
		"command tools":           "/tools",
		"run command doctor":      "/doctor",
		"slash blackbox status":   "/blackbox status",
		"slash provider status":   "/provider-status",
		"slash knowledge status":  "/knowledge-status",
		"slash dry run":           "/dry-run",
		"slash todo add buy milk": "/todo add buy milk",
		"slash rag status":        "/rag-status",
	}
	for in, want := range cases {
		got, _, ok := matchVoiceCommand(in)
		if !ok {
			t.Errorf("%q was not recognized", in)
			continue
		}
		if got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}

	// A nonexistent command must fall through to the planner rather than
	// dispatching something arbitrary.
	if line, _, ok := matchVoiceCommand("slash nonexistent thing"); ok {
		t.Errorf("unknown spoken command became %q", line)
	}
}

// TestVoiceCommandAllowedDefaultDeny is the security property: voice reaches
// only what was explicitly marked reachable.
func TestVoiceCommandAllowedDefaultDeny(t *testing.T) {
	// These must NEVER be reachable by voice: they destroy data, fire traffic at
	// someone else, move the approval posture, or change what runs unattended.
	//
	// The list shrank when live mode arrived (/blackbox on is meant to reach the
	// whole shell by speaking), and each removal was argued on its own merits
	// rather than as a batch — see the VoiceOK doc comment in registry.go.
	denied := []string{
		"/purge", "/config provider openai",
		"/hooks add", "/rag-reset", "/commit", "/init",
		"/setup", "/stealth on", "/scan authorize 10.0.0.1",
	}
	for _, line := range denied {
		ok, reason := voiceCommandAllowed(line)
		if ok {
			t.Errorf("%q is reachable by voice and must not be", line)
			continue
		}
		if reason == "" {
			t.Errorf("%q was refused with no explanation", line)
		}
	}

	// And a representative sample that must be reachable — including the ones
	// live mode opened up, which are reversible and change nothing unattended.
	allowed := []string{"/status", "/todo add x", "/plan do a thing", "/diff", "/web query", "/undo",
		"/resume", "/model use x", "/provider use x", "/typewrite-all on", "/knowledge-update",
		"/rag-rebuild", "/test-basic-ai", "/blackbox on"}
	for _, line := range allowed {
		if ok, reason := voiceCommandAllowed(line); !ok {
			t.Errorf("%q should be voice-reachable: %s", line, reason)
		}
	}
}

// TestEveryVoiceOKCommandExists guards the registry flag itself.
func TestEveryVoiceOKCommandExists(t *testing.T) {
	var voiceable int
	for _, cmd := range registry {
		if !cmd.VoiceOK {
			continue
		}
		voiceable++
		if cmd.Handler == nil {
			t.Errorf("%s is VoiceOK but has no handler", cmd.Name)
		}
	}
	if voiceable == 0 {
		t.Fatal("no command is voice-reachable — the spoken surface is empty")
	}

	// The destructive set must be flagged off, checked against the registry
	// directly so adding VoiceOK to one of these fails here.
	for _, name := range []string{"/purge", "/config", "/hooks", "/rag-reset", "/commit", "/init",
		"/setup", "/stealth", "/scan"} {
		cmd, ok := lookupCommand(name)
		if !ok {
			t.Fatalf("%s is missing from the registry", name)
		}
		if cmd.VoiceOK {
			t.Errorf("%s must not be VoiceOK: it is irreversible or changes what runs unattended", name)
		}
	}

	// Posture commands are readable but not settable by voice.
	for _, name := range []string{"/permissions", "/sandbox"} {
		cmd, ok := lookupCommand(name)
		if !ok {
			t.Fatalf("%s is missing from the registry", name)
		}
		if !cmd.VoiceReadOnly {
			t.Errorf("%s changes how much runs unattended; voice must be read-only on it", name)
		}
	}
}

// TestVoiceReadOnlyBlocksArgumentsOnly: asking is fine, setting is not. This is
// the single choke point for that rule — the dispatcher, not each handler.
func TestVoiceReadOnlyBlocksArgumentsOnly(t *testing.T) {
	readable := []string{"/permissions", "/sandbox"}
	for _, line := range readable {
		if ok, reason := voiceCommandAllowed(line); !ok {
			t.Errorf("%q should be readable by voice: %s", line, reason)
		}
	}
	settable := []string{"/permissions auto", "/permissions plan", "/sandbox off", "/sandbox strict"}
	for _, line := range settable {
		ok, reason := voiceCommandAllowed(line)
		if ok {
			t.Errorf("%q must not be settable by voice", line)
			continue
		}
		if !strings.Contains(reason, "typed") {
			t.Errorf("%q: refusal should say it must be typed, got %q", line, reason)
		}
	}
}

func TestStripFiller(t *testing.T) {
	cases := map[string]string{
		"please fix the parser": "fix the parser",
		"the parser":            "parser",
		"to call the vendor":    "call the vendor",
		"please":                "",
		"":                      "",
		// Only LEADING filler goes; the same words inside the argument are
		// content ("the folder" is part of what the user asked for).
		"find the file in the folder": "find the file in the folder",
		"the file in the folder":      "file in the folder",
	}
	for in, want := range cases {
		if got := stripFiller(in); got != want {
			t.Errorf("stripFiller(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRoundedTokens(t *testing.T) {
	cases := map[int64]string{
		0: "0", 42: "42", 999: "999",
		1_000: "1 thousand", 11_431: "11 thousand",
		1_500_000: "1.5 million",
	}
	for in, want := range cases {
		if got := roundedTokens(in); got != want {
			t.Errorf("roundedTokens(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestVoiceCommandVocabularyIsNonEmpty(t *testing.T) {
	vocab := voiceCommandVocabulary()
	if len(vocab) < 10 {
		t.Fatalf("spoken vocabulary has only %d entries", len(vocab))
	}
	for _, line := range vocab {
		if !strings.Contains(line, "/") {
			t.Errorf("vocabulary line names no command: %q", line)
		}
	}
}

func TestMatchPhrasePrefixRespectsWordBoundaries(t *testing.T) {
	if _, ok := matchPhrasePrefix("statuses of the pods", "status"); ok {
		t.Error("\"statuses\" must not match the phrase \"status\"")
	}
	if rest, ok := matchPhrasePrefix("status of the pods", "status"); !ok || rest != "of the pods" {
		t.Errorf("boundary match failed: rest=%q ok=%v", rest, ok)
	}
	if rest, ok := matchPhrasePrefix("status", "status"); !ok || rest != "" {
		t.Errorf("exact match failed: rest=%q ok=%v", rest, ok)
	}
}
