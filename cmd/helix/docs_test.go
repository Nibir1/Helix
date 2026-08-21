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
