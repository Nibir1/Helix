// internal/providers/llamacpp/llamacpp.go
//
// Purpose: llama.cpp local provider, spoken to over `llama-server`'s
// OpenAI-compatible HTTP API (BlackBox P11.4).
//
// Architecture: this is the ADR-002 sidecar pattern applied to the LLM itself.
// `llama-server` is a user-managed external process — Helix never links a GGUF
// runtime, never downloads weights, and never installs it (same contract as
// whisper.cpp/Piper/Kokoro, P7.7). The core stays CGO-free.
//
// Why a second local runtime alongside Ollama: on boards where Ollama is
// unsupported — notably the first-gen Jetson Nano, whose JetPack 4.6 is frozen
// at CUDA 10.2 / Maxwell 5.3 (see docs/edge_deployment.md §5) — a hand-built
// llama.cpp is the only local-LLM path. Registering it as a first-class
// provider makes it a valid target for the Phase 11 offline failover chain.
package llamacpp

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"helix/internal/providers"
	openaicompatible "helix/internal/providers/openai_compatible"
)

const (
	// Name is the registry key for this provider.
	Name = "llamacpp"

	// DisplayName is the human-readable label shown by /provider and /doctor.
	DisplayName = "llama.cpp (llama-server)"

	// DefaultBaseURL is llama-server's stock OpenAI-compatible endpoint, i.e.
	// what `llama-server -m model.gguf --port 8080` serves.
	DefaultBaseURL = "http://127.0.0.1:8080/v1"

	// DefaultModel is a stable UI label, not a routing key: llama-server serves
	// whichever GGUF it was launched with and ignores the requested model name.
	// ListModels reports the real loaded model.
	DefaultModel = "local-gguf"

	// URLEnv overrides the endpoint without editing config.json.
	URLEnv = "HELIX_LLAMACPP_URL"
)

// BaseURL resolves the endpoint by precedence: explicit config, then the
// URLEnv override, then llama-server's default port.
//
// Args:
//   - configured: the value from config.json (may be empty).
//
// Returns: a normalized base URL ending in /v1.
// Complexity: O(len(url)).
func BaseURL(configured string) string {
	if s := strings.TrimSpace(configured); s != "" {
		return normalize(s)
	}
	if s := strings.TrimSpace(os.Getenv(URLEnv)); s != "" {
		return normalize(s)
	}
	return DefaultBaseURL
}

// normalize appends the OpenAI-compatible /v1 prefix when the user supplied a
// bare host:port. That omission is the single most common llama-server
// misconfiguration, and it fails as an opaque 404 rather than a clear error —
// so absorb it here instead of making the user diagnose it.
func normalize(u string) string {
	u = strings.TrimSuffix(strings.TrimSpace(u), "/")
	if strings.HasSuffix(u, "/v1") {
		return u
	}
	return u + "/v1"
}

// New builds the llama.cpp provider.
//
// Local: true is load-bearing, not cosmetic — it exempts the provider from the
// API-key requirement and marks it as an offline-capable brain for the Phase 11
// failover chain and the planner's longer local-model timeouts.
//
// Args:
//   - baseURL: configured endpoint ("" → env override → default).
//   - client: the shared retrying HTTP client.
//
// Returns: a registry-ready provider.
// Complexity: O(1).
func New(baseURL string, client *providers.HTTPClient) *openaicompatible.Provider {
	return openaicompatible.New(openaicompatible.Config{
		Name:         Name,
		DisplayName:  DisplayName,
		BaseURL:      BaseURL(baseURL),
		DefaultModel: DefaultModel,
		Local:        true,
	}, client)
}

// Diagnosis classifies why an endpoint health check failed.
type Diagnosis int

const (
	// DiagnosisUnreachable: nothing is listening on the port.
	DiagnosisUnreachable Diagnosis = iota

	// DiagnosisForeignServer: something answered, but it is not llama-server.
	DiagnosisForeignServer
)

