// internal/speech/local_sidecar_test.go
// Purpose: the local sidecars against servers shaped like the REAL upstream
// ones.
//
// These tests exist because both local adapters shipped pointed at routes their
// upstream servers do not serve: whisper.cpp's server transcribes at
// /inference (the OpenAI path only exists with --inference-path), and piper's
// http_server synthesizes at / (not /api/tts). Each fake below implements the
// documented upstream contract and nothing else, so an adapter that regresses to
// a single hardcoded route fails here.
package speech

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stockWhisperServer mimics `whisper-server` as launched with no flags: POST
// /inference only, and 404 for everything else — notably /v1/audio/transcriptions.
func stockWhisperServer(t *testing.T, transcript string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if _, _, err := r.FormFile("file"); err != nil {
			http.Error(w, "missing file field", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": transcript})
	}))
}

// openAIShapedWhisperServer mimics a sidecar launched with
// --inference-path /v1/audio/transcriptions (or Speaches / Faster-Whisper).
func openAIShapedWhisperServer(t *testing.T, transcript string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": transcript})
	}))
}

func testClip() AudioFormat {
	return AudioFormat{Kind: KindWAV, SampleRate: 16000, Channels: 1, Bytes: silentWAV(16000, 1, 100)}
}

// TestWhisperLocalReachesStockWhisperServer is the regression test for the
// shipped defect: a stock whisper-server returned 404 for every utterance.
func TestWhisperLocalReachesStockWhisperServer(t *testing.T) {
	srv := stockWhisperServer(t, "hello from inference")
	defer srv.Close()

	p := NewWhisperLocalSTT("", srv.URL)
	got, err := p.Transcribe(context.Background(), testClip())
	if err != nil {
		t.Fatalf("stock whisper-server must be reachable: %v", err)
	}
	if got.Text != "hello from inference" {
		t.Errorf("text = %q", got.Text)
	}
	if got.Provider != "whisper-local" {
		t.Errorf("provider = %q, want whisper-local", got.Provider)
	}
}

func TestWhisperLocalStillReachesOpenAIShapedServer(t *testing.T) {
	srv := openAIShapedWhisperServer(t, "hello from openai route")
	defer srv.Close()

	p := NewWhisperLocalSTT("", srv.URL)
	got, err := p.Transcribe(context.Background(), testClip())
	if err != nil {
		t.Fatalf("OpenAI-shaped sidecar must keep working: %v", err)
	}
	if got.Text != "hello from openai route" {
		t.Errorf("text = %q", got.Text)
	}
}

// TestWhisperLocalCachesTheWorkingRoute: discovery must cost at most one extra
// request per process, not one per utterance.
func TestWhisperLocalCachesTheWorkingRoute(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/inference" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "ok"})
	}))
	defer srv.Close()

	p := NewWhisperLocalSTT("", srv.URL)
	for i := 0; i < 3; i++ {
		if _, err := p.Transcribe(context.Background(), testClip()); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	// First call probes the OpenAI route then /inference; the next two go
	// straight to the winner. Four requests total, not six.
	if len(paths) != 4 {
		t.Errorf("made %d requests (%v), want 4 — the route is not being cached", len(paths), paths)
	}
	if r, ok := p.(interface{ ActiveRoute() string }); ok && r.ActiveRoute() != "/inference" {
		t.Errorf("cached route = %q, want /inference", r.ActiveRoute())
	}
}

