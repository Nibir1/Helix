// internal/edge/edge_test.go
// Purpose: BlackBox P10.3 — board detection, thermal parsing, and the
// Jetson-Nano gate are exercised with synthetic sysfs fixtures, so the logic
// that only ever runs on real boards is still covered on a dev machine.
package edge

import (
	"os"
	"path/filepath"
	"testing"

	"helix/internal/confinement"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// Device-tree model strings are NUL-terminated; leaving the NUL in corrupts
// terminal output on the very boards this feature targets.
func TestDetectBoardTrimsDeviceTreeNul(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "model")
	writeFile(t, p, "Raspberry Pi 5 Model B Rev 1.0\x00")

	if got := detectBoardFrom([]string{p}); got != "Raspberry Pi 5 Model B Rev 1.0" {
		t.Fatalf("board = %q, want the NUL trimmed", got)
	}
}

func TestDetectBoardUsesFirstReadablePath(t *testing.T) {
	dir := t.TempDir()
	second := filepath.Join(dir, "second")
	writeFile(t, second, "Intel N100 Mini PC")

	got := detectBoardFrom([]string{filepath.Join(dir, "missing"), second})
	if got != "Intel N100 Mini PC" {
		t.Fatalf("board = %q, want the fallback path used", got)
	}
	if got := detectBoardFrom([]string{filepath.Join(dir, "nope")}); got != "" {
		t.Fatalf("undetectable board must be empty, got %q", got)
	}
}

// The Jetson Nano 1st-gen is the one board in the matrix where Ollama is
// unsupported, so this gate decides whether setup offers a local LLM at all.
func TestIsJetsonNanoFirstGen(t *testing.T) {
	cases := []struct {
		board string
		want  bool
	}{
		{"NVIDIA Jetson Nano Developer Kit", true},
		{"nvidia jetson nano", true},
		// The Orin Nano is a different, modern device that also says "Nano" —
		// refusing Ollama on it would be wrong.
		{"NVIDIA Jetson Orin Nano Developer Kit", false},
		{"NVIDIA Jetson Xavier NX", false},
		{"Raspberry Pi 5 Model B Rev 1.0", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsJetsonNanoFirstGen(tc.board); got != tc.want {
			t.Errorf("IsJetsonNanoFirstGen(%q) = %v, want %v", tc.board, got, tc.want)
		}
	}
}

// Boards expose several zones (CPU, GPU, PMIC); the first is not reliably the
// one that throttles, so the hottest must win.
func TestReadThermalPicksHottestZone(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "thermal_zone0", "temp"), "42000\n")
	writeFile(t, filepath.Join(dir, "thermal_zone1", "temp"), "81500\n")
	writeFile(t, filepath.Join(dir, "thermal_zone2", "temp"), "38000\n")

	c, errText := readThermalFrom(filepath.Join(dir, "thermal_zone*", "temp"))
	if errText != "" {
		t.Fatalf("unexpected error: %s", errText)
	}
	if c < 81.4 || c > 81.6 {
		t.Fatalf("temperature = %.2f, want ~81.5", c)
	}
}

func TestReadThermalIgnoresImplausibleReadings(t *testing.T) {
	dir := t.TempDir()
	// A zone reporting a nonsense value must not become the headline number.
	writeFile(t, filepath.Join(dir, "thermal_zone0", "temp"), "999999999\n")
	writeFile(t, filepath.Join(dir, "thermal_zone1", "temp"), "45000\n")

	c, _ := readThermalFrom(filepath.Join(dir, "thermal_zone*", "temp"))
	if c < 44.9 || c > 45.1 {
		t.Fatalf("temperature = %.2f, want the plausible 45.0", c)
	}
}

func TestReadThermalNoSensors(t *testing.T) {
	c, errText := readThermalFrom(filepath.Join(t.TempDir(), "nothing*", "temp"))
	if c != 0 || errText == "" {
		t.Fatalf("absent sensors must report 0 with an explanation, got %.2f / %q", c, errText)
	}
}

func TestReadThrottledParsesFirmwareBitmask(t *testing.T) {
	dir := t.TempDir()

	clean := filepath.Join(dir, "clean")
	writeFile(t, clean, "throttled=0x0\n")
	if readThrottledFrom([]string{clean}) {
		t.Error("a zero bitmask must not report throttling")
	}

	hot := filepath.Join(dir, "hot")
	writeFile(t, hot, "throttled=0x50005\n")
	if !readThrottledFrom([]string{hot}) {
		t.Error("a non-zero bitmask must report throttling")
	}

	if readThrottledFrom([]string{filepath.Join(dir, "absent")}) {
		t.Error("an absent file must report not-throttled, not panic")
	}

	garbage := filepath.Join(dir, "garbage")
	writeFile(t, garbage, "not a number\n")
	if readThrottledFrom([]string{garbage}) {
		t.Error("unparseable content must not be read as throttling")
	}
}

func TestThermalVerdictThresholds(t *testing.T) {
	cases := []struct {
		c        float64
		contains string
	}{
		{0, "unknown"},
		{45, "ok"},
		{72, "warm"},
		{81, "hot"},
		{86, "CRITICAL"},
	}
	for _, tc := range cases {
		got := ThermalVerdict(tc.c)
		if got == "" {
			t.Fatalf("verdict for %.0f°C is empty", tc.c)
		}
		if tc.contains != "" && !contains(got, tc.contains) {
			t.Errorf("ThermalVerdict(%.0f) = %q, want it to mention %q", tc.c, got, tc.contains)
		}
	}
}

func contains(s, sub string) bool { return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// A degraded confinement backend must come with the fix, not just the fact —
// on a headless board this note is the only thing telling the operator that
// their sandbox silently lost a layer.
func TestConfinementNoteExplainsDegradation(t *testing.T) {
	note := confinementNote("linux", string(confinement.BackendNone))
	if note == "" {
		t.Fatal("a missing confinement backend must be explained")
	}
	if !contains(note, "bubblewrap") {
		t.Errorf("the note must name the remediation, got %q", note)
	}
	// A healthy backend needs no commentary.
	if confinementNote("linux", "landlock") != "" {
		t.Error("a working backend must produce no note")
	}
}

// Collect must never panic or block, whatever the host looks like — a
// diagnostic that breaks the device is worse than no diagnostic.
func TestCollectIsSafeOnAnyHost(t *testing.T) {
	r := Collect()
	if r.OS == "" || r.Arch == "" {
		t.Fatalf("OS/arch must always be reported: %+v", r)
	}
	if r.AudioBackend == "" {
		t.Fatal("the audio backend name must always be reported")
	}
	if r.Confinement == "" {
		t.Fatal("the confinement backend must always be reported")
	}
}
