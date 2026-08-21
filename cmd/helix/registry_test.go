package main

import (
	"strings"
	"testing"
)

// TestRegistryInvariants guards the properties /help, tab completion, and the
// did-you-mean suggester all depend on. A table this size drifts silently
// otherwise — which is the exact failure the registry was built to end.
func TestRegistryInvariants(t *testing.T) {
	if len(registry) == 0 {
		t.Fatal("command registry is empty")
	}

	validCategory := map[string]bool{}
	for _, cat := range categoryOrder() {
		validCategory[cat] = true
	}

	seen := map[string]string{} // name/alias -> owning command
	for _, cmd := range registry {
		if !strings.HasPrefix(cmd.Name, "/") {
			t.Errorf("command %q must start with a slash", cmd.Name)
		}
		if strings.ContainsAny(cmd.Name, " \t") {
			t.Errorf("command %q must not contain whitespace", cmd.Name)
		}
		if cmd.Name != strings.ToLower(cmd.Name) {
			t.Errorf("command %q must be lowercase: dispatch lowercases the typed verb", cmd.Name)
		}
		if cmd.Handler == nil {
			t.Errorf("command %q has no handler", cmd.Name)
		}
		if strings.TrimSpace(cmd.Summary) == "" {
			t.Errorf("command %q has no summary; /help would show a blank row", cmd.Name)
		}
		if !validCategory[cmd.Category] {
			t.Errorf("command %q has category %q, which categoryOrder() does not list — "+
				"it would be omitted from /help entirely", cmd.Name, cmd.Category)
		}
		// The usage line must name the command it documents, or /help sends the
		// reader to the wrong place.
		if !strings.HasPrefix(cmd.UsageLine(), cmd.Name) {
			t.Errorf("command %q has usage %q, which does not start with the command name",
				cmd.Name, cmd.UsageLine())
		}

		for _, name := range append([]string{cmd.Name}, cmd.Aliases...) {
			if owner, dup := seen[name]; dup {
				t.Errorf("%q is claimed by both %s and %s", name, owner, cmd.Name)
			}
			seen[name] = cmd.Name
		}
	}
}

// TestRegistryLookupResolvesAliasesAndCase checks the two inputs the old
// TrimPrefix-based handlers got wrong.
func TestRegistryLookupResolvesAliasesAndCase(t *testing.T) {
	cases := []struct{ typed, want string }{
		{"/help", "/help"},
		{"/HELP", "/help"},
		{"/?", "/help"},
		{"/intel", "/vuln"},
		{"/INTEL", "/vuln"},
		{"/usage", "/cost"},
		{"/mode", "/permissions"},
	}
	for _, tc := range cases {
		cmd, ok := lookupCommand(tc.typed)
		if !ok {
			t.Errorf("%q did not resolve", tc.typed)
			continue
		}
		if cmd.Name != tc.want {
			t.Errorf("%q resolved to %s, want %s", tc.typed, cmd.Name, tc.want)
		}
	}
}

