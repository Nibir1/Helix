// internal/speech/piper_native.go
// Purpose: run Piper WITHOUT Python.
//
// Piper's HTTP server is a Python module (`python3 -m piper.http_server`), and
// for a long time that made the default private voice the one component of
// Helix that required a Python interpreter — in a project whose whole point is
// a CGO-free single binary, whose CSM integration was built around a Rust
// sidecar specifically to avoid PyTorch, and whose architecture doc claims a
// "no-Python rule". Piper was the exception nobody had removed.
//
// Worse than the inconsistency: on a machine with NO Python, setup offered to
// run `python3 -m pip install --user piper-tts flask` — a command that begins
// with the interpreter that is missing. Helix walked the user into an install
// that could not run, which is exactly the failure the `Unmet` precondition was
// invented for one sidecar over.
//
// Piper also ships standalone prebuilt binaries for every platform Helix
// targets (linux x86_64/aarch64/armv7l, macOS aarch64/x64, windows amd64). They
// need no interpreter, no server, and no port — which additionally retires the
// macOS AirPlay-on-5000 collision the wizard keeps working around.
//
// The trade-off, stated because it is real: those binaries come from the
// 2023.11.14 release, after which Piper's development moved to a Python-first
// rewrite. They are self-contained and they work; they are also frozen. The
// HTTP path remains for anyone running a newer server.
package speech

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ErrNoPiperBinary means no usable native piper was found.
var ErrNoPiperBinary = errors.New("no native piper binary found")

// PiperBinaryNames are the executable names to look for, in preference order.
var piperBinaryNames = []string{"piper", "piper-tts"}

// FindPiperBinary locates a native piper executable.
//
// Helix's own install directory is searched BEFORE PATH: a `piper` on PATH may
// be the Python console script, which is a shim around the module this file
// exists to avoid. The one Helix downloaded is known to be the standalone
// build.
func FindPiperBinary() (string, error) {
	if home, err := os.UserHomeDir(); err == nil {
		own := filepath.Join(home, ".helix", "piper", piperExecutableName())
		if isExecutableFile(own) && IsNativePiperBinary(own) {
			return own, nil
		}
	}
	for _, name := range piperBinaryNames {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		// Searching Helix's own directory first was not enough of a guard.
		//
		// `pip install piper-tts` drops a `piper` CONSOLE SCRIPT on PATH, and on
		// a machine that never downloaded the standalone build, LookPath finds
		// it. Returning it here makes newPiperProvider wrap a Python shim in the
		// NATIVE adapter — so the wizard would start the HTTP server, verify it
		// answering on its port, and then speech.Status would health-check the
		// shim instead and report "piper-local still not answering" three lines
		// later. One name, two providers, contradicting each other.
		if !IsNativePiperBinary(p) {
			continue
		}
		return p, nil
	}
	return "", ErrNoPiperBinary
}

// IsNativePiperBinary reports whether path is a real executable rather than an
// interpreter script.
//
// A shebang is the whole test: the standalone piper is a Mach-O/ELF/PE image
// and never begins with "#!", while every console script and shell wrapper
// does. Cheap, and it does not run the file to find out — the question is
// whether this thing IS the native runtime, and executing it to ask would both
// be slower and start something.
//
// LIMITATION, on Windows: pip does not write a shebang script there. It writes
// a real PE launcher named piper.exe, which is byte-indistinguishable from the
// standalone build by any test this cheap. So the shim confusion this prevents
// on Unix is still possible on Windows, where the defence is the lookup order
// instead — Helix's own downloaded binary in ~/.helix/piper is preferred over
// anything on PATH. Stated rather than papered over: a function that claims a
// guarantee it cannot keep on one platform is worse than one that names the
// gap.
func IsNativePiperBinary(path string) bool {
	f, err := os.Open(path) //nolint:gosec // a path already resolved from PATH
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	var head [2]byte
	if n, _ := io.ReadFull(f, head[:]); n < 2 {
		return false // too small to be an executable of any kind
	}
	return head[0] != '#' || head[1] != '!'
}

