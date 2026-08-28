// internal/edge/edge.go
//
// Purpose: edge-appliance diagnostics (BlackBox P10.3).
//
// docs/edge_deployment.md says "does it run on device X" is really three
// questions: the CPU architecture, whether the board can host the sidecars you
// want, and the two Linux gotchas — `audio_cgo` for speaker output and
// bubblewrap where Landlock is unavailable. Both gotchas fail QUIETLY: a
// CGO-free binary is structurally silent no matter how the TTS provider is
// configured, and confinement degrades to none on an old kernel without
// stopping anything. On a headless board with no screen, silent degradation is
// the worst possible failure mode.
//
// This package answers those questions from the running binary, so the report
// describes what is actually in force rather than what the docs hope for.
package edge

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"helix/internal/audio"
	"helix/internal/confinement"
)

// Report is the full edge-appliance picture.
type Report struct {
	OS   string
	Arch string

	// Board is the detected hardware model ("" when undetectable).
	Board string

	// AudioBackend describes this build's speaker capability, and
	// SpeechSupported is the actionable form of it.
	AudioBackend    string
	SpeechSupported bool

	// Confinement is the backend actually in force, with Note carrying
	// remediation when it degraded.
	Confinement string
	Note        string

	// Thermal is the hottest sensor reading in °C (0 when unavailable) and
	// Throttled reports a kernel-flagged throttle event where detectable.
	ThermalC   float64
	Throttled  bool
	ThermalErr string
}

// Collect gathers the edge report. Every probe is best-effort and non-fatal:
// diagnostics must never be the thing that breaks a device.
func Collect() Report {
	r := Report{
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		Board:           DetectBoard(),
		AudioBackend:    audio.BackendName(),
		SpeechSupported: audio.SpeechSupported(),
		Confinement:     confinement.BackendName(),
	}
	r.Note = confinementNote(r.OS, r.Confinement)
	r.ThermalC, r.ThermalErr = readThermalC()
	r.Throttled = readThrottled()
	return r
}

// boardModelPaths are where Linux exposes a board name. The device-tree entries
// cover Raspberry Pi and Jetson; the DMI entry covers x86 mini-PCs.
var boardModelPaths = []string{
	"/proc/device-tree/model",
	"/sys/firmware/devicetree/base/model",
	"/sys/class/dmi/id/product_name",
}

// DetectBoard returns the hardware model string, or "" when it cannot be
// determined (non-Linux, a VM, or a board that does not publish one).
func DetectBoard() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	return detectBoardFrom(boardModelPaths)
}

// detectBoardFrom is the platform-independent half, split out so the parsing
// (NUL trimming, path precedence) is testable on any developer machine rather
// than only on the boards it targets.
func detectBoardFrom(paths []string) string {
	for _, p := range paths {
		if s := readBoardFile(p); s != "" {
			return s
		}
	}
	return ""
}

// readBoardFile reads one model file. Device-tree strings are NUL-terminated,
// so the trailing NUL must be trimmed or it corrupts terminal output.
func readBoardFile(path string) string {
	data, err := os.ReadFile(path) // #nosec G304 — fixed diagnostic paths
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(string(data), "\x00\n"))
}

// IsJetsonNanoFirstGen reports whether the board is a first-generation Jetson
// Nano — the one device in the matrix where Ollama is unsupported (JetPack 4.6
// is frozen at CUDA 10.2 / Maxwell 5.3), so the cloud voice path is the
// recommendation and a local-LLM setup must be refused rather than attempted.
//
// The Orin Nano is explicitly excluded: it is a different, modern device whose
// model string also contains "Nano".
func IsJetsonNanoFirstGen(board string) bool {
	b := strings.ToLower(board)
	if !strings.Contains(b, "jetson") || !strings.Contains(b, "nano") {
		return false
	}
	return !strings.Contains(b, "orin")
}

// confinementNote explains a degraded confinement backend and how to fix it.
// An empty string means the backend is fine and needs no commentary.
func confinementNote(goos, backend string) string {
	if backend != string(confinement.BackendNone) {
		return ""
	}
	if goos == "linux" {
		// Landlock needs kernel >= 5.13; older boards (Jetson Nano's 4.9) will
		// never have it, which makes bubblewrap the only remaining layer.
		return "no kernel confinement in force — install bubblewrap " +
			"(`sudo apt install -y bubblewrap`) to restore a real sandbox layer. " +
			"The directory sandbox and the full safety pipeline still apply."
	}
	return "no kernel confinement backend available on this platform"
}

// thermalGlob is the sysfs thermal-zone pattern, overridable for tests.
var thermalGlob = "/sys/class/thermal/thermal_zone*/temp"

// readThermalC returns the hottest sensor reading in °C.
//
// The hottest zone is used, not the first: boards expose several (CPU, GPU,
// PMIC) and the first is not reliably the one that throttles.
func readThermalC() (float64, string) {
	if runtime.GOOS != "linux" {
		return 0, "thermal sensors are Linux-only"
	}
	return readThermalFrom(thermalGlob)
}

// readThermalFrom is the platform-independent half (see detectBoardFrom).
func readThermalFrom(glob string) (float64, string) {
	zones, err := filepath.Glob(glob)
	if err != nil || len(zones) == 0 {
		return 0, "no thermal sensors exposed"
	}
	hottest := 0.0
	found := false
	for _, z := range zones {
		data, err := os.ReadFile(z) // #nosec G304 — sysfs glob result
		if err != nil {
			continue
		}
		milli, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
		if err != nil {
			continue
		}
		// sysfs reports milli-degrees; some boards report degrees directly.
		c := milli / 1000.0
		if c > 200 || c <= 0 {
			continue // implausible reading; skip rather than alarm
		}
		if c > hottest {
			hottest = c
			found = true
		}
	}
	if !found {
		return 0, "no usable thermal reading"
	}
	return hottest, ""
}

// throttledPaths are where the Raspberry Pi firmware exposes its throttle
// bitmask without requiring the vcgencmd binary.
var throttledPaths = []string{
	"/sys/devices/platform/soc/soc:firmware/get_throttled",
	"/sys/class/hwmon/hwmon0/throttle",
}

// readThrottled reports a firmware-flagged throttle event where the board
// exposes one. A false result means "not detected", not "definitely fine" —
// most boards publish nothing, which is why ThermalC is reported alongside.
func readThrottled() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	return readThrottledFrom(throttledPaths)
}

// readThrottledFrom is the platform-independent half (see detectBoardFrom).
func readThrottledFrom(paths []string) bool {
	for _, p := range paths {
		data, err := os.ReadFile(p) // #nosec G304 — fixed diagnostic paths
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(data))
		s = strings.TrimPrefix(strings.TrimPrefix(s, "throttled="), "0x")
		if s == "" {
			continue
		}
		v, err := strconv.ParseUint(s, 16, 64)
		if err != nil {
			continue
		}
		if v != 0 {
			return true
		}
	}
	return false
}

// ThermalVerdict turns a temperature into a short human judgement.
// Thresholds follow the Raspberry Pi's documented behavior: soft throttling
// begins around 80°C and hard throttling at 85°C.
func ThermalVerdict(c float64) string {
	switch {
	case c <= 0:
		return "unknown"
	case c >= 85:
		return "CRITICAL — hard throttling; improve cooling"
	case c >= 80:
		return "hot — soft throttling likely; check airflow"
	case c >= 70:
		return "warm"
	default:
		return "ok"
	}
}
