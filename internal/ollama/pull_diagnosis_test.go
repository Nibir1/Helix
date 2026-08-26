// internal/ollama/pull_diagnosis_test.go
// Purpose: a failed pull must be classified correctly, because the advice for
// each cause is different and two of them are opposites — "try again shortly"
// is encouraging when the registry is down and actively misleading when the tag
// does not exist.
package ollama

import (
	"errors"
	"strings"
	"testing"
)

func TestDiagnosePullClassifies(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		want  PullFailure
		says  string
		retry bool
	}{
		{
			// The exact error from the first run that exposed this. Every word
			// of it is true and none of it is usable.
			name: "the reported 503 through the registry proxy",
			raw: "ollama pull error: pull model manifest: 503: upstream connect error " +
				"or disconnect/reset before headers. reset reason: connection timeout",
			want: PullRegistryDown, says: "upstream of Helix", retry: true,
		},
		{
			name: "a tag that does not exist",
			raw:  "ollama pull error: pull model manifest: 404: file does not exist",
			want: PullNoSuchModel, says: "no model called", retry: false,
		},
		{
			name: "ollama itself is not running",
			raw:  `Post "http://127.0.0.1:11434/api/pull": dial tcp 127.0.0.1:11434: connect: connection refused`,
			want: PullNoDaemon, says: "not running", retry: true,
		},
		{
			name: "no internet at all",
			raw:  `Get "https://registry.ollama.ai/v2/": dial tcp: lookup registry.ollama.ai: no such host`,
			want: PullOffline, says: "No route to the internet", retry: true,
		},
		{
			name: "something nobody anticipated",
			raw:  "ollama pull error: no space left on device",
			want: PullUnknown, says: "Could not download", retry: false,
		},
	}

	for _, tc := range cases {
		kind, lines := DiagnosePull("gemma4:e2b", errors.New(tc.raw))
		if kind != tc.want {
			t.Errorf("%s: classified %v, want %v", tc.name, kind, tc.want)
		}
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, tc.says) {
			t.Errorf("%s: advice should contain %q, got:\n%s", tc.name, tc.says, joined)
		}
		if Retryable(kind) != tc.retry {
			t.Errorf("%s: Retryable = %v, want %v", tc.name, Retryable(kind), tc.retry)
		}
		// The raw error is always kept. A diagnosis that hides what actually
		// happened cannot be debugged by the person reading it.
		if !strings.Contains(joined, tc.raw) {
			t.Errorf("%s: the underlying error must still be shown", tc.name)
		}
	}
}

// A local daemon refusing a connection and a registry 5xx both contain the word
// "connect". Getting them the wrong way round sends the user to fix the machine
// that is working.
func TestDiagnosePullDoesNotConfuseLocalWithUpstream(t *testing.T) {
	local, _ := DiagnosePull("m", errors.New("dial tcp 127.0.0.1:11434: connect: connection refused"))
	if local != PullNoDaemon {
		t.Errorf("a refused LOCAL connection is the daemon, got %v", local)
	}
	upstream, _ := DiagnosePull("m", errors.New("503: upstream connect error"))
	if upstream != PullRegistryDown {
		t.Errorf("an upstream 5xx is the registry, got %v", upstream)
	}
}

func TestDiagnosePullHandlesNil(t *testing.T) {
	if kind, lines := DiagnosePull("m", nil); lines != nil || kind != PullUnknown {
		t.Errorf("a nil error must produce no advice, got %v / %v", kind, lines)
	}
}