// TestParseCmdArgs covers the parsing that replaced TrimPrefix. The uppercase
// and alias cases are the actual historical bugs: "/CD /tmp" used to leave the
// target as the whole line, and "/intel CVE-1" never matched "/vuln".
func TestParseCmdArgs(t *testing.T) {
	cases := []struct {
		input      string
		wantName   string
		wantRest   string
		wantFields int
	}{
		{"/cd /tmp", "/cd", "/tmp", 1},
		{"/CD /tmp", "/cd", "/tmp", 1},
		{"  /cd   /tmp/deep  ", "/cd", "/tmp/deep", 1},
		{"/git commit everything now", "/git", "commit everything now", 3},
		{"/intel CVE-2024-1234", "/vuln", "CVE-2024-1234", 1},
		{"/help", "/help", "", 0},
		{"/help\tgit", "/help", "git", 1},
	}
	for _, tc := range cases {
		var got cmdArgs
		captured := false
		cmd, ok := lookupCommand(firstWord(tc.input))
		if !ok {
			t.Errorf("%q: verb did not resolve", tc.input)
			continue
		}
		// Swap in a capturing handler rather than reimplementing the parse, so
		// the test exercises the dispatcher's own splitting.
		orig := cmd.Handler
		cmd.Handler = func(c cmdArgs) { got, captured = c, true }
		handled := handleSlashCommand(tc.input)
		cmd.Handler = orig

		if !handled || !captured {
			t.Errorf("%q was not dispatched", tc.input)
			continue
		}
		if got.Name != tc.wantName {
			t.Errorf("%q: name = %q, want %q", tc.input, got.Name, tc.wantName)
		}
		if got.Rest != tc.wantRest {
			t.Errorf("%q: rest = %q, want %q", tc.input, got.Rest, tc.wantRest)
		}
		if got.Count() != tc.wantFields {
			t.Errorf("%q: %d fields, want %d", tc.input, got.Count(), tc.wantFields)
		}
	}
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// TestDispatchIgnoresPaths keeps absolute-path executables out of the command
// namespace: "/usr/bin/git --version" is a command to run, not a Helix verb.
func TestDispatchIgnoresPaths(t *testing.T) {
	for _, input := range []string{"/usr/bin/git --version", "/bin/ls", "/opt/tools/x"} {
		if handleSlashCommand(input) {
			t.Errorf("%q was claimed as a Helix command; it must fall through to the pipeline", input)
		}
	}
}

func TestCmdArgsAccessors(t *testing.T) {
	c := cmdArgs{Name: "/todo", Rest: "add Write The Tests", Fields: strings.Fields("add Write The Tests")}
	if c.Sub() != "add" {
		t.Errorf("Sub() = %q, want add", c.Sub())
	}
	if got := c.From(1); got != "Write The Tests" {
		t.Errorf("From(1) = %q, want %q", got, "Write The Tests")
	}
	if c.Arg(9) != "" {
		t.Errorf("Arg out of range should be empty, got %q", c.Arg(9))
	}
	if c.From(9) != "" {
		t.Errorf("From out of range should be empty, got %q", c.From(9))
	}
	if c.Empty() {
		t.Error("Empty() should be false when arguments are present")
	}
	if !(cmdArgs{}).Empty() {
		t.Error("Empty() should be true for a bare command")
	}
}

// TestSuggestCommands checks the did-you-mean quality on realistic typos. An
// unknown command with no suggestion is a dead end for the user.
func TestSuggestCommands(t *testing.T) {
	cases := []struct{ typed, want string }{
		{"/hlep", "/help"},
		{"/stat", "/status"},
		{"/comit", "/commit"},
		{"/permission", "/permissions"},
		{"/todos", "/todo"},
		{"/doctr", "/doctor"},
	}
	for _, tc := range cases {
		got := suggestCommands(tc.typed, 4)
		if !contains(got, tc.want) {
			t.Errorf("suggestCommands(%q) = %v, want it to include %s", tc.typed, got, tc.want)
		}
	}

	// A string with nothing in common must not produce noise.
	if got := suggestCommands("/zzzqqqxxwww", 3); len(got) != 0 {
		t.Errorf("suggestCommands on gibberish = %v, want none", got)
	}
	if got := suggestCommands("/", 3); len(got) != 0 {
		t.Errorf("suggestCommands on a bare slash = %v, want none", got)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestEditDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"abc", "abc", 0},
		{"help", "hlep", 1}, // adjacent transposition costs 1
		{"status", "stats", 1},
		{"café", "cafe", 1}, // rune-based, not byte-based
	}
	for _, tc := range cases {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestCommandNamesPublishedForCompletion ensures the line editor is handed the
// same names the dispatcher accepts.
func TestCommandNamesPublishedForCompletion(t *testing.T) {
	names := commandNames()
	if len(names) < len(registry) {
		t.Fatalf("published %d names for %d commands", len(names), len(registry))
	}
	for _, name := range names {
		if _, ok := lookupCommand(name); !ok {
			t.Errorf("published name %q is not dispatchable", name)
		}
	}
	// Sorted, because completion listings are shown in this order.
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("names are not sorted: %q before %q", names[i-1], names[i])
		}
	}
}
