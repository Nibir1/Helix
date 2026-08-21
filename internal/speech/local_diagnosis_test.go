// internal/speech/local_diagnosis_test.go
// Purpose: the failures a user actually reports, and whether the message tells
// them what to do.
//
// The case that prompted this: selecting piper-local on macOS produced
//
//	Speech failed: all TTS providers failed: piper-local: piper-local: HTTP 403:
//
// which is accurate and useless — the provider named twice, an empty body, and
// no hint that AirPlay Receiver owns port 5000 and 403s everything.
package speech

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"helix/internal/providers"
)

// airplayLikeServer answers 403 to everything, as macOS AirPlay Receiver does.
func airplayLikeServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
}

// TestPiper403IsDiagnosedNotJustReported is the reported bug.
func TestPiper403IsDiagnosedNotJustReported(t *testing.T) {
	srv := airplayLikeServer(t)
	defer srv.Close()

	p := NewPiperTTS(srv.URL)
	_, err := p.Synthesize(context.Background(), "voice link online", SynthesisOptions{})
	if err == nil {
		t.Fatal("a 403 must be an error")
	}
	msg := err.Error()

	// It must say something IS there, which is the fact that changes the fix.
	if !strings.Contains(msg, "listening") {
		t.Errorf("message should say something is listening:\n%s", msg)
	}
	// It must name the endpoint, so the user knows which address failed.
	if !strings.Contains(msg, srv.URL) {
		t.Errorf("message should name the endpoint:\n%s", msg)
	}
	// It must carry a next step, not just a status code.
	if !strings.Contains(msg, "piper.http_server") {
		t.Errorf("message should show how to start the sidecar:\n%s", msg)
	}
	// And it must name the provider exactly once.
	if n := strings.Count(msg, "piper-local"); n != 1 {
		t.Errorf("provider named %d times, want 1:\n%s", n, msg)
	}
}

// TestPiper403WalksBothRoutesFirst: a 403 on "/" must not abort the walk, since
// the other known route may still be the right one.
func TestPiper403WalksBothRoutesFirst(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/api/tts" {
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(silentWAV(22050, 1, 60))
			return
		}
		w.WriteHeader(http.StatusForbidden) // a proxy 403ing the root
	}))
	defer srv.Close()

	p := NewPiperTTS(srv.URL)
	got, err := p.Synthesize(context.Background(), "hello", SynthesisOptions{})
	if err != nil {
		t.Fatalf("a 403 on one route must not stop the walk: %v", err)
	}
	if got.SampleRate != 22050 {
		t.Errorf("rate = %d", got.SampleRate)
	}
	if len(paths) < 2 {
		t.Errorf("only tried %v; both routes should have been attempted", paths)
	}
}