// piperExecutableName is the file name inside the release archive.
func piperExecutableName() string {
	if runtime.GOOS == "windows" {
		return "piper.exe"
	}
	return "piper"
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

// PiperReleaseVersion is the release the standalone binaries come from.
//
// Pinned, and frozen upstream: after this tag Piper's development moved to a
// Python-first rewrite, so this is the last release that ships self-contained
// executables. That is a real trade-off, not a footnote — it is why the Python
// HTTP path is kept rather than removed.
const PiperReleaseVersion = "2023.11.14-2"

// PiperReleaseAsset names the standalone archive for this host, and reports
// whether Helix will offer to download it.
//
// **macOS is deliberately excluded, and it is not an oversight.** Both macOS
// archives in this release are missing their shared libraries: they ship the
// libonnxruntime .dSYM debug bundle but no .dylib at all, so the extracted
// binary dies immediately with
//
//	dyld: Library not loaded: @rpath/libespeak-ng.1.dylib
//
// Verified by downloading and running it, not inferred. The Linux archives
// carry libespeak-ng.so and libpiper_phonemize.so, and the Windows zip carries
// the four matching DLLs; only macOS is broken. Offering a 19 MB download that
// produces an executable which cannot start is precisely the "walked into
// something that cannot work" failure the sidecar preconditions exist to
// prevent, so on macOS Helix says so instead.
func PiperReleaseAsset() (string, bool) {
	name, _, ok := piperAsset()
	return name, ok
}

// PiperReleaseSHA256 is the pinned digest of this host's archive.
//
// Pinned because threat V8 (sidecar supply chain) says Helix's installers
// "pin versions and publish checksums", and the Ollama installer already
// refuses to run a script whose SHA-256 does not match. A download piped
// straight into tar would have made that claim false the moment it shipped —
// the archive is a 26 MB executable payload fetched over the network and then
// run, which is precisely the thing a checksum is for.
//
// GitHub publishes no digest field for these assets, so each was downloaded and
// hashed directly. Byte counts are recorded alongside as a second, cheaper
// signal that the right file arrived.
func PiperReleaseSHA256() (string, bool) {
	_, sum, ok := piperAsset()
	return sum, ok
}

// piperAsset resolves the archive name and its pinned digest for this host.
func piperAsset() (name, sha256 string, ok bool) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "piper_linux_x86_64.tar.gz",
			"a50cb45f355b7af1f6d758c1b360717877ba0a398cc8cbe6d2a7a3a26e225992", true
	case "linux/arm64":
		return "piper_linux_aarch64.tar.gz",
			"fea0fd2d87c54dbc7078d0f878289f404bd4d6eea6e7444a77835d1537ab88eb", true
	case "linux/arm":
		return "piper_linux_armv7l.tar.gz",
			"c6946fcd57c705ed1d4666ea880f80ba0bbbd14de62ecbdd13460baf3bac8e37", true
	case "windows/amd64":
		return "piper_windows_amd64.zip",
			"f3c58906402b24f3a96d92145f58acba6d86c9b5db896d207f78dc80811efcea", true
	}
	return "", "", false
}

// PiperNativeUnavailableReason explains why this host gets no binary offer.
//
// Empty when one is available. Named separately from the asset lookup because
// "no build for this platform" and "the published build is broken" are
// different facts and only one of them might change with a new release.
func PiperNativeUnavailableReason() string {
	switch runtime.GOOS {
	case "darwin":
		return "Piper's " + PiperReleaseVersion + " macOS build ships without its own " +
			"libraries (no libespeak-ng/libonnxruntime dylib), so the downloaded binary " +
			"cannot start. Helix will not fetch 19 MB to produce something that fails on " +
			"first run. Use the Python server, a piper you built yourself, or a different voice."
	default:
		return "Piper publishes no standalone binary for " +
			runtime.GOOS + "/" + runtime.GOARCH + "."
	}
}

// RequiredGLIBCXX is the C++ ABI version piper's bundled libraries need.
//
// libpiper_phonemize.so imports GLIBCXX_3.4.26 (GCC 9) and the archive does NOT
// bundle libstdc++, so the system one has to supply it. Verified by reading the
// symbol versions out of the aarch64 build, not inferred.
const RequiredGLIBCXX = "GLIBCXX_3.4.26"