// Diagnose classifies a failed health check and returns operator guidance.
//
// The distinction matters more than it looks. Port 8080 is llama-server's
// default AND one of the most commonly occupied ports on a developer machine —
// so "not reachable" is actively misleading when some unrelated dev service is
// sitting there answering HTTP. The user reads "not reachable", assumes the
// port is free, starts llama-server on it, and gets a bind conflict instead of
// an explanation.
//
// An HTTP status in the error means a server replied; anything else (dial
// refused, DNS, timeout) means nobody is home.
//
// Args:
//   - err: the error returned by HealthCheck.
//   - url: the endpoint that was probed.
//
// Returns: the classification and a multi-line hint.
// Complexity: O(len(err)).
func Diagnose(err error, url string) (Diagnosis, string) {
	if err != nil && strings.Contains(err.Error(), "HTTP ") {
		return DiagnosisForeignServer, "Something IS listening on " + url +
			", but it did not answer as llama-server.\n" +
			"  That port is probably in use by another service. Either stop it, or\n" +
			"  run llama-server on a free port and point Helix at it:\n" +
			"    llama-server -m model.gguf --port 8081\n" +
			"    export HELIX_LLAMACPP_URL=http://127.0.0.1:8081"
	}
	// host:port, not the full URL: nothing "listens on" a path, and printing
	// /v1 here invited the reading that the path was the problem.
	//
	// The next step depends on whether the binary exists, and this package is
	// where that can be checked — so it is decided here rather than by each
	// caller. Telling someone without llama.cpp to run `llama-server` sends
	// them to a "command not found" first.
	msg := "Nothing is listening on " + hostPort(url) + ".\n"
	if binary, installed := ServerInstalled(); installed {
		msg += "  llama-server is installed (" + binary + "); it is just not running.\n" +
			"  Start it:\n" +
			"    llama-server -m /path/to/model.gguf --port " + portOf(url)
		return DiagnosisUnreachable, msg
	}
	msg += "  llama-server is NOT INSTALLED on this machine."
	for _, line := range InstallHint() {
		msg += "\n  " + line
	}
	return DiagnosisUnreachable, msg
}

// -------------------------------------------------------
// LOCAL INSTALLATION
// -------------------------------------------------------

// serverBinaries are the names llama.cpp's server ships under. `llama-server`
// is current; `server` was the name before the 2024 rename, and some distro
// packages still install it that way.
var serverBinaries = []string{"llama-server", "server"}

// ServerInstalled reports whether llama.cpp's server binary is on PATH, and
// where.
//
// This exists because "not reachable" and "not installed" are different
// problems with different fixes, and the setup flow was conflating them: it
// printed `llama-server -m model.gguf --port 8080` as the remedy for an
// unreachable endpoint, which on a machine without llama.cpp is a command that
// fails with "command not found". Advice the user cannot act on is worse than
// none, because they spend time on it first.
func ServerInstalled() (string, bool) {
	for _, name := range serverBinaries {
		if path, err := exec.LookPath(name); err == nil {
			return path, true
		}
	}
	// Homebrew's llama.cpp keg and the common manual build location. Checked
	// explicitly because a GUI-launched Helix inherits a minimal PATH.
	for _, p := range []string{
		"/opt/homebrew/bin/llama-server",
		"/usr/local/bin/llama-server",
	} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, true
		}
	}
	return "", false
}

// InstallHint returns the platform-appropriate way to obtain llama.cpp.
//
// Helix never installs it (ADR-002: llama.cpp is a user-managed sidecar, and
// building it involves GPU-backend choices Helix has no business making), so
// the useful contribution is the exact command for this machine.
func InstallHint() []string {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("brew"); err == nil {
			return []string{
				"Install it with Homebrew:",
				"  brew install llama.cpp",
			}
		}
		return []string{
			"Install Homebrew (https://brew.sh) then:",
			"  brew install llama.cpp",
			"Or build from source: https://github.com/ggml-org/llama.cpp",
		}
	case "linux":
		return []string{
			"Build it (no official package on most distros):",
			"  git clone https://github.com/ggml-org/llama.cpp && cd llama.cpp",
			"  cmake -B build && cmake --build build --config Release -j",
			"  build/bin/llama-server --help",
		}
	default:
		return []string{
			"Get a release build: https://github.com/ggml-org/llama.cpp/releases",
		}
	}
}

// hostPort reduces an endpoint to host:port for socket-level messages.
func hostPort(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

// portOf extracts the port from an endpoint so a suggested launch command
// targets the port Helix will actually probe.
func portOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Port() == "" {
		return "8080"
	}
	return u.Port()
}

// -------------------------------------------------------
// MODELS ON DISK
// -------------------------------------------------------

