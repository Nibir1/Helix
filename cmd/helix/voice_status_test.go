// cmd/helix/voice_status_test.go
// Purpose: /voice-status table layout. The QA session produced this row —
//
//	whisper-local  Whisper (local sidecar)  whisper-local unreachable: Get "http://127.0.0.… key    local
//
// three defects at once: the error truncated mid-URL, every column after it
// misaligned, and an unreachable keyless local sidecar labeled "key". The
// renderer returns strings so all three are pinned here without a terminal.
package main

import (
	"strings"
	"testing"

	"helix/internal/speech"
)

// whisperDownRow is the QA row: a local sidecar in the active chain whose
// health probe failed with a long, URL-bearing error.
func whisperDownRow() speech.ProviderStatusRow {
	return speech.ProviderStatusRow{
		Name: "whisper-local", Display: "Whisper (local sidecar)",
		Local: true, RequiresKey: false, HasKey: false, InChain: true, Healthy: false,
		HealthDetail: `whisper-local unreachable: Get "http://127.0.0.1:8080/v1/models": ` +
			`dial tcp 127.0.0.1:8080: connect: connection refused`,
	}
}

func TestStatusRowsStayAlignedWithLongErrors(t *testing.T) {
	rows := []speech.ProviderStatusRow{
		{Name: "groq", Display: "Groq Whisper", RequiresKey: true, HasKey: true,
			InChain: true, Healthy: true},
		whisperDownRow(),
	}

	lines := statusRowLines(rows)

	// The two provider rows are lines[1] and lines[2] (lines[0] is the header);
	// every column must start at the same offset in all three.
	header, groq := lines[0], lines[1]
	whisper := lines[2]
	for _, col := range []string{"STATE", "KEY", "WHERE"} {
		want := strings.Index(header, col)
		if want < 0 {
			t.Fatalf("header %q is missing the %s column", header, col)
		}
	}
	// State starts after the name and display columns; compare the actual byte
	// offsets of the state word across rows.
	stateAt := 2 + statusNameWidth + 1 + statusDisplayWidth + 1
	for _, l := range []string{header, groq, whisper} {
		if len(l) <= stateAt {
			t.Fatalf("row is shorter than the state column: %q", l)
		}
	}
	if got := strings.TrimSpace(whisper[stateAt : stateAt+statusStateWidth]); got != "down" {
		t.Errorf("whisper state cell = %q, want \"down\" (a one-word state, not the error)", got)
	}
	if got := strings.TrimSpace(groq[stateAt : stateAt+statusStateWidth]); got != "healthy" {
		t.Errorf("groq state cell = %q, want \"healthy\"", got)
	}
	// The row itself must stay short enough to read: the old version was 100+
	// columns because the error lived inside it.
	if len(whisper) > 80 {
		t.Errorf("row is %d columns wide; the detail belongs on its own line:\n%s",
			len(whisper), whisper)
	}
}

func TestStatusRowsPrintFullErrorDetail(t *testing.T) {
	lines := statusRowLines([]speech.ProviderStatusRow{whisperDownRow()})

	// Detail lines are indented under the row; joining them must reproduce the
	// whole error, address and all. The QA output stopped at "http://127.0.0.…".
	var detail []string
	for _, l := range lines {
		if strings.HasPrefix(l, statusDetailIndent) {
			detail = append(detail, strings.TrimSpace(l))
		}
	}
	if len(detail) == 0 {
		t.Fatal("no indented detail lines were printed for a failing provider")
	}
	joined := strings.Join(detail, " ")
	for _, want := range []string{
		"127.0.0.1:8080",
		"connection refused",
		`http://127.0.0.1:8080/v1/models`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("detail is missing %q — it was truncated:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "…") {
		t.Error("the detail must not be truncated at all")
	}
	// Wrapped, not one endless line.
	for _, l := range detail {
		if len(l) > statusDetailWidth {
			t.Errorf("detail line is %d columns, want ≤%d: %q", len(l), statusDetailWidth, l)
		}
	}
}

// A local sidecar the chain depends on gets the command that starts it —
// Helix never launches sidecars itself (ADR-002), so the hint is the fix.
func TestStatusRowsHintHowToStartADownSidecar(t *testing.T) {
	lines := strings.Join(statusRowLines([]speech.ProviderStatusRow{whisperDownRow()}), "\n")

	if !strings.Contains(lines, "whisper-server") {
		t.Errorf("a down whisper-local in the chain must say how to start it:\n%s", lines)
	}
	if !strings.Contains(lines, "docs/edge_deployment.md") {
		t.Errorf("the hint should point at the deployment doc:\n%s", lines)
	}

	// A standby sidecar nobody selected must NOT be nagged about.
	standby := whisperDownRow()
	standby.InChain = false
	standby.HealthDetail = "standby"
	quiet := strings.Join(statusRowLines([]speech.ProviderStatusRow{standby}), "\n")
	if strings.Contains(quiet, "whisper-server") {
		t.Errorf("an out-of-chain provider must not get a start-it hint:\n%s", quiet)
	}
	if !strings.Contains(quiet, "standby") {
		t.Errorf("an out-of-chain provider is standby, not down:\n%s", quiet)
	}
}

func TestProviderKeyStateDistinguishesKeylessFromKeyed(t *testing.T) {
	cases := []struct {
		name string
		row  speech.ProviderStatusRow
		want string
	}{
		// The QA defect: a keyless local sidecar displayed "key".
		{"keyless local sidecar", speech.ProviderStatusRow{Local: true}, "free"},
		{"cloud with a stored key", speech.ProviderStatusRow{RequiresKey: true, HasKey: true}, "key"},
		{"cloud without a key", speech.ProviderStatusRow{RequiresKey: true}, "no key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := providerKeyState(tc.row); got != tc.want {
				t.Errorf("providerKeyState = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTruncStrIsRuneSafe(t *testing.T) {
	// Byte-slicing "héllo wörld" at 8 used to split a multibyte rune.
	got := truncStr("héllo wörld", 8)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncStr(%q, 8) = %q, want an ellipsis", "héllo wörld", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Errorf("truncStr split a rune: %q", got)
	}
	if n := len([]rune(got)); n != 8 {
		t.Errorf("truncStr produced %d runes, want 8: %q", n, got)
	}
	if s := "short"; truncStr(s, 8) != s {
		t.Errorf("a string within the bound must be returned unchanged")
	}
	if truncStr("anything", 0) != "" {
		t.Error("a non-positive bound must yield the empty string, not a panic")
	}
}

func TestWrapTextKeepsLongURLsIntact(t *testing.T) {
	url := "http://127.0.0.1:8080/v1/models?a=1&b=2&c=3&d=4&e=5&f=6&g=7&h=8&i=9&j=10"
	lines := wrapText("dial failed for "+url+" after 3 tries", 40)

	joined := strings.Join(lines, " ")
	if !strings.Contains(joined, url) {
		t.Errorf("the URL was split across lines:\n%s", strings.Join(lines, "\n"))
	}
	if wrapText("   ", 40) != nil {
		t.Error("blank input must produce no lines")
	}
}

func TestStatusRowsEmptyChain(t *testing.T) {
	lines := statusRowLines(nil)
	if len(lines) != 1 || !strings.Contains(lines[0], "no registered providers") {
		t.Errorf("an empty table must say so, got %q", lines)
	}
}