// PiperBinaryUsableHere reports whether this host can actually run the
// standalone binary, and why not when it cannot.
//
// The check exists for edge boards. Piper's binary needs only GLIBC_2.17 — no
// board is that old — but it needs a libstdc++ from GCC 9 or newer, and the
// Jetson Nano's JetPack 4.x is Ubuntu 18.04 with GCC 7.5, which tops out at
// GLIBCXX_3.4.25. Raspberry Pi OS Buster has the same problem; Bullseye and
// Bookworm do not.
//
// Checked BEFORE offering the download, because 50 MB that cannot start is the
// "walked into something that cannot work" failure this codebase keeps having
// to remove. Non-Linux hosts skip it: the constraint is a glibc/libstdc++ one.
func PiperBinaryUsableHere() (string, bool) {
	if runtime.GOOS != "linux" {
		return "", true
	}
	for _, path := range libstdcxxCandidates() {
		if hasSymbolVersion(path, RequiredGLIBCXX) {
			return "", true
		}
	}
	return "this system's libstdc++ predates " + RequiredGLIBCXX +
		" (GCC 9). Piper's binary bundles its own espeak and onnxruntime but " +
		"not libstdc++, so it cannot start here — the Jetson Nano's JetPack 4.x " +
		"(Ubuntu 18.04) and Raspberry Pi OS Buster are the common cases. Use the " +
		"Python server, or a newer OS image.", false
}

// libstdcxxCandidates lists where the system C++ runtime usually lives.
func libstdcxxCandidates() []string {
	var out []string
	roots := []string{
		"/usr/lib", "/usr/lib64",
		"/usr/lib/" + runtime.GOARCH + "-linux-gnu",
		"/usr/lib/aarch64-linux-gnu", "/usr/lib/arm-linux-gnueabihf",
		"/usr/lib/x86_64-linux-gnu",
	}
	for _, r := range roots {
		out = append(out, filepath.Join(r, "libstdc++.so.6"))
	}
	return out
}

// hasSymbolVersion reports whether a library exports a given version tag.
//
// Reads the file and looks for the literal string rather than parsing ELF: the
// version tags live in .gnu.version_d as plain text, a substring scan finds
// them, and pulling in an ELF parser to answer one yes/no question would be a
// dependency for nothing.
func hasSymbolVersion(path, want string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte(want))
}

// piperNativeTTS synthesizes by running the piper binary directly.
type piperNativeTTS struct {
	name    string
	display string
	binary  string
	model   string

	// session holds the piper process with its model resident. See its type
	// comment for the measurement that makes this mandatory rather than an
	// optimisation.
	session *piperSession
}

// NewPiperNativeTTS builds the interpreter-free Piper adapter.
func NewPiperNativeTTS(binary, model string) TTSProvider {
	return &piperNativeTTS{
		name:    "piper-local",
		display: "Piper (local binary)",
		binary:  binary,
		model:   model,
		session: &piperSession{binary: binary, model: model},
	}
}

func (p *piperNativeTTS) Name() string         { return p.name }
func (p *piperNativeTTS) DisplayName() string  { return p.display }
func (p *piperNativeTTS) SetAPIKey(string)     {}
func (p *piperNativeTTS) RequiresAPIKey() bool { return false }
func (p *piperNativeTTS) IsLocal() bool        { return true }
func (p *piperNativeTTS) DefaultModel() string { return "piper" }

// Endpoint reports where synthesis happens, for status output.
//
// Not a URL: there is no server and no port, which is most of the point. Status
// screens print this verbatim, so it says what it is.
func (p *piperNativeTTS) Endpoint() string { return p.binary + " (no server)" }

