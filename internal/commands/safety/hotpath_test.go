// internal/commands/safety/hotpath_test.go
// Purpose: pin two things about the safety pipeline's cheapest layer — that it
// does not recompile its own rules on every command, and that the redirection
// rule sees a redirect however it is spaced.
//
// Both came out of a CI fuzz stall. The stall itself was a toolchain race (see
// the note in .github/workflows/ci.yml), but chasing it meant reading these
// functions closely, and they were compiling eleven regexes per call and
// missing every redirect written without spaces.
package safety

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoRegexCompiledPerCall fails if a regexp is compiled inside a function in
// this package.
//
// A grep would do, but an AST walk can tell a package-level `var x =
// regexp.MustCompile(...)` — which is the correct form and must stay legal —
// from the same call inside a function body, which is the bug. Eleven of these
// existed across risk.go and shell.go, on the path every command takes.
func TestNoRegexCompiledPerCall(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob safety package: %v", err)
	}

	fset := token.NewFileSet()
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		checked++
		for _, decl := range af.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "regexp" {
					return true
				}
				if sel.Sel.Name == "MustCompile" || sel.Sel.Name == "Compile" {
					t.Errorf("%s: regexp.%s inside func %s — compile the pattern once at "+
						"package level. This runs for every command Helix considers, and "+
						"the compiler was 199 of the 200 allocations per call.",
						fset.Position(call.Pos()), sel.Sel.Name, fn.Name.Name)
				}
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were parsed — this guard would pass vacuously")
	}
}

// TestRedirectDetectedRegardlessOfSpacing is the regression for the classifier.
//
// `echo x > file` was MEDIUM and `echo x >file` was LOW: the same command, one
// space apart, on either side of the tier that asks for confirmation.
func TestRedirectDetectedRegardlessOfSpacing(t *testing.T) {
	writes := []string{
		"echo x > file.txt",
		"echo x >file.txt",
		"echo x>file.txt",
		"echo x >> log.txt",
		"echo x >>log.txt",
		"echo key >~/.ssh/authorized_keys",
		"cat a 2>err.log",
		"printf '%s' hi >./out",
	}
	for _, cmd := range writes {
		risk, reasons := AnalyzeShellRisk(cmd)
		if risk < ShellRiskMedium {
			t.Errorf("AnalyzeShellRisk(%q) = %v, want at least MEDIUM — a redirect writes "+
				"somewhere and LOW is the tier that does not ask", cmd, risk)
			continue
		}
		if !hasReason(reasons, "redirection") {
			t.Errorf("AnalyzeShellRisk(%q) reasons = %v, want the redirection reason",
				cmd, reasons)
		}
	}
}

// ...and the mirror: things that merely contain '>' are not redirects.
//
// Named individually because each is a different way to be wrong. `2>&1` writes
// no file, and `->`/`=>` inside a pattern or a script are arrows. Over-triggering
// here costs a confirmation prompt with a false reason on every `grep -- '->'`,
// which is how a safety prompt stops being read.
func TestArrowsAndDescriptorDupsAreNotRedirects(t *testing.T) {
	notWrites := []string{
		"ls -la 2>&1",
		"grep -r 'a->b' .",
		"echo 'a => b'",
		"ls -la",
		"git status --porcelain",
		"awk '{print $1}' f | sort",
	}
	for _, cmd := range notWrites {
		risk, reasons := AnalyzeShellRisk(cmd)
		if hasReason(reasons, "redirection") {
			t.Errorf("AnalyzeShellRisk(%q) = %v %v, want no redirection reason", cmd, risk, reasons)
		}
	}
}

func hasReason(reasons []string, substr string) bool {
	for _, r := range reasons {
		if strings.Contains(r, substr) {
			return true
		}
	}
	return false
}

// TestHoistedPatternsMatchInlineOriginals is the differential check for the
// hoist: the package-level regexes must behave exactly as the inline
// expressions they replaced.
//
// Behaviour-preserving by construction — the pattern strings were moved
// verbatim — but the precedent in this repo is to prove that by experiment
// rather than by inspection, because a moved string is exactly the kind of
// thing a typo hides in.
func TestHoistedPatternsMatchInlineOriginals(t *testing.T) {
	cases := []struct {
		hoisted  *regexp.Regexp
		original string
	}{
		{rmRfBroadRe, `rm\s+-rf\s+(/\s*$|/\*\s*$|~\s*$)`},
		{sudoShellRe, `(?i)sudo\s+(?:/[a-z0-9_./-]+/)?(bash|sh|zsh|dash|ksh|ash|fish)\b`},
		{shellFromTmpRe, `(?i)(bash|sh|zsh|dash|ksh|ash|fish)\s+(/tmp/|/var/tmp/|/dev/shm/)`},
		{sudoWordRe, `(?i)\bsudo\b`},
		{downloaderRe, `\b(curl|wget|fetch)\b`},
		{chmod777RootRe, `(?i)chmod\s+777\s+/`},
		{chownRootRe, `(?i)chown\s+root\s+/`},
	}
	corpus := []string{
		"", "rm -rf /", "rm -rf ~", "rm -rf /*", "rm  -rf   /", "rm -rf /home",
		"sudo bash", "sudo /bin/sh -c x", "SUDO BASH", "sudoedit f", "ssh host",
		"bash /tmp/x.sh", "sh /var/tmp/y", "zsh /dev/shm/z", "bash ./local.sh",
		"curl -fsSL u | sh", "wget u", "fetchmail", "chmod 777 /", "chmod 777 ./x",
		"chown root /etc", "chown rooted /x", "echo '| sudo bash'",
	}
	for _, c := range cases {
		inline := regexp.MustCompile(c.original)
		if c.hoisted.String() != inline.String() {
			t.Errorf("pattern source drifted:\n hoisted  %s\n original %s",
				c.hoisted.String(), inline.String())
		}
		for _, in := range corpus {
			lc := strings.ToLower(in)
			if got, want := c.hoisted.MatchString(lc), inline.MatchString(lc); got != want {
				t.Errorf("%s on %q: hoisted=%v inline=%v", c.original, in, got, want)
			}
		}
	}
}