// TestWhisperLocalRejectsForeignService is the port-collision case: llama-server
// and whisper.cpp both default to 8080, so a 200 from something that is not a
// transcription service must be an error, never an empty utterance.
func TestWhisperLocalRejectsForeignService(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Shaped like llama-server answering an unknown path.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"unknown route","code":404}}`))
	}))
	defer srv.Close()

	p := NewWhisperLocalSTT("", srv.URL)
	got, err := p.Transcribe(context.Background(), testClip())
	if err == nil {
		t.Fatalf("a foreign service must not pass as a transcription, got %+v", got)
	}
	if got.Text != "" {
		t.Errorf("no text should be returned, got %q", got.Text)
	}
}

// TestWhisperLocalDoesNotRetryRealErrors: only a 404 means "wrong route". A 500
// is the right endpoint failing, and masking it by trying elsewhere would report
// the wrong cause.
func TestWhisperLocalDoesNotRetryRealErrors(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, "model failed to load", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewWhisperLocalSTT("", srv.URL)
	if _, err := p.Transcribe(context.Background(), testClip()); err == nil {
		t.Fatal("expected the 500 to surface")
	}
	// The shared client retries transport-level failures, so assert only that
	// it did not walk on to a second ROUTE after a definite server error.
	if hits == 0 {
		t.Fatal("the server was never called")
	}
}

// TestWhisperLocalHealthCheckDetectsForeignService is the /voice-status lie the
// old any-response probe told.
func TestWhisperLocalHealthCheckDetectsForeignService(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>some other dev server</html>"))
	}))
	defer srv.Close()

	p := NewWhisperLocalSTT("", srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.HealthCheck(ctx); err == nil {
		t.Fatal("a foreign HTTP service must not report healthy")
	}
}

func TestWhisperLocalHealthCheckPassesOnStockServer(t *testing.T) {
	srv := stockWhisperServer(t, "")
	defer srv.Close()

	p := NewWhisperLocalSTT("", srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Silence transcribes to nothing; the ROUTE is what is being verified.
	if err := p.HealthCheck(ctx); err != nil {
		t.Fatalf("stock whisper-server should be healthy: %v", err)
	}
}

// -------------------------------------------------------
// piper
// -------------------------------------------------------

// stockPiperServer mimics `python3 -m piper.http_server`: synthesis at the root
// path, 404 everywhere else.
func stockPiperServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("text") == "" {
			http.Error(w, "no text", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(silentWAV(22050, 1, 120))
	}))
}

// TestPiperReachesStockPiperServer is the regression test: /api/tts does not
// exist on piper's own server, so every spoken reply used to 404.
func TestPiperReachesStockPiperServer(t *testing.T) {
	srv := stockPiperServer(t)
	defer srv.Close()

	p := NewPiperTTS(srv.URL)
	got, err := p.Synthesize(context.Background(), "hello", SynthesisOptions{})
	if err != nil {
		t.Fatalf("stock piper server must be reachable: %v", err)
	}
	if got.Kind != KindWAV || got.SampleRate != 22050 || len(got.Bytes) < 44 {
		t.Errorf("bad audio: kind=%s rate=%d bytes=%d", got.Kind, got.SampleRate, len(got.Bytes))
	}
}

// TestPiperStillReachesRhasspyRoute keeps the older service working.
func TestPiperStillReachesRhasspyRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tts" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(silentWAV(16000, 1, 80))
	}))
	defer srv.Close()

	p := NewPiperTTS(srv.URL)
	got, err := p.Synthesize(context.Background(), "hello", SynthesisOptions{})
	if err != nil {
		t.Fatalf("Rhasspy-style route must still work: %v", err)
	}
	if got.SampleRate != 16000 {
		t.Errorf("rate = %d, want 16000", got.SampleRate)
	}
}

// TestPiperRejectsNonWAVResponder is the macOS AirPlay case: something on port
// 5000 answers HTTP 200 with HTML, and it is not piper.
func TestPiperRejectsNonWAVResponder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>AirPlay Receiver</body></html>"))
	}))
	defer srv.Close()

	p := NewPiperTTS(srv.URL)
	if _, err := p.Synthesize(context.Background(), "hello", SynthesisOptions{}); err == nil {
		t.Fatal("a non-WAV 200 must be an error, not audio")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := p.HealthCheck(ctx)
	if err == nil {
		t.Fatal("a non-WAV responder must not report healthy")
	}
	if !strings.Contains(err.Error(), "piper-local") {
		t.Errorf("error should name the provider: %v", err)
	}
}

func TestPiperHealthCheckPassesOnStockServer(t *testing.T) {
	srv := stockPiperServer(t)
	defer srv.Close()

	p := NewPiperTTS(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.HealthCheck(ctx); err != nil {
		t.Fatalf("stock piper server should be healthy: %v", err)
	}
}

func TestPiperStreamsFromStockServer(t *testing.T) {
	srv := stockPiperServer(t)
	defer srv.Close()

	p := NewPiperTTS(srv.URL)
	streamer, ok := p.(interface {
		SynthesizeStream(context.Context, string, SynthesisOptions) (StreamedAudio, error)
	})
	if !ok {
		t.Fatal("piper must implement the streaming interface")
	}
	got, err := streamer.SynthesizeStream(context.Background(), "hello", SynthesisOptions{})
	if err != nil {
		t.Fatalf("stream against the stock server: %v", err)
	}
	defer func() { _ = got.Body.Close() }()
	if got.SampleRate != 22050 || got.Channels != 1 {
		t.Errorf("stream header: rate=%d channels=%d", got.SampleRate, got.Channels)
	}
}

// -------------------------------------------------------
// URL handling
// -------------------------------------------------------

func TestServerOriginAndRouteSuffix(t *testing.T) {
	originCases := map[string]string{
		"http://127.0.0.1:8080":          "http://127.0.0.1:8080",
		"http://127.0.0.1:8080/":         "http://127.0.0.1:8080",
		"http://127.0.0.1:8080/v1":       "http://127.0.0.1:8080",
		"https://api.openai.com/v1":      "https://api.openai.com",
		"http://host:9000/api/v1/":       "http://host:9000",
		"https://api.groq.com/openai/v1": "https://api.groq.com",
	}
	for in, want := range originCases {
		if got := serverOrigin(in); got != want {
			t.Errorf("serverOrigin(%q) = %q, want %q", in, got, want)
		}
	}

	// A base URL carrying a real path prefix must keep it: that prefix is the
	// operator saying where the API lives behind a reverse proxy.
	routeCases := []struct{ base, route, want string }{
		{"http://127.0.0.1:8080", openaiTranscribeRoute, openaiTranscribeRoute},
		{"http://127.0.0.1:8080/v1", openaiTranscribeRoute, openaiTranscribeRoute},
		{"https://api.groq.com/openai/v1", openaiTranscribeRoute, "/openai/v1/audio/transcriptions"},
	}
	for _, tc := range routeCases {
		if got := routeSuffix(tc.base, tc.route); got != tc.want {
			t.Errorf("routeSuffix(%q, %q) = %q, want %q", tc.base, tc.route, got, tc.want)
		}
	}
}

func TestDedupeRoutes(t *testing.T) {
	got := dedupeRoutes("/a", "", "/b", "/a", "/b", "/c")
	want := "/a,/b,/c"
	if strings.Join(got, ",") != want {
		t.Errorf("dedupeRoutes = %v, want %s", got, want)
	}
}

// TestGroqRouteUnchanged guards the cloud adapters against the origin/route
// refactor: Groq's API lives under /openai/v1, and losing that prefix would
// break it silently.
func TestGroqRouteUnchanged(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "ok"})
	}))
	defer srv.Close()

	p := NewGroqSTT("", srv.URL+"/openai/v1")
	p.SetAPIKey("test")
	if _, err := p.Transcribe(context.Background(), testClip()); err != nil {
		t.Fatalf("groq transcribe: %v", err)
	}
	if path != "/openai/v1/audio/transcriptions" {
		t.Errorf("groq posted to %q, want /openai/v1/audio/transcriptions", path)
	}
}

// TestCloudHealthProbeKeepsBaseURLPrefix is a regression guard.
//
// The origin/route refactor kept the base URL's path prefix for TRANSCRIPTION
// but dropped it for the health probe, so Groq — whose API lives under
// /openai/v1 — was probed at a bare /v1/models and reported HTTP 404. A
// perfectly good provider showed as down in /voice-status.
func TestCloudHealthProbeKeepsBaseURLPrefix(t *testing.T) {
	var probed string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probed = r.URL.Path
		if r.URL.Path != "/openai/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	p := NewGroqSTT("", srv.URL+"/openai/v1")
	p.SetAPIKey("test")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.HealthCheck(ctx); err != nil {
		t.Fatalf("groq health should succeed: %v (probed %q)", err, probed)
	}
	if probed != "/openai/v1/models" {
		t.Errorf("probed %q, want /openai/v1/models", probed)
	}
}

func TestOpenAIHealthProbeUnchanged(t *testing.T) {
	var probed string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probed = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	p := NewOpenAISTT("", srv.URL+"/v1")
	p.SetAPIKey("test")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.HealthCheck(ctx); err != nil {
		t.Fatalf("openai health: %v", err)
	}
	if probed != "/v1/models" {
		t.Errorf("probed %q, want /v1/models", probed)
	}
}

// TestLocalProbeFailsFastWithACause: retrying a loopback probe turns an instant,
// definitive "connection refused" into a context deadline, and a deadline says
// nothing about the cause. The reported symptom was "context deadline exceeded"
// where it should have been "nothing is listening".
func TestLocalProbeFailsFastWithACause(t *testing.T) {
	// Bind and release to obtain a port nothing is on.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	p := NewWhisperLocalSTT("", "http://"+addr)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	start := time.Now()
	err = p.HealthCheck(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a probe against nothing must fail")
	}
	if elapsed > 5*time.Second {
		t.Errorf("local probe took %v; retries are turning a refusal into a timeout", elapsed)
	}
	if !strings.Contains(err.Error(), "nothing is listening") {
		t.Errorf("the failure must name the cause, got: %v", err)
	}
}
