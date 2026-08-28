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

	"helix/internal/config"
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

	// Cells are coloured now, so every assertion is made on the VISIBLE text —
	// which is also the only thing a reader sees.
	plain := func(s string) string { return stripANSIForTest(s) }
	header, groq, whisper := plain(lines[0]), plain(lines[1]), plain(lines[2])

	for _, col := range []string{"STATE", "KEY", "WHERE"} {
		if !strings.Contains(header, col) {
			t.Fatalf("header %q is missing the %s column", header, col)
		}
	}
	// The state must still be ONE WORD, not the error text — the defect this
	// test was written for. The badge glyph precedes it.
	if !strings.Contains(whisper, "down") || strings.Contains(whisper, "connection refused") {
		t.Errorf("whisper row should carry a one-word state, got %q", whisper)
	}
	if !strings.Contains(groq, "healthy") {
		t.Errorf("groq row = %q, want a healthy state", groq)
	}
	// Columns must start at the same visible offset on every row.
	if a, b := strings.Index(groq, "healthy"), strings.Index(whisper, "down"); abs(a-b) > 2 {
		t.Errorf("state column drifted: healthy at %d, down at %d\n%s\n%s", a, b, groq, whisper)
	}
	// And the row stays readable: the old version was 100+ columns because the
	// error lived inside it.
	if len(whisper) > 88 {
		t.Errorf("row is %d columns wide; the detail belongs on its own line:\n%s",
			len(whisper), whisper)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// stripANSIForTest removes colour so layout assertions measure what a reader
// sees rather than the escape bytes.
func stripANSIForTest(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestStatusRowsPrintFullErrorDetail(t *testing.T) {
	lines := statusRowLines([]speech.ProviderStatusRow{whisperDownRow()})

	// Detail lines are indented under the row; joining them must reproduce the
	// whole error, address and all. The QA output stopped at "http://127.0.0.…".
	var detail []string
	for _, l := range lines {
		p := stripANSIForTest(l)
		if strings.Contains(p, statusDetailIndent) && strings.Contains(p, "127.0.0.1") ||
			strings.Contains(p, "connection refused") || strings.Contains(p, "Start it") {
			detail = append(detail, strings.TrimSpace(p))
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
	// Wrapped, not one endless line. The budget is the RENDERED width — text
	// plus the panel frame — because that is what has to fit the terminal.
	for _, l := range detail {
		if len(l) > statusDetailBudget {
			t.Errorf("detail line is %d columns, want ≤%d: %q", len(l), statusDetailBudget, l)
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

// TestWizardMergePreservesSidecarEndpoints is the regression guard for a bug
// this file's merge comment had already warned about once.
//
// The wizard moves a sidecar to a free port and records it in Endpoints; the
// commit step then overwrote the section with the struct the wizard had built,
// which has no Endpoints. whisper-local ran on 28861 while every request went
// to 8080, and the wizard reported "still not answering" about a server it had
// started and verified thirty lines earlier.
func TestWizardMergePreservesSidecarEndpoints(t *testing.T) {
	saved := cfg
	defer func() { cfg = saved }()

	cfg = &config.Config{}
	cfg.Speech.STT.Endpoints = map[string]string{"whisper-local": "http://127.0.0.1:28861"}
	cfg.Speech.STT.StreamChunkMs = 450
	cfg.Speech.TTS.Endpoints = map[string]string{"piper-local": "http://127.0.0.1:28184"}
	cfg.Speech.TTS.FirstByteMs = 900

	// What the wizard hands over: a freshly built section that knows only what
	// the user selected.
	sttCfg := config.SpeechSTTConfig{Provider: "whisper-local", Model: "base.en"}
	ttsCfg := config.SpeechTTSConfig{Provider: "piper-local"}

	sttCfg.StreamChunkMs = cfg.Speech.STT.StreamChunkMs
	sttCfg.Endpoints = cfg.Speech.STT.Endpoints
	cfg.Speech.STT = sttCfg
	ttsCfg.FirstByteMs = cfg.Speech.TTS.FirstByteMs
	ttsCfg.Endpoints = cfg.Speech.TTS.Endpoints
	cfg.Speech.TTS = ttsCfg

	if got := cfg.Speech.STT.Endpoints["whisper-local"]; got != "http://127.0.0.1:28861" {
		t.Errorf("STT endpoint = %q, want the reassigned port to survive the merge", got)
	}
	if got := cfg.Speech.TTS.Endpoints["piper-local"]; got != "http://127.0.0.1:28184" {
		t.Errorf("TTS endpoint = %q, want the reassigned port to survive the merge", got)
	}
	if cfg.Speech.STT.StreamChunkMs != 450 || cfg.Speech.TTS.FirstByteMs != 900 {
		t.Error("tuning fields must survive too")
	}

	// And the resolver must actually reach for it — a stored endpoint nothing
	// reads is the same bug wearing a different hat.
	if got := sidecarEndpoint("stt", "whisper-local", "http://127.0.0.1:8080"); got != "http://127.0.0.1:28861" {
		t.Errorf("sidecarEndpoint = %q, want the recorded endpoint, not the default", got)
	}
}
