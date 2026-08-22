// internal/edge/setup_script_test.go
// Purpose: BlackBox P10.2 — keep scripts/edge-setup.sh honest from CI. Shell
// scripts rot silently; these tests assert its syntax, its consent/checksum
// guarantees (guardrail §12 #1), and that its pinned Ollama checksum has not
// drifted from the one internal/ollama enforces.
package edge

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	// This package lives at internal/edge; the repo root is two levels up.
	path := filepath.Join("..", "..", rel)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("missing %s: %v", rel, err)
	}
	return path
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(repoFile(t, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func TestEdgeSetupScriptIsValidBash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash syntax check is not meaningful on Windows")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}

	out, err := exec.Command(bash, "-n", repoFile(t, "scripts/edge-setup.sh")).CombinedOutput()
	if err != nil {
		t.Fatalf("edge-setup.sh has a syntax error: %v\n%s", err, out)
	}
}

// The script and internal/ollama verify the SAME upstream artifact. If the two
// pins drift, one install path trusts a script the other would reject — a
// supply-chain hole that no other test would notice.
func TestOllamaChecksumPinStaysInSyncWithGoInstaller(t *testing.T) {
	script := readRepoFile(t, "scripts/edge-setup.sh")
	installer := readRepoFile(t, "internal/ollama/installer.go")

	scriptPin := extractQuoted(script, "OLLAMA_INSTALL_SHA256=")
	goPin := extractQuoted(installer, "ollamaInstallScriptSHA256 = ")

	if scriptPin == "" {
		t.Fatal("edge-setup.sh must pin the Ollama installer checksum")
	}
	if goPin == "" {
		t.Fatal("internal/ollama must pin the Ollama installer checksum")
	}
	if scriptPin != goPin {
		t.Fatalf("checksum pins drifted:\n  script: %s\n  go:     %s\n"+
			"Both verify the same upstream install.sh; update them together.", scriptPin, goPin)
	}
	if len(goPin) != 64 {
		t.Fatalf("pin is not a SHA-256 hex digest: %q", goPin)
	}
}

// extractQuoted returns the first double-quoted value following marker.
func extractQuoted(body, marker string) string {
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	rest := body[i+len(marker):]
	if !strings.HasPrefix(rest, `"`) {
		return ""
	}
	rest = rest[1:]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// The cardinal supply-chain rule: never execute a remote script unverified.
func TestEdgeSetupNeverPipesCurlToShell(t *testing.T) {
	script := readRepoFile(t, "scripts/edge-setup.sh")

	for _, bad := range []string{"curl -fsSL | sh", "| sh", "|sh", "| bash", "|bash"} {
		// The instructions block legitimately shows a `docker run` line and
		// git clones; only piping into a shell is forbidden.
		for _, line := range strings.Split(script, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "say ") {
				continue // comments and printed help text are not execution
			}
			if strings.Contains(trimmed, bad) {
				t.Errorf("edge-setup.sh must never pipe a download into a shell: %q", trimmed)
			}
		}
	}
}

// Fail-closed verification: a checksum mismatch must refuse to run, and the
// script must actually compare a computed digest against the pin.
func TestEdgeSetupVerifiesBeforeRunning(t *testing.T) {
	script := readRepoFile(t, "scripts/edge-setup.sh")

	for _, want := range []string{
		"sha256sum",                   // digest computation
		"shasum",                      // macOS/BSD fallback
		`"$GOT" != "$WANT"`,           // the comparison itself
		"NOT running the installer",   // the fail-closed message
		"HELIX_OLLAMA_INSTALL_SHA256", // documented override, same as the Go path
	} {
		if !strings.Contains(script, want) {
			t.Errorf("edge-setup.sh is missing checksum-verification element %q", want)
		}
	}
}

// Consent (guardrail §12 #1): every install must pass through ask(), and a
// non-TTY run must default to NO so headless provisioning cannot hang or
// silently install software nobody approved.
func TestEdgeSetupIsConsentGated(t *testing.T) {
	script := readRepoFile(t, "scripts/edge-setup.sh")

	if !strings.Contains(script, "if [ ! -t 0 ]; then") {
		t.Error("ask() must detect a non-TTY and decline rather than block")
	}
	if !strings.Contains(script, `ask "Install Ollama`) {
		t.Error("the Ollama install must be behind an explicit prompt")
	}
	if !strings.Contains(script, "read -r reply || true") {
		t.Error("prompt reads must tolerate EOF under `set -e` (the install.sh lesson)")
	}
	if !strings.Contains(script, "--dry-run") {
		t.Error("the script must support --dry-run so its plan is reviewable")
	}
}

// The Jetson Nano 1st-gen refusal is the script's one hard board gate; it must
// point at the cloud path rather than simply failing.
func TestEdgeSetupRefusesOllamaOnJetsonNano(t *testing.T) {
	script := readRepoFile(t, "scripts/edge-setup.sh")

	if !strings.Contains(script, "is_jetson_nano_first_gen") {
		t.Fatal("the script must gate Ollama on the Jetson Nano check")
	}
	if !strings.Contains(script, "Ollama is NOT supported here") {
		t.Error("the refusal must be explicit")
	}
	if !strings.Contains(script, "/blackbox setup") || !strings.Contains(script, "Groq") {
		t.Error("the refusal must point at the cloud voice path, not just refuse")
	}
	// Same Orin carve-out as internal/edge.IsJetsonNanoFirstGen.
	if !strings.Contains(script, "*orin*") {
		t.Error("the Orin Nano must be excluded from the first-gen refusal")
	}
}

// End-to-end behavioral check: --dry-run must change nothing and must exercise
// the Jetson branch when the board is forced.
func TestEdgeSetupDryRunJetsonBranch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell execution is not meaningful on Windows")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}

	out, err := exec.Command(bash, repoFile(t, "scripts/edge-setup.sh"),
		"--dry-run", "--assume-board=NVIDIA Jetson Nano Developer Kit").CombinedOutput()
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, out)
	}
	text := string(out)

	if !strings.Contains(text, "Ollama is NOT supported here") {
		t.Errorf("the Jetson Nano branch did not fire:\n%s", text)
	}
	if !strings.Contains(text, "nothing was changed") {
		t.Errorf("--dry-run must state that nothing changed:\n%s", text)
	}
	// A modern board must NOT hit the refusal.
	out2, err := exec.Command(bash, repoFile(t, "scripts/edge-setup.sh"),
		"--dry-run", "--assume-board=Raspberry Pi 5 Model B Rev 1.0").CombinedOutput()
	if err != nil {
		t.Fatalf("dry run (Pi) failed: %v\n%s", err, out2)
	}
	if strings.Contains(string(out2), "Ollama is NOT supported here") {
		t.Error("the Pi 5 must not be refused a local LLM")
	}
}

