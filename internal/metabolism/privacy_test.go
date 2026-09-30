package metabolism

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoNetworkImports is the recorder's half of Metabolism's privacy rule:
// the package that writes what the user asked must never be able to send it
// anywhere (the same contract as internal/journal, threat V5). Its imports are
// checked here, and it imports only internal/journal, which has the same test.
func TestNoNetworkImports(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob: %v", err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range af.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == "net" || strings.HasPrefix(p, "net/") || p == "crypto/tls" || p == "os/exec" {
				t.Errorf("%s imports %q; the recorder must be telemetry-free", f, p)
			}
			if strings.HasPrefix(p, "helix/") && p != "helix/internal/journal" {
				t.Errorf("%s imports %q; the recorder may depend only on internal/journal", f, p)
			}
		}
	}
}
