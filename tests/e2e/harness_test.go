//go:build !windows

// tests/e2e/harness_test.go
// Purpose: End-to-end TTY harness for Helix. Boots the real helix binary under
// a pseudo-terminal (PTY) with an isolated $HOME and a mock OpenAI-compatible
// provider, then proves classifier routing, safety tiers, and confirmation UX
// with zero real AI and zero external network.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"

	"helix/internal/rag"
)

// ansiRe strips CSI color/cursor sequences and OSC shell-integration sequences
// so assertions can match on plain text.
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]|\x1b\\][^\a]*\a")

// harness wraps one PTY-backed helix process plus its mock provider.
type harness struct {
	t         *testing.T
	ptmx      *os.File
	cmd       *exec.Cmd
	srv       *httptest.Server
	home      string
	project   string
	chatHits  *int32
	ttsHits   *int32
	outMu     sync.Mutex
	outBuf    bytes.Buffer
	closeOnce sync.Once

	// plannerPrompts returns the prompts the mock provider actually received.
	// Assertions about CONTEXT (what Helix told the model) cannot be made from
	// terminal output — the prompt is the only place session memory, RAG blocks
	// and directives are visible. It is a closure over the handler's own state
	// rather than a copy, so there is one source of truth.
	plannerPrompts func() []string
}

// newHarness boots helix under a PTY with an isolated HOME and a mock provider
// that returns chatResponse for every planner request. It waits until the
// interactive prompt is ready before returning.
func newHarness(t *testing.T, chatResponse string) *harness {
	t.Helper()
	home := t.TempDir()
	project := t.TempDir()

	hits := new(int32)
	ttsHits := new(int32)
	var promptMu sync.Mutex
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chat/completions":
			atomic.AddInt32(hits, 1)
			reply := chatResponse
			// the Instruction Firewall issues a critic call after any
			// plan with shell steps. Answer it with a well-formed "yes" so the
			// pre-firewall scenarios keep their original behavior.
			promptText := extractPromptFromRequest(r)
			promptMu.Lock()
			prompts = append(prompts, promptText)
			promptMu.Unlock()
			if strings.Contains(promptText, "safety critic") || strings.Contains(promptText, "plan critic") {
				reply = `{"verdict":"yes"}`
			}
			contentJSON, err := json.Marshal(reply)
			if err != nil {
				http.Error(w, "marshal failed", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s}}]}\n\n", contentJSON)
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		case "/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[{"id":"test-model","owned_by":"e2e"}]}`)
		case "/v1/audio/speech":
			// Mock TTS (OpenAI-shaped binary WAV response) for BlackBox /say.
			atomic.AddInt32(ttsHits, 1)
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(mockWAV)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	helixDir := filepath.Join(home, ".helix")
	if err := os.MkdirAll(helixDir, 0o755); err != nil {
		srv.Close()
		t.Fatalf("create .helix dir: %v", err)
	}
	// Pre-configure the "custom" provider so the interactive setup wizard is
	// skipped entirely and all planner traffic routes to the mock server.
	cfg := map[string]interface{}{
		"provider":                 "custom",
		"provider_model":           "test-model",
		"custom_provider_base_url": srv.URL,
		"user_preferences": map[string]interface{}{
			"typing_effect": false,
			"user_name":     "E2E",
		},
	}
	if os.Getenv("HELIX_E2E_SPEECH") != "" {
		cfg["speech"] = map[string]interface{}{
			"tts": map[string]interface{}{
				"provider": "openai",
				"base_url": srv.URL + "/v1",
			},
		}
	}
	cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		srv.Close()
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(helixDir, "config.json"), cfgBytes, 0o644); err != nil {
		srv.Close()
		t.Fatalf("write config: %v", err)
	}

	// Pre-seed the knowledge meta key so the background bootstrap skips all
	// network fetches, keeping the harness fully offline and hermetic.
	db, err := rag.OpenDB(home)
	if err != nil {
		srv.Close()
		t.Fatalf("open rag db: %v", err)
	}
	if _, err := db.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('knowledge_last_update','e2e-seed')"); err != nil {
		_ = db.Close()
		srv.Close()
		t.Fatalf("seed knowledge meta: %v", err)
	}
	if err := db.Close(); err != nil {
		srv.Close()
		t.Fatalf("close rag db: %v", err)
	}

	cmd := exec.Command(binPath)
	cmd.Dir = project
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"CUSTOM_API_KEY=test",
		"OPENAI_API_KEY=test", // speech TTS chain ("tts.openai" env mapping)
		"HELIX_MODEL_DIR=" + filepath.Join(home, "models"),
		"TERM=xterm-256color",
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		srv.Close()
		t.Fatalf("pty start: %v", err)
	}

	h := &harness{
		t:        t,
		ptmx:     ptmx,
		cmd:      cmd,
		srv:      srv,
		home:     home,
		project:  project,
		chatHits: hits,
		ttsHits:  ttsHits,
		plannerPrompts: func() []string {
			promptMu.Lock()
			defer promptMu.Unlock()
			return append([]string(nil), prompts...)
		},
	}
	go h.readLoop()

	if err := h.Expect("Helix Native Shell", 30*time.Second); err != nil {
		h.Close()
		t.Fatalf("helix did not reach the interactive prompt: %v", err)
	}
	// Let the raw-mode line reader settle before injecting keystrokes.
	time.Sleep(1 * time.Second)
	return h
}

