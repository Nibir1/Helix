// internal/diagnostics/diagnostics_test.go
// Purpose: Verify redaction, retention, purge, opt-out, and — critically — that
// the diagnostics package imports no networking primitives (telemetry-free
// guarantee, grep-verified in CI on every run).
package diagnostics

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRedactEnv(t *testing.T) {
	got := RedactEnv([]string{
		"OPENAI_API_KEY=sk-live-123",
		"CUSTOM_API_KEY=abc",
		"GH_TOKEN=tok",
		"DB_SECRET=s",
		"NVD_API_KEY=k",
		"HOME=/home/u",
	})
	for _, k := range []string{"OPENAI_API_KEY", "CUSTOM_API_KEY", "GH_TOKEN", "DB_SECRET", "NVD_API_KEY"} {
		if got[k] != "[REDACTED]" {
			t.Fatalf("%s not redacted: %q", k, got[k])
		}
	}
	if got["HOME"] != "/home/u" {
		t.Fatal("non-sensitive env must be preserved")
	}
}

func TestWriteListPurge(t *testing.T) {
	SetReportsDir(t.TempDir())
	defer SetReportsDir("")
	path, err := WriteReport("panic: test boom", []byte("stacktrace"))
	if err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("crash report must be 0600, got %o", perm)
		}
	}
	sums := ListReports()
	if len(sums) != 1 || !strings.Contains(sums[0].Reason, "test boom") {
		t.Fatalf("ListReports mismatch: %+v", sums)
	}
	n, err := PurgeReports()
	if err != nil || n != 1 {
		t.Fatalf("PurgeReports = %d, %v", n, err)
	}
	if len(ListReports()) != 0 {
		t.Fatal("expected zero reports after purge")
	}
}

func TestRetentionCap(t *testing.T) {
	SetReportsDir(t.TempDir())
	defer SetReportsDir("")
	for i := 0; i < maxReports+2; i++ {
		if _, err := WriteReport("panic: retention", []byte("s")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Millisecond) // distinct timestamped names
	}
	if got := len(ListReports()); got != maxReports {
		t.Fatalf("expected retention cap %d, got %d", maxReports, got)
	}
}

func TestDisabledOptOut(t *testing.T) {
	t.Setenv(EnvDisable, "off")
	if Enabled() {
		t.Fatal("expected reporting disabled")
	}
	if _, err := WriteReport("panic: x", []byte("s")); err == nil {
		t.Fatal("expected WriteReport error when disabled")
	}
}

// TestNoNetworkImports grep-verifies the telemetry-free contract in CI: the
// package must never import networking or TLS primitives.
func TestNoNetworkImports(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob diagnostics package: %v", err)
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
			if p == "net" || strings.HasPrefix(p, "net/") || p == "crypto/tls" {
				t.Fatalf("diagnostics must be telemetry-free but imports %q", p)
			}
		}
	}
}