// TestWhisper403WalksBothRoutes is the same rule on the STT side: llama-server
// answering 401/403 on the shared 8080 default must not stop the walk.
func TestWhisper403WalksBothRoutes(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/inference" {
			_, _ = w.Write([]byte(`{"text":"heard it"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	p := NewWhisperLocalSTT("", srv.URL)
	got, err := p.Transcribe(context.Background(), testClip())
	if err != nil {
		t.Fatalf("a 403 on one route must not stop the walk: %v", err)
	}
	if got.Text != "heard it" {
		t.Errorf("text = %q", got.Text)
	}
	if len(paths) < 2 {
		t.Errorf("only tried %v", paths)
	}
}

// TestServerErrorDoesNotWalkOn: a 5xx is the RIGHT endpoint failing. Trying
// elsewhere would report the wrong cause.
func TestServerErrorDoesNotWalkOn(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		http.Error(w, "voice model failed to load", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewPiperTTS(srv.URL)
	if _, err := p.Synthesize(context.Background(), "hello", SynthesisOptions{}); err == nil {
		t.Fatal("expected the 500 to surface")
	}
	// The shared client retries transport failures, so assert on distinct routes.
	distinct := map[string]bool{}
	for _, p := range paths {
		distinct[p] = true
	}
	if len(distinct) > 1 {
		t.Errorf("a 500 must not cause a second ROUTE to be tried, saw %v", distinct)
	}
}

func TestLocalDiagnosisConnectionRefused(t *testing.T) {
	err := LocalDiagnosis("kokoro-local", "http://127.0.0.1:8880",
		"docker run -p 8880:8880 image", "tts-url", syscall.ECONNREFUSED)

	msg := err.Error()
	if !strings.Contains(msg, "nothing is listening") {
		t.Errorf("refused connection should say nothing is listening:\n%s", msg)
	}
	if !strings.Contains(msg, "docker run") {
		t.Errorf("should carry the launch command:\n%s", msg)
	}
	// Not the wrong diagnosis: nothing there is not the same as something there.
	if strings.Contains(msg, "IS listening") {
		t.Errorf("must not claim something is listening:\n%s", msg)
	}
}

func TestLocalDiagnosisNamesAirPlayOnDarwin(t *testing.T) {
	err := LocalDiagnosis("piper-local", "http://127.0.0.1:5000",
		piperStartCmd, piperCfgKey, &providers.StatusError{Code: 403, Snippet: ""})

	msg := err.Error()
	if runtime.GOOS == "darwin" {
		if !strings.Contains(msg, "AirPlay") {
			t.Errorf("on macOS a 403 on port 5000 should name AirPlay Receiver:\n%s", msg)
		}
		if !strings.Contains(msg, "System Settings") {
			t.Errorf("should say how to turn it off:\n%s", msg)
		}
	} else {
		// Elsewhere there is no known culprit, so it must not invent one — it
		// should hand over the command that finds the real owner.
		if strings.Contains(msg, "AirPlay") {
			t.Errorf("AirPlay is macOS-only; do not name it on %s:\n%s", runtime.GOOS, msg)
		}
		if !strings.Contains(msg, "lsof") {
			t.Errorf("should show how to find the port owner:\n%s", msg)
		}
	}
}

func TestLocalDiagnosisNotFound(t *testing.T) {
	err := LocalDiagnosis("whisper-local", "http://127.0.0.1:8080",
		whisperStartCmd, whisperCfgKey, &providers.StatusError{Code: 404, Snippet: ""})

	msg := err.Error()
	if !strings.Contains(msg, "none of the routes") {
		t.Errorf("a 404 on every route should say so:\n%s", msg)
	}
	if !strings.Contains(msg, "/config stt-url") {
		t.Errorf("should offer the repoint command:\n%s", msg)
	}
}

func TestLocalDiagnosisPassesThroughUnknownErrors(t *testing.T) {
	err := LocalDiagnosis("piper-local", "http://127.0.0.1:5000", "", "", errors.New("tls handshake timeout"))
	if !strings.Contains(err.Error(), "tls handshake timeout") {
		t.Errorf("an unrecognized cause must still be reported verbatim: %v", err)
	}
}

func TestLocalDiagnosisNilIsNil(t *testing.T) {
	if err := LocalDiagnosis("x", "y", "z", "k", nil); err != nil {
		t.Errorf("nil in, nil out; got %v", err)
	}
}

func TestEndpointPort(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:5000": "5000",
		"http://127.0.0.1":      "80",
		"https://example.com":   "443",
		"http://host:8880/v1":   "8880",
		"garbage":               "?",
	}
	for in, want := range cases {
		if got := endpointPort(in); got != want {
			t.Errorf("endpointPort(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestChainErrorNamesEachProviderOnce guards the doubled-prefix regression at
// the level the user actually reads: the failover message.
func TestChainErrorNamesEachProviderOnce(t *testing.T) {
	labelled := labelProviderErr("piper-local", errors.New("piper-local: HTTP 403"))
	if n := strings.Count(labelled.Error(), "piper-local"); n != 1 {
		t.Errorf("self-labelled error was prefixed again: %q", labelled)
	}

	// An adapter that forgot to label itself still gets named — that is the
	// case where the name matters most.
	anonymous := labelProviderErr("kokoro-local", errors.New("connection refused"))
	if !strings.HasPrefix(anonymous.Error(), "kokoro-local:") {
		t.Errorf("an unlabelled error must be given its provider: %q", anonymous)
	}
	if labelProviderErr("x", nil) != nil {
		t.Error("nil must stay nil")
	}
}

func TestDiagnosisIsQuickEnoughToPrint(t *testing.T) {
	// Guard against ever making this do I/O: it runs inside an error path.
	start := time.Now()
	for i := 0; i < 1000; i++ {
		_ = LocalDiagnosis("piper-local", "http://127.0.0.1:5000", piperStartCmd, piperCfgKey,
			&providers.StatusError{Code: 403, Snippet: ""})
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("1000 diagnoses took %v — this must stay pure string work", elapsed)
	}
}