// readLoop continuously drains the PTY into an in-memory buffer.
func (h *harness) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := h.ptmx.Read(buf)
		if n > 0 {
			h.outMu.Lock()
			h.outBuf.Write(buf[:n])
			h.outMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// stripped returns the ANSI-free snapshot of all captured output.
func (h *harness) stripped() string {
	h.outMu.Lock()
	defer h.outMu.Unlock()
	return ansiRe.ReplaceAllString(h.outBuf.String(), "")
}

// Expect polls the ANSI-stripped output until substr appears or timeout elapses.
func (h *harness) Expect(substr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(h.stripped(), substr) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %q\n----- captured output -----\n%s", substr, h.stripped())
}

// WriteLine sends a line of input followed by Enter.
func (h *harness) WriteLine(line string) {
	_, _ = h.ptmx.Write([]byte(line + "\r"))
}

// SendExpect sends a line, then waits for one MORE occurrence of substr than
// was captured before the send. Expect alone cannot synchronize a second
// command that prints the same marker as an earlier one (e.g. the GRID STATUS
// line after every input): it matches the stale occurrence and returns while
// the command is still running.
func (h *harness) SendExpect(line, substr string, timeout time.Duration) error {
	base := strings.Count(h.stripped(), substr)
	h.WriteLine(line)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Count(h.stripped(), substr) > base {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for next %q\n----- captured output -----\n%s", substr, h.stripped())
}

// ExpectFile polls until path exists, dumping the captured TUI transcript on
// failure: under a PTY the transcript is the only way to see why a command
// produced no file.
func (h *harness) ExpectFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("expected file %q to exist within %v\n----- captured output -----\n%s", path, timeout, h.stripped())
}

// ChatHits returns the number of planner/chat requests the mock has served.
func (h *harness) ChatHits() int32 {
	return atomic.LoadInt32(h.chatHits)
}

// TTSHits returns the number of mock TTS synthesis requests served.
func (h *harness) TTSHits() int32 {
	return atomic.LoadInt32(h.ttsHits)
}

// mockWAV is a minimal 16-bit mono 8kHz WAV (2 frames) for the mock TTS.
var mockWAV = buildMockWAV()

func buildMockWAV() []byte {
	samples := []int16{1000, -1000}
	data := make([]byte, len(samples)*2)
	for i, s := range samples {
		data[i*2] = byte(s)
		data[i*2+1] = byte(s >> 8)
	}
	var buf []byte
	buf = append(buf, "RIFF"...)
	buf = append(buf, 0x24, 0x00, 0x00, 0x00)
	buf = append(buf, "WAVE"...)
	buf = append(buf, "fmt "...)
	buf = append(buf, 16, 0x00, 0x00, 0x00)
	buf = append(buf, 1, 0x00)                // PCM
	buf = append(buf, 1, 0x00)                // mono
	buf = append(buf, 0x40, 0x1F, 0x00, 0x00) // 8000 Hz
	buf = append(buf, 0x80, 0x3E, 0x00, 0x00) // byte rate
	buf = append(buf, 2, 0x00)                // block align
	buf = append(buf, 16, 0x00)               // bits
	buf = append(buf, "data"...)
	buf = append(buf, byte(len(data)), 0x00, 0x00, 0x00)
	return append(buf, data...)
}

// Close terminates the helix process and releases the PTY and mock server.
func (h *harness) Close() {
	h.closeOnce.Do(func() {
		_, _ = h.ptmx.WriteString("exit\r")
		done := make(chan error, 1)
		go func() { done <- h.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			if h.cmd.Process != nil {
				_ = h.cmd.Process.Kill()
			}
			<-done
		}
		_ = h.ptmx.Close()
		h.srv.Close()
	})
}

// expectFile polls until path exists or timeout elapses.
func expectFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("expected file %q to exist within %v", path, timeout)
}