// InstallCommand returns a single runnable command that installs llama.cpp, and
// whether one exists on this platform.
//
// Deliberately narrow. ADR-002 keeps llama.cpp user-managed because BUILDING it
// involves GPU-backend choices (CUDA, Metal, Vulkan, CPU) that Helix has no
// business making on someone's behalf. A Homebrew bottle makes none of those
// choices — it is a signed, prebuilt binary and one command — so offering to run
// it respects the reasoning behind that decision rather than just its letter.
// Everywhere the install is a build, this returns false and the caller prints
// instructions instead.
func InstallCommand() (string, bool) {
	if _, err := exec.LookPath("brew"); err != nil {
		return "", false
	}
	// Homebrew exists on Linux too, and its llama.cpp bottle is equally
	// unambiguous there.
	return "brew install llama.cpp", true
}

// CacheDirs returns the directories llama.cpp downloads models into, most
// specific first.
//
// There are several because the location has moved: `-hf` downloads once went to
// LLAMA_CACHE (or ~/.cache/llama.cpp) and now land in the shared Hugging Face
// hub cache. Checking all of them means a model pulled by any recent version is
// found, rather than Helix reporting "no models" next to several gigabytes of
// weights.
func CacheDirs() []string {
	var dirs []string
	add := func(p string) {
		if p = strings.TrimSpace(p); p != "" {
			dirs = append(dirs, p)
		}
	}

	add(os.Getenv("LLAMA_CACHE"))
	add(os.Getenv("HF_HUB_CACHE"))
	if hf := strings.TrimSpace(os.Getenv("HF_HOME")); hf != "" {
		add(filepath.Join(hf, "hub"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".cache", "llama.cpp"))
		add(filepath.Join(home, ".cache", "huggingface", "hub"))
	}
	return dirs
}

// CachedModel is a GGUF llama.cpp has already downloaded.
type CachedModel struct {
	// Name is the file's basename, which for an -hf download carries the repo
	// and quantization.
	Name string

	// Path is the absolute file path, for `llama-server -m`.
	Path string

	SizeBytes int64
}

// SizeGB renders the size for a listing.
func (m CachedModel) SizeGB() float64 { return float64(m.SizeBytes) / (1 << 30) }

// maxCacheScanDepth bounds the walk. The HF hub cache nests
// models--org--name/snapshots/<hash>/file.gguf, so a handful of levels is
// enough; an unbounded walk of a home directory is not something an interactive
// setup step should ever do.
const maxCacheScanDepth = 6

// CachedModels lists the GGUF files llama.cpp has on disk, largest first.
//
// Size-descending because the largest model a machine has is usually the most
// capable one it can run, which is the sensible default to suggest.
func CachedModels() []CachedModel {
	seen := map[string]bool{}
	var out []CachedModel

	for _, dir := range CacheDirs() {
		root, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			continue
		}
		rootDepth := strings.Count(root, string(os.PathSeparator))

		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // an unreadable subtree must not hide the rest
			}
			if d.IsDir() {
				if strings.Count(path, string(os.PathSeparator))-rootDepth > maxCacheScanDepth {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.EqualFold(filepath.Ext(d.Name()), ".gguf") {
				return nil
			}
			// Resolve symlinks: the HF cache stores blobs once and links
			// snapshots at them, so the same weights appear under two paths.
			real := path
			if resolved, rerr := filepath.EvalSymlinks(path); rerr == nil {
				real = resolved
			}
			if seen[real] {
				return nil
			}
			seen[real] = true

			info, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			out = append(out, CachedModel{Name: d.Name(), Path: path, SizeBytes: info.Size()})
			return nil
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].SizeBytes != out[j].SizeBytes {
			return out[i].SizeBytes > out[j].SizeBytes
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// PullCommand renders the command that downloads a model from Hugging Face and
// serves it.
//
// This is the answer to "can llama.cpp fetch models like Ollama?" — it can, with
// -hf, and the download is cached for later launches. No second runtime is
// needed to get weights onto the machine.
func PullCommand(hfRepo, port string) string {
	if port == "" {
		port = "8080"
	}
	return "llama-server -hf " + hfRepo + " --port " + port
}

// ServeCommand renders the command that serves a model already on disk.
func ServeCommand(path, port string) string {
	if port == "" {
		port = "8080"
	}
	return "llama-server -m " + path + " --port " + port
}
