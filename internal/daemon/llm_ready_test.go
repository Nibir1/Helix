// internal/daemon/llm_ready_test.go
// Purpose: BlackBox P11.3 — the startup readiness check for the offline brain
// stays inert when it has nothing to verify, so it never touches Ollama (or
// starts a multi-gigabyte download) on a machine that did not ask for it.
package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"helix/internal/agent"
	"helix/internal/config"
)

// readyProbeDaemon builds the minimum Daemon the readiness check needs: a
// journal it can write to, and the fallback settings under test.
func readyProbeDaemon(t *testing.T, fb config.LLMFallbackConfig) (*Daemon, *Journal) {
	t.Helper()
	j, err := NewJournalAt(filepath.Join(t.TempDir(), "journal", "interactions.jsonl"))
	if err != nil {
		t.Fatalf("journal: %v", err)
	}
	return &Daemon{journal: j, llmFallback: fb}, j
}

func hasKind(entries []JournalEntry, kind string) bool {
	for _, e := range entries {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func TestEnsureLocalBrainReadySkipsWhenFallbackDisabled(t *testing.T) {
	off := false
	d, j := readyProbeDaemon(t, config.LLMFallbackConfig{Enabled: &off, Provider: "ollama"})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d.ensureLocalBrainReady(ctx)

	if hasKind(j.Tail(10), "llm_ready") {
		t.Fatal("a disabled fallback must not probe or journal readiness")
	}
}

// llama-server is a user-managed sidecar (ADR-002/P7.7): it loads its GGUF at
// launch and exposes no install-or-pull API, so there is nothing for the
// readiness check to ensure beyond reachability, which sidecarHealthLoop
// already reports.
func TestEnsureLocalBrainReadySkipsLlamaCpp(t *testing.T) {
	on := true
	d, j := readyProbeDaemon(t, config.LLMFallbackConfig{
		Enabled: &on, Provider: "llamacpp", Model: "local-gguf",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d.ensureLocalBrainReady(ctx)

	if hasKind(j.Tail(10), "llm_ready") {
		t.Fatal("llama.cpp has no pull API — the readiness check must be a no-op for it")
	}
}

// The daemon must never start a multi-gigabyte model download on its own
// (guardrail §12 #1). EnsureReady defaults to false, and this pins that default
// so a future refactor cannot flip it silently.
func TestEnsureReadyDefaultsToNoDownload(t *testing.T) {
	if config.LLMDefaults().Fallback.EnsureReady {
		t.Fatal("ensure_ready must default to false — model pulls are consent-gated")
	}
}

// --- P8.8: the daemon must keep the buffered render path -------------------

// The daemon captures IPC reply text by overriding PrintAIMessage. If its
// renderer ever gained streaming support, the agent would take the live path
// and `helix remote submit` would start returning empty replies — silently,
// because Go does not dispatch an embedded override from inside the embedded
// type's own methods.
func TestDaemonRendererDoesNotStream(t *testing.T) {
	var r agent.Renderer = &daemonRenderer{}
	if _, ok := r.(agent.StreamingRenderer); ok {
		t.Fatal("daemonRenderer must not implement StreamingRenderer — " +
			"PrintAIMessage is how the daemon captures the reply for IPC")
	}
}

func TestDaemonRendererStillCapturesReply(t *testing.T) {
	r := &daemonRenderer{}
	r.PrintAIMessage("the answer", false)
	reply, errText := r.takeResult()
	if reply != "the answer" || errText != "" {
		t.Fatalf("reply capture broken: reply=%q err=%q", reply, errText)
	}
}
