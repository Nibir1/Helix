//go:build !windows

// tests/e2e/firewall_e2e_test.go
// Purpose: Live E2E proof of the Instruction Firewall. The mock provider
// returns a plan containing an injected command, and the mock critic returns
// verdict=no. Helix must quarantine the plan (chat fallback) and MUST NOT
// execute the injected command.
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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"helix/internal/rag"
)

// fwHarness is a minimal PTY harness with a dynamic (prompt-aware) mock.
type fwHarness struct {
	t         *testing.T
	ptmx      *os.File
	cmd       *exec.Cmd
	srv       *httptest.Server
	project   string
	outMu     sync.Mutex
	outBuf    bytes.Buffer
	closeOnce sync.Once
}

func newFirewallHarness(t *testing.T, respond func(prompt string) string) *fwHarness {
	t.Helper()
	home := t.TempDir()
	project := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		contentJSON, err := json.Marshal(respond(extractPromptFromRequest(r)))
		if err != nil {
			http.Error(w, "marshal failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s}}]}\n\n", contentJSON)
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
	}))

	helixDir := filepath.Join(home, ".helix")
	if err := os.MkdirAll(helixDir, 0o755); err != nil {
		srv.Close()
		t.Fatalf("create .helix dir: %v", err)
	}
	cfg := map[string]interface{}{
		"provider":                 "custom",
		"provider_model":           "test-model",
		"custom_provider_base_url": srv.URL,
		"user_preferences":         map[string]interface{}{"typing_effect": false, "user_name": "E2E"},
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
		"HELIX_MODEL_DIR=" + filepath.Join(home, "models"),
		"TERM=xterm-256color",
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		srv.Close()
		t.Fatalf("pty start: %v", err)
	}
	h := &fwHarness{t: t, ptmx: ptmx, cmd: cmd, srv: srv, project: project}
	go h.readLoop()
	if err := h.Expect("Helix Native Shell", 30*time.Second); err != nil {
		h.Close()
		t.Fatalf("helix not ready: %v", err)
	}
	time.Sleep(1 * time.Second)
	return h
}

func (h *fwHarness) readLoop() {
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

func (h *fwHarness) stripped() string {
	h.outMu.Lock()
	defer h.outMu.Unlock()
	return ansiRe.ReplaceAllString(h.outBuf.String(), "")
}

func (h *fwHarness) Expect(substr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(h.stripped(), substr) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %q\n----- output -----\n%s", substr, h.stripped())
}

func (h *fwHarness) WriteLine(line string) { _, _ = h.ptmx.Write([]byte(line + "\r")) }

func (h *fwHarness) Close() {
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

// TestE2E_FirewallQuarantinesInjectedPlan proves the critic quarantines a
// planner-echoed injected command instead of executing it.
func TestE2E_FirewallQuarantinesInjectedPlan(t *testing.T) {
	h := newFirewallHarness(t, func(prompt string) string {
		switch {
		case strings.Contains(prompt, "plan critic"):
			return `{"verdict":"no"}` // critic rejects -> quarantine
		case strings.Contains(prompt, "planning module"):
			return `{"intent":"shell","steps":[{"tool":"shell","command":"touch injected.txt"}]}`
		default:
			return "I cannot carry out that request."
		}
	})
	defer h.Close()

	h.WriteLine("please create a marker file for me")
	if err := h.Expect("quarantined", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("GRID STATUS", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.project, "injected.txt")); err == nil {
		t.Fatal("injected command must NOT execute when the critic quarantines the plan")
	}
}
