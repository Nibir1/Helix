package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// commandRowRe matches a README command-table row: | `/thing [args]` | text |
var commandRowRe = regexp.MustCompile("(?m)^\\|\\s*`(/[^`]+)`\\s*\\|")

// TestReadmeCommandReferenceMatchesRegistry keeps the published command
// reference honest.
//
// The registry already stops /help from drifting from the code, but the README
// is a separate copy that nothing forced to agree — and a documented command
// that does not exist (or an undocumented one that does) is the same defect the
// registry was built to end, one layer out.
func TestReadmeCommandReferenceMatchesRegistry(t *testing.T) {
	readme := readReadme(t)

	section := betweenHeadings(readme,
		"## Comprehensive Command Reference", "## AI & Planner System")
	if section == "" {
		t.Fatal("could not locate the command reference section in README.md")
	}

	documented := map[string]string{} // canonical name -> usage line as written
	for _, m := range commandRowRe.FindAllStringSubmatch(section, -1) {
		usage := strings.ReplaceAll(m[1], `\|`, "|")
		name := usage
		if i := strings.IndexAny(name, " \t"); i >= 0 {
			name = name[:i]
		}
		cmd, ok := lookupCommand(name)
		if !ok {
			t.Errorf("README documents %q, which is not a registered command", name)
			continue
		}
		documented[cmd.Name] = usage
	}

	for _, cmd := range registry {
		if cmd.Hidden {
			continue
		}
		usage, ok := documented[cmd.Name]
		if !ok {
			t.Errorf("%s is registered but absent from the README command reference "+
				"(add a row under its section)", cmd.Name)
			continue
		}
		// The usage line is the contract a reader acts on, so a stale one is
		// worse than none at all.
		if usage != cmd.UsageLine() {
			t.Errorf("%s: README shows usage %q, registry says %q",
				cmd.Name, usage, cmd.UsageLine())
		}
	}
}

// TestReadmeDocumentsEveryPermissionMode: the posture table is the one place a
// reader learns how much runs without being asked.
func TestReadmeDocumentsEveryPermissionMode(t *testing.T) {
	readme := readReadme(t)
	for _, mode := range strings.Split(permissionModeList(), " | ") {
		if !strings.Contains(readme, "`"+mode+"`") {
			t.Errorf("permission mode %q is not documented in the README", mode)
		}
	}
	// And the guarantee that makes the loosest mode safe must be stated.
	if !strings.Contains(readme, "High-risk commands stay blocked in **every** mode") {
		t.Error("the README must state that high risk stays blocked in every mode")
	}
}

func readReadme(t *testing.T) string {
	t.Helper()
	// Tests run with the package directory as the working directory.
	data, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	return string(data)
}

func betweenHeadings(doc, start, end string) string {
	i := strings.Index(doc, start)
	if i < 0 {
		return ""
	}
	rest := doc[i:]
	j := strings.Index(rest, end)
	if j < 0 {
		return rest
	}
	return rest[:j]
}

// treeEntryRe matches an `internal/` package line in the README's architecture
// tree: "│   ├── update/    # ...".
var treeEntryRe = regexp.MustCompile(`[├└]──\s+([a-z_]+)/`)

// TestReadmeArchitectureTreeMatchesPackages keeps the published architecture
// diagram honest, for the same reason the command reference is checked.
//
// It had drifted badly: seventeen packages existed that the tree never
// mentioned — speech, vision, wakeword, update, session, daemon, ambient and
// more — so a reader forming a mental model of Helix from the README was
// missing every subsystem added after the first release. A diagram nobody
// verifies becomes a picture of the past.
func TestReadmeArchitectureTreeMatchesPackages(t *testing.T) {
	section := betweenHeadings(readReadme(t), "## Architecture Overview", "---")
	if section == "" {
		t.Fatal("could not locate the architecture tree in README.md")
	}

	documented := map[string]bool{}
	for _, m := range treeEntryRe.FindAllStringSubmatch(section, -1) {
		documented[m[1]] = true
	}
	// Top-level entries share the same shape as package lines; they are not
	// packages and are not checked either way.
	for _, notAPackage := range []string{"cmd", "internal", "tests", "docs", "dist", "scripts"} {
		delete(documented, notAPackage)
	}

	entries, err := os.ReadDir(filepath.Join("..", "..", "internal"))
	if err != nil {
		t.Fatalf("read internal/: %v", err)
	}
	actual := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			actual[e.Name()] = true
		}
	}
	if len(actual) < 10 {
		t.Fatalf("only found %d packages — the walk is broken, so this check "+
			"would pass by finding nothing", len(actual))
	}

	for name := range actual {
		if !documented[name] {
			t.Errorf("internal/%s exists but is absent from the README architecture tree", name)
		}
	}
	for name := range documented {
		if !actual[name] {
			t.Errorf("the README architecture tree lists internal/%s, which does not exist", name)
		}
	}
}
