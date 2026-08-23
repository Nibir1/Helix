// internal/speech/live_sidecar_test.go
// Purpose: Phase 1's last open acceptance item — the local sidecar adapters
// against a REAL server rather than a faithful fake.
//
// local_sidecar_test.go already pins the wire contract against httptest servers
// shaped like the upstream ones, and that is the right test for CI. It cannot,
// however, catch the class of bug that made this item worth keeping open: the
// adapter shipped speaking only /v1/audio/transcriptions while a stock
// `whisper-server` serves /inference, so local STT was unusable as shipped and
// every mock in the repo agreed with the mock. Only a real binary settles
// whether route discovery works against the thing it was written for.
//
// Honest about the gating (§9 rules 1 and 6): this needs no audio hardware —
// input is a file — but it does need the whisper.cpp binary, a model, and a way
// to synthesize speech with known ground truth. When any of those is missing it
// SKIPS LOUDLY with the reason, so a CI run reports "not exercised" rather than
// passing silently. Set HELIX_LIVE_SIDECAR=1 to run it; it is off by default
// because loading a model and transcribing costs seconds, and `make test` is
// run constantly.
package speech

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// liveSidecarEnv is the opt-in switch.
const liveSidecarEnv = "HELIX_LIVE_SIDECAR"

// requireLiveSidecar skips unless explicitly enabled, and says why.
func requireLiveSidecar(t *testing.T) {
	t.Helper()
	if os.Getenv(liveSidecarEnv) == "" {
		t.Skipf("live sidecar QA not requested — set %s=1 to run it against a real server",
			liveSidecarEnv)
	}
}

// freePort asks the kernel for an unused port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// whisperModel finds a downloaded ggml model, preferring the smaller one so the
// test starts fast.
func whisperModel(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	dir := filepath.Join(home, ".helix", "whisper-models")
	for _, name := range []string{"ggml-base.en.bin", "ggml-small.en.bin", "ggml-tiny.en.bin"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skipf("no whisper model in %s — /blackbox setup downloads one", dir)
	return ""
}

// spokenWAV synthesizes speech with known ground truth at whisper's native
// 16 kHz mono. Using a synthesizer rather than a committed fixture keeps a
// multi-megabyte binary out of the repository and makes the expected text
// obvious at the call site.
func spokenWAV(t *testing.T, text string) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skipf("no bundled speech synthesizer on %s to generate a known clip", runtime.GOOS)
	}
	if _, err := exec.LookPath("say"); err != nil {
		t.Skipf("`say` unavailable: %v", err)
	}
	path := filepath.Join(t.TempDir(), "utterance.wav")
	cmd := exec.Command("say", "-o", path, "--data-format=LEI16@16000", text)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not synthesize a test clip: %v (%s)", err, out)
	}
	return path
}