// --check must be side-effect free, so it is safe to run on a live appliance.
func TestEdgeSetupCheckOnlyIsInert(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell execution is not meaningful on Windows")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}

	out, err := exec.Command(bash, repoFile(t, "scripts/edge-setup.sh"), "--check").CombinedOutput()
	if err != nil {
		t.Fatalf("--check failed: %v\n%s", err, out)
	}
	text := string(out)
	if !strings.Contains(text, "nothing was changed") {
		t.Errorf("--check must state it changed nothing:\n%s", text)
	}
	if strings.Contains(text, "Install Ollama") {
		t.Errorf("--check must not reach any install prompt:\n%s", text)
	}
}

// Shellcheck catches the shell-specific footguns Go tests cannot: A && B || C
// mistaken for if-then-else, unquoted splits, malformed disable directives.
// Skipped when the linter is absent so the suite stays portable.
func TestEdgeSetupPassesShellcheck(t *testing.T) {
	sc, err := exec.LookPath("shellcheck")
	if err != nil {
		t.Skipf("shellcheck not installed: %v", err)
	}
	out, err := exec.Command(sc, repoFile(t, "scripts/edge-setup.sh")).CombinedOutput()
	if err != nil {
		t.Fatalf("shellcheck reported issues:\n%s", out)
	}
}