// Synthesize pipes text in and reads a WAV back.
//
// `--output_file -` writes the WAV to stdout. Note the flag spellings are
// genuinely inconsistent upstream — `--output_file` with an underscore,
// `--output-raw` with a hyphen — so neither is guessed.
func (p *piperNativeTTS) Synthesize(ctx context.Context, text string, _ SynthesisOptions) (AudioFormat, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return AudioFormat{}, errors.New("piper-local: nothing to speak")
	}
	if p.model == "" {
		return AudioFormat{}, fmt.Errorf(
			"piper-local: no voice model configured — /blackbox setup fetches one")
	}
	if !isExecutableFile(p.model) && !fileReadable(p.model) {
		return AudioFormat{}, fmt.Errorf(
			"piper-local: voice model not found at %s — /blackbox setup fetches it", p.model)
	}

	data, err := p.session.speak(ctx, text)
	if err != nil {
		return AudioFormat{}, err
	}
	if len(data) == 0 {
		return AudioFormat{}, errors.New("piper-local: produced no audio")
	}
	rate, channels, err := wavHeaderInfo(data)
	if err != nil {
		// Piper writes a WAV header. Anything else means the binary is not the
		// one we think it is, and guessing a rate here is how audio comes out at
		// the wrong pitch.
		return AudioFormat{}, fmt.Errorf("piper-local: output is not WAV audio: %w", err)
	}
	return AudioFormat{Kind: KindWAV, SampleRate: rate, Channels: channels, Bytes: data}, nil
}

// HealthCheck verifies the binary runs and the voice model is present.
//
// Both halves, because they fail for unrelated reasons and only one of them is
// fixable by re-running the installer.
func (p *piperNativeTTS) HealthCheck(ctx context.Context) error {
	if !isExecutableFile(p.binary) {
		if _, err := exec.LookPath(p.binary); err != nil {
			return fmt.Errorf("piper-local: %s is not executable", p.binary)
		}
	}
	if p.model == "" || !fileReadable(p.model) {
		return fmt.Errorf("piper-local: voice model missing (%s) — /blackbox setup fetches it",
			orNone(p.model))
	}
	// Cheapest real proof: synthesize one short word. A --help exit code says
	// the file runs, not that it can produce audio with this voice — the same
	// distinction that had whisper reporting "reachable" while every utterance
	// failed.
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := p.Synthesize(probeCtx, "ok", SynthesisOptions{}); err != nil {
		return err
	}
	return nil
}

func fileReadable(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "not configured"
	}
	return s
}

// piperSession is a piper process held open with its voice model resident.
//
// This is the whole reason the native path is worth having. Piper's cost is
// dominated by LOADING the model, not by speaking:
//
//	5 utterances, one process   →  128 ms each
//	5 utterances, one spawn each →  513 ms each
//
// measured on the development machine. A per-call CLI reloads a 60 MB ONNX
// model for every sentence, which is a 4x regression against the HTTP server it
// was meant to replace — the server is fast precisely because it keeps the model
// warm. Holding one process open gets the server's speed with no port, no HTTP
// hop, and no Python.
//
// The margin is WIDER on weak hardware, not narrower: model load is the part
// that scales with CPU, so a Pi pays more per reload than this machine does.
type piperSession struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	outDir  string
	binary  string
	model   string
	started bool
}

// utteranceTimeout bounds one synthesis.
//
// Generous because the FIRST call also pays the model load, and on a Pi 4 that
// can be seconds. A tighter bound would make the slowest boards look broken.
const utteranceTimeout = 90 * time.Second

// start launches piper with a private output directory.
//
// --output-dir rather than --output_file, because one process must serve many
// utterances: piper reads stdin line by line and writes one WAV per line, which
// is exactly the batching that keeps the model resident.
func (s *piperSession) start() error {
	if s.started && s.alive() {
		return nil
	}
	s.stop()

	dir, err := os.MkdirTemp("", "helix-piper-*")
	if err != nil {
		return fmt.Errorf("piper-local: cannot create output directory: %w", err)
	}

	cmd := exec.Command(s.binary,
		"--model", s.model,
		"--output-dir", dir,
		"--output-dir-naming", "timestamp")
	// The binary loads its espeak-ng data and shared libraries relative to its
	// own location, so it must run from there. Extracting the archive without
	// its siblings, or launching from elsewhere, produces a binary that starts
	// and then cannot phonemize.
	cmd.Dir = filepath.Dir(s.binary)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("piper-local: cannot open stdin: %w", err)
	}
	// Piper logs progress to stderr. Discarded rather than parsed: the log
	// format differs between the C++ binary and the Python module, and framing
	// on it would silently break on whichever one was not tested. The output
	// DIRECTORY is the frame instead, which no log format can change.
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("piper-local: cannot start %s: %w", s.binary, err)
	}

	s.cmd, s.stdin, s.outDir, s.started = cmd, stdin, dir, true
	go func() { _ = cmd.Wait() }() // reap, so a dead piper does not become a zombie
	return nil
}