// startWhisperServer launches a stock `whisper-server` and waits for it to
// answer. "Stock" is the point: no --inference-path, so it serves /inference
// only, which is the configuration the adapter used to fail against.
func startWhisperServer(t *testing.T, model string) string {
	t.Helper()
	bin, err := exec.LookPath("whisper-server")
	if err != nil {
		t.Skipf("whisper-server not installed: %v", err)
	}

	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin,
		"-m", model, "--host", "127.0.0.1", "--port", fmt.Sprint(port))
	logFile, err := os.Create(filepath.Join(t.TempDir(), "whisper-server.log"))
	if err != nil {
		cancel()
		t.Fatalf("create server log: %v", err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		cancel()
		t.Skipf("could not start whisper-server: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
		_ = logFile.Close()
	})

	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(90 * time.Second) // model load dominates
	for time.Now().Before(deadline) {
		conn, derr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if derr == nil {
			_ = conn.Close()
			return endpoint
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("whisper-server did not accept connections on %s", endpoint)
	return ""
}

// TestLiveWhisperLocalTranscribes is the acceptance run: Helix's own adapter,
// against a real whisper.cpp server, on real synthesized speech.
func TestLiveWhisperLocalTranscribes(t *testing.T) {
	requireLiveSidecar(t)

	model := whisperModel(t)
	clipPath := spokenWAV(t, "The quick brown fox jumps over the lazy dog.")
	endpoint := startWhisperServer(t, model)

	raw, err := os.ReadFile(clipPath)
	if err != nil {
		t.Fatalf("read clip: %v", err)
	}

	// The adapter under test is exactly the one the registry builds for the
	// "whisper-local" provider — constructed the same way, with no model name
	// (a local sidecar serves whatever it was launched with).
	adapter := NewWhisperLocalSTT("", endpoint)
	if adapter.Name() != "whisper-local" {
		t.Fatalf("adapter name = %q, want whisper-local", adapter.Name())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	start := time.Now()
	tr, err := adapter.Transcribe(ctx, AudioFormat{
		Kind:       "wav",
		SampleRate: 16000,
		Channels:   1,
		Bytes:      raw,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("live transcription failed: %v", err)
	}

	got := strings.ToLower(strings.TrimSpace(tr.Text))
	t.Logf("live whisper-local transcript in %s: %q", elapsed.Round(time.Millisecond), tr.Text)

	// Assert on content words rather than an exact string: a real model is
	// allowed to differ on punctuation and casing, and pinning those would make
	// this fail on a model upgrade for no good reason.
	for _, word := range []string{"quick", "brown", "fox", "lazy", "dog"} {
		if !strings.Contains(got, word) {
			t.Errorf("transcript is missing %q — got %q", word, got)
		}
	}
	if tr.Provider != "whisper-local" {
		t.Errorf("transcript provider = %q, want whisper-local", tr.Provider)
	}
}

// The route-discovery claim, tested where it actually matters. A stock server
// has no /v1/audio/transcriptions, so an adapter that only spoke the OpenAI
// route gets a 404 — which is precisely how local STT shipped broken. This
// asserts the discovery happens and, once found, is reused rather than
// re-probed on every call.
func TestLiveWhisperLocalDiscoversStockRoute(t *testing.T) {
	requireLiveSidecar(t)

	model := whisperModel(t)
	clipPath := spokenWAV(t, "Route discovery works.")
	endpoint := startWhisperServer(t, model)

	raw, err := os.ReadFile(clipPath)
	if err != nil {
		t.Fatalf("read clip: %v", err)
	}
	clip := AudioFormat{Kind: "wav", SampleRate: 16000, Channels: 1, Bytes: raw}

	adapter := NewWhisperLocalSTT("", endpoint)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	first, err := adapter.Transcribe(ctx, clip)
	if err != nil {
		t.Fatalf("first transcription (route discovery) failed: %v", err)
	}
	if strings.TrimSpace(first.Text) == "" {
		t.Fatal("first transcription returned empty text")
	}

	// Second call on the same adapter: the remembered route must still work.
	second, err := adapter.Transcribe(ctx, clip)
	if err != nil {
		t.Fatalf("second transcription (remembered route) failed: %v", err)
	}
	if strings.TrimSpace(second.Text) == "" {
		t.Fatal("second transcription returned empty text — route memory may be wrong")
	}
	t.Logf("stock-route transcripts: %q then %q", first.Text, second.Text)
}

// The chain-level path: Helix does not call adapters directly in production, it
// walks a failover chain. This proves a registry configured with whisper-local
// as its only STT provider transcribes end to end — the shape a "fully local /
// private" preset produces.
func TestLiveWhisperLocalThroughRegistryChain(t *testing.T) {
	requireLiveSidecar(t)

	model := whisperModel(t)
	// Reuse the pangram rather than a Helix-specific phrase. The first run of
	// this test said "Helix hears me locally" and base.en returned "He looks,
	// here's me locally" — the chain worked perfectly and the assertion was one
	// synonym away from failing for reasons that have nothing to do with the
	// chain. Proper nouns and short clauses are exactly what a small model gets
	// wrong, so the audio here is chosen to be transcribed reliably.
	clipPath := spokenWAV(t, "The quick brown fox jumps over the lazy dog.")
	endpoint := startWhisperServer(t, model)

	raw, err := os.ReadFile(clipPath)
	if err != nil {
		t.Fatalf("read clip: %v", err)
	}

	reg := newTestRegistry(t)
	reg.RegisterSTT(NewWhisperLocalSTT("", endpoint))
	reg.SetConfig(Config{STT: STTConfig{Provider: "whisper-local"}})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	tr, err := reg.Transcribe(ctx, AudioFormat{
		Kind: "wav", SampleRate: 16000, Channels: 1, Bytes: raw,
	})
	if err != nil {
		t.Fatalf("registry chain transcription failed: %v", err)
	}
	got := strings.ToLower(tr.Text)
	if !strings.Contains(got, "fox") {
		t.Errorf("chain transcript does not resemble the spoken text: %q", tr.Text)
	}
	if tr.Provider != "whisper-local" {
		t.Errorf("chain reported provider %q, want whisper-local", tr.Provider)
	}
	t.Logf("registry chain transcript: %q (provider %s)", tr.Text, tr.Provider)
}

// startPiperServer launches piper's own http_server and waits for it to answer.
//
// Same reasoning as the whisper case: piper's server synthesizes at "/", not
// "/api/tts", and the adapter shipped speaking only the latter. A fake proves
// the adapter can talk to the contract this repo wrote down; only the real
// server proves the contract was right.
func startPiperServer(t *testing.T, voice string) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 unavailable: %v", err)
	}
	// piper's HTTP server is a module, not a binary, so probe for importability
	// rather than for a name on PATH.
	if out, err := exec.Command("python3", "-c", "import piper.http_server").CombinedOutput(); err != nil {
		t.Skipf("piper http_server not installed: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "python3", "-m", "piper.http_server",
		"-m", voice, "--host", "127.0.0.1", "--port", fmt.Sprint(port))
	logFile, err := os.Create(filepath.Join(t.TempDir(), "piper-server.log"))
	if err != nil {
		cancel()
		t.Fatalf("create server log: %v", err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		cancel()
		t.Skipf("could not start piper http_server: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
		_ = logFile.Close()
	})

	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		conn, derr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if derr == nil {
			_ = conn.Close()
			return endpoint
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("piper http_server did not accept connections on %s", endpoint)
	return ""
}

// piperVoice finds an installed .onnx voice.
func piperVoice(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	dir := filepath.Join(home, ".helix", "piper-voices")
	matches, err := filepath.Glob(filepath.Join(dir, "*.onnx"))
	if err != nil || len(matches) == 0 {
		t.Skipf("no piper voice in %s — /blackbox setup downloads one", dir)
	}
	return matches[0]
}

// TestLivePiperLocalSynthesizes is the TTS half of the local-sidecar acceptance
// run: real server, real synthesis, and the result must be audio Helix can
// actually decode.
//
// It deliberately does NOT play the audio. Playback needs a device, §9 rule 1
// keeps audio hardware out of the test suite, and "can Helix decode what the
// sidecar returned" is the part that has ever been broken. Whether the speaker
// works is a different question from whether the bytes are right.
func TestLivePiperLocalSynthesizes(t *testing.T) {
	requireLiveSidecar(t)

	voice := piperVoice(t)
	endpoint := startPiperServer(t, voice)

	adapter := NewPiperTTS(endpoint)
	if adapter.Name() != "piper-local" {
		t.Fatalf("adapter name = %q, want piper-local", adapter.Name())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	start := time.Now()
	out, err := adapter.Synthesize(ctx, "Helix voice link online.", SynthesisOptions{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("live synthesis failed: %v", err)
	}
	if len(out.Bytes) == 0 {
		t.Fatal("synthesis returned no audio bytes")
	}
	t.Logf("live piper-local synthesized %d bytes (%s) in %s",
		len(out.Bytes), out.Kind, elapsed.Round(time.Millisecond))

	// The payoff: the bytes must survive Helix's OWN decoder, since that is what
	// stands between a sidecar's response and the speaker. A server returning a
	// container Helix cannot parse is indistinguishable from silence at runtime.
	samples, err := DecodeWAVPCM16(out.Bytes)
	if err != nil {
		t.Fatalf("piper output is not decodable by Helix's WAV decoder: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("decoded zero samples — the clip would be silent")
	}

	rate, channels, err := wavHeaderInfo(out.Bytes)
	if err != nil {
		t.Fatalf("piper output has an unreadable WAV header: %v", err)
	}
	if rate <= 0 || channels <= 0 {
		t.Fatalf("implausible WAV header: %d Hz, %d channels", rate, channels)
	}
	t.Logf("decoded %d samples at %d Hz, %d channel(s)", len(samples), rate, channels)

	// Non-silence check: a header-only or all-zero clip decodes fine and plays
	// as nothing, which is exactly the failure a smoke test should catch.
	var peak int16
	for _, s := range samples {
		if s > peak {
			peak = s
		}
	}
	if peak == 0 {
		t.Fatal("decoded audio is entirely silent")
	}
}