// unusedPlan is a harmless planner response for scenarios that must NOT call AI.
const unusedPlan = `{"intent":"response","steps":[{"tool":"response","message":"unused"}]}`

// Scenario (a): a high-confidence shell command bypasses the planner entirely.
func TestE2E_DirectShellBypassesPlanner(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	marker := "e2e_a_marker.txt"
	if err := os.WriteFile(filepath.Join(h.project, marker), []byte("x"), 0o644); err != nil {
		t.Fatalf("create marker: %v", err)
	}
	before := h.ChatHits()
	h.WriteLine("ls -la")
	if err := h.Expect("GRID STATUS", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect(marker, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := h.ChatHits(); got != before {
		t.Fatalf("direct shell command must not call the planner; chat hits %d -> %d", before, got)
	}
}

// Scenario (b): natural language routes through the planner and executes the plan.
func TestE2E_NaturalLanguageUsesPlanner(t *testing.T) {
	plan := `{"intent":"shell","steps":[{"tool":"shell","command":"touch planner_ran.txt"}]}`
	h := newHarness(t, plan)
	defer h.Close()

	before := h.ChatHits()
	h.WriteLine("please create a marker file for me")
	if err := h.Expect("GRID STATUS", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := h.ChatHits(); got <= before {
		t.Fatalf("natural language must call the planner; chat hits %d -> %d", before, got)
	}
	expectFile(t, filepath.Join(h.project, "planner_ran.txt"), 5*time.Second)
}

// Scenario (c): a medium-risk command prompts for confirmation; declining skips it.
func TestE2E_MediumRiskConfirmationDeclined(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	if err := os.WriteFile(filepath.Join(h.project, "target.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("create target: %v", err)
	}
	h.WriteLine("sed -i s/a/b/ target.txt")
	if err := h.Expect("Execute anyway?", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	h.WriteLine("n")
	if err := h.Expect("Command skipped", 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

// Scenario (d): a high-risk command is hard-blocked before execution.
func TestE2E_HighRiskBlocked(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	before := h.ChatHits()
	h.WriteLine("rm -rf /")
	if err := h.Expect("GRID STATUS", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("dangerous pattern", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := h.ChatHits(); got != before {
		t.Fatalf("blocked command must not call the planner; chat hits %d -> %d", before, got)
	}
}

// Scenario (e): non-interactive mode blocks high-risk commands with a non-zero exit.
func TestE2E_NonInteractiveBlocksHighRisk(t *testing.T) {
	home := t.TempDir()
	cmd := exec.Command(binPath, "-c", "rm -rf /")
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected a non-zero exit for a high-risk non-interactive command")
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 0 {
		t.Fatal("expected non-zero exit code")
	}
	if !strings.Contains(stderr.String(), "blocked") {
		t.Fatalf("expected a blocked message on stderr, got: %q", stderr.String())
	}
}

// Scenario (f): /help renders the SOS protocol.
func TestE2E_HelpRenders(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	h.WriteLine("/help")
	if err := h.Expect("SOS PROTOCOL", 15*time.Second); err != nil {
		t.Fatal(err)
	}
}

// Scenario (g): /purge respects a declined confirmation and deletes nothing.
func TestE2E_PurgeCancelled(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	cfgPath := filepath.Join(h.home, ".helix", "config.json")
	h.WriteLine("/purge")
	if err := h.Expect("FULL PURGE", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	h.WriteLine("n")
	if err := h.Expect("Purge cancelled", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config.json must survive a declined purge: %v", err)
	}
}

// extractPromptFromRequest pulls the first message content from an
// OpenAI-compatible chat request body (used to route critic calls).
func extractPromptFromRequest(r *http.Request) string {
	var body struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	defer func() { _ = r.Body.Close() }()
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return ""
	}
	if len(body.Messages) == 0 {
		return ""
	}
	return body.Messages[0].Content
}