// alive reports whether the held process is still running.
func (s *piperSession) alive() bool {
	return s.cmd != nil && s.cmd.Process != nil &&
		(s.cmd.ProcessState == nil || !s.cmd.ProcessState.Exited())
}

// stop tears the session down and removes its directory.
func (s *piperSession) stop() {
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if s.outDir != "" {
		_ = os.RemoveAll(s.outDir)
	}
	s.cmd, s.stdin, s.outDir, s.started = nil, nil, "", false
}

// speak sends one line and returns the WAV piper wrote for it.
//
// Retries once through a fresh process: a sidecar that has been up for hours
// can be killed by an OOM reaper or a laptop suspend, and re-synthesizing is
// cheap next to telling the user their voice stopped working.
func (s *piperSession) speak(ctx context.Context, text string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.speakOnce(ctx, text)
	if err == nil {
		return data, nil
	}
	s.stop()
	return s.speakOnce(ctx, text)
}

func (s *piperSession) speakOnce(ctx context.Context, text string) ([]byte, error) {
	if err := s.start(); err != nil {
		return nil, err
	}

	// Note what is already in the directory: the frame is "a file that was not
	// here before", so a leftover from an earlier utterance must not be
	// mistaken for this one's answer.
	before, err := wavSet(s.outDir)
	if err != nil {
		return nil, err
	}

	// One utterance per line. Embedded newlines would be read as several.
	line := strings.ReplaceAll(strings.TrimSpace(text), "\n", " ") + "\n"
	if _, err := io.WriteString(s.stdin, line); err != nil {
		return nil, fmt.Errorf("piper-local: cannot send text: %w", err)
	}

	deadline, cancel := context.WithTimeout(ctx, utteranceTimeout)
	defer cancel()

	for {
		select {
		case <-deadline.Done():
			return nil, fmt.Errorf("piper-local: no audio after %s", utteranceTimeout)
		case <-time.After(10 * time.Millisecond):
		}
		if !s.alive() {
			return nil, errors.New("piper-local: the piper process exited")
		}
		path, ok, err := newWAV(s.outDir, before)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		data, err := readStableFile(path)
		if err != nil {
			return nil, err
		}
		_ = os.Remove(path) // one utterance, one file: never let the dir grow
		return data, nil
	}
}

// wavSet lists the WAVs currently in dir.
func wavSet(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("piper-local: cannot read output directory: %w", err)
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".wav") {
			out[e.Name()] = true
		}
	}
	return out, nil
}

// newWAV returns a WAV present now that was not present before.
func newWAV(dir string, before map[string]bool) (string, bool, error) {
	now, err := wavSet(dir)
	if err != nil {
		return "", false, err
	}
	for name := range now {
		if !before[name] {
			return filepath.Join(dir, name), true, nil
		}
	}
	return "", false, nil
}

// readStableFile reads a file once its size has stopped changing.
//
// The file APPEARS when piper creates it, not when it finishes writing it, so
// reading on first sight yields a truncated WAV — a clip that decodes to a
// fraction of the sentence, or fails its header check. Waiting for two equal
// sizes is the cheap way to know the writer is done.
func readStableFile(path string) ([]byte, error) {
	var last int64 = -1
	for i := 0; i < 600; i++ {
		info, err := os.Stat(path)
		if err == nil {
			if size := info.Size(); size > 0 && size == last {
				return os.ReadFile(path)
			} else {
				last = size
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, errors.New("piper-local: output file never settled")
}
