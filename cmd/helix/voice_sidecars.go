// cmd/helix/voice_sidecars.go
// Purpose: get a local speech sidecar actually RUNNING, rather than explaining
// why it is not.
//
// This closes the gap the setup flow kept stopping at. Helix already knew the
// binary was missing, which port was free, and which model to load — everything
// except the last step, which it handed back as a command to copy. For
// llama.cpp that step is already automated; there was no reason speech should
// be different, and the asymmetry is why voice setup could be run five times and
// still not speak.
//
// The consent model matches llama.cpp's: nothing installs or starts implicitly,
// each step is a separate yes, and a started process is announced along with how
// to stop it.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"helix/internal/commands"
	"helix/internal/edge"
	"helix/internal/providers/llamacpp"
	"helix/internal/shell"
	"helix/internal/sidecar"
	"helix/internal/speech"
	"helix/internal/utils"

	"github.com/fatih/color"
)

// voiceSidecar describes how to obtain, feed, and run one local speech server.
type voiceSidecar struct {
	// Binaries are the executable names to look for, in preference order.
	Binaries []string

	// InstallCmd is a single unambiguous install command, or "".
	//
	// Same rule as llama.cpp: Helix offers to run an install only when there is
	// one obvious command that makes no choices on the user's behalf. Anything
	// requiring a build with backend flags is printed, not executed.
	InstallCmd func() (string, bool)

	// ModelHint describes what the server needs besides the binary, and how to
	// get it. Empty when the binary is self-sufficient.
	ModelHint func() (cmd string, why string, ok bool)

	// Args renders the launch arguments for a port.
	Args func(port int) []string

	// Unmet reports a precondition Helix will NOT resolve for the user, and
	// why. Distinct from a missing binary, which Helix offers to install: this
	// is for a dependency the project has decided not to require at all.
	//
	// Docker is the only one today. Helix must stay installable and usable with
	// no container runtime — QA hit "Cannot connect to the Docker daemon" as
	// the last line of voice setup, having been walked all the way to a pull it
	// could never complete. A capability that needs docker is fine; being
	// marched into docker is not.
	Unmet func() (reason string, unmet bool)
}

// dockerAvailable reports whether a container runtime is usable — the binary
// AND a daemon that answers. `docker` on PATH with a dead daemon is the exact
// state that produced the QA failure, so presence alone is not enough.
func dockerAvailable() bool {
	bin, err := exec.LookPath("docker")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, bin, "info", "--format", "{{.ServerVersion}}").Run() == nil
}

// voiceSidecars is the table of local speech servers Helix can set up.
func voiceSidecars() map[string]voiceSidecar {
	return map[string]voiceSidecar{
		"whisper-local": {
			Binaries: []string{"whisper-server"},
			InstallCmd: func() (string, bool) {
				if _, err := exec.LookPath("brew"); err != nil {
					return "", false
				}
				// The formula installs whisper-server among several binaries and
				// prints that it downloads no models — which is why ModelHint
				// below is not optional.
				return "brew install whisper-cpp", true
			},
			ModelHint: func() (string, string, bool) {
				if _, ok := installedWhisperModel(); ok {
					return "", "", false // one is already on disk
				}
				m := chosenWhisperModel()
				return fmt.Sprintf("curl -fL --create-dirs -o %s %s",
						helixPath("whisper-models/"+m.File), whisperModelURLFor(m.File)),
					fmt.Sprintf("whisper needs a model; %s is ~%d MB — %s",
						m.Name, m.SizeMB, m.Accuracy),
					true
			},
			Args: func(port int) []string {
				return []string{
					"-m", whisperModelPath(),
					"--port", fmt.Sprint(port),
				}
			},
		},
		// Sesame CSM-1B via csm.rs — a Rust/candle server, no Python, no Docker.
		//
		// Two things make this different from the other sidecars and both are
		// visible in the fields below. It is BUILT rather than installed, because
		// the backend is a compile-time choice (cuda / metal / accelerate / mkl)
		// and picking one for the user would silently hand a 3080 owner a CPU
		// build. And its weights are GATED on Hugging Face: the user must accept
		// Sesame's terms and hold a token, which Helix will not and cannot do on
		// their behalf.
		"csm-local": {
			Binaries: []string{"csm-server"},
			InstallCmd: func() (string, bool) {
				// Deliberately no auto-install. The llama.cpp precedent applies
				// exactly: "Helix offers to run an install only when there is one
				// obvious command that makes no choices on the user's behalf."
				// A CSM build chooses a compute backend, so it is printed.
				return "", false
			},
			ModelHint: func() (string, string, bool) {
				return "huggingface-cli login   # then accept the terms at https://huggingface.co/sesame/csm-1b",
					"CSM's weights are gated: accept Sesame's terms once and log in, " +
						"then csm.rs downloads them on first run (~2 GB, or ~700 MB for the q4_k GGUF)",
					true
			},
			Args: func(port int) []string {
				return []string{
					"--model-id", "sesame/csm-1b",
					"--port", fmt.Sprint(port),
				}
			},
		},
		"kokoro-local": {
			// Docker-hosted, so the "binary" is docker and the image is the
			// argument. Helix does not install Docker, and does not ask the
			// user to either — see Unmet.
			Binaries:   []string{"docker"},
			InstallCmd: func() (string, bool) { return "", false },
			Unmet: func() (string, bool) {
				if dockerAvailable() {
					return "", false
				}
				return "kokoro runs in a container and no Docker daemon is " +
					"answering. Helix will not install one — piper-local is the " +
					"docker-free local voice and needs only Python", true
			},
			ModelHint: func() (string, string, bool) {
				if dockerImagePresent(kokoroImage) {
					return "", "", false
				}
				// Pull as its OWN step, before the readiness wait.
				//
				// `docker run` on a missing image pulls it first, which for this
				// one is a multi-gigabyte download — so the container had not
				// even started when the readiness budget expired, and Helix
				// reported "did not come up" for a sidecar that came up fine a
				// few minutes later. Separating the pull means the wait measures
				// startup, and the download gets docker's own progress display.
				return "docker pull " + kokoroImage,
					"the kokoro image is ~2 GB; pulling it separately keeps the startup check honest",
					true
			},
			Args: func(port int) []string {
				return []string{
					"run", "--rm", "-p", fmt.Sprintf("%d:8880", port),
					"ghcr.io/remsky/kokoro-fastapi-cpu",
				}
			},
		},
		"piper-local": {
			// piper installs as a Python module, so the "binary" is the
			// interpreter and the module is the argument.
			Binaries: []string{"python3", "python"},
			InstallCmd: func() (string, bool) {
				// Flask is NOT a dependency of piper-tts, but piper.http_server
				// imports it — installing only piper-tts yields a server that
				// dies on startup with ModuleNotFoundError.
				return "python3 -m pip install --user piper-tts flask", true
			},
			ModelHint: func() (string, string, bool) {
				if _, err := os.Stat(piperVoicePath()); err == nil {
					return "", "", false // already fetched
				}
				// curl, not `python3 -m piper.download_voices`.
				//
				// A python.org Python does not use the system trust store, so
				// its downloader fails with CERTIFICATE_VERIFY_FAILED on a stock
				// macOS install until Install Certificates.command is run. curl
				// uses the system store and just works.
				// BOTH files: piper loads <model>.onnx.json alongside the weights
				// for phoneme and speaker configuration, and fails without it.
				return fmt.Sprintf(
						"curl -fL --create-dirs -o %s %s/en_US-lessac-medium.onnx -o %s.json %s/en_US-lessac-medium.onnx.json",
						piperVoicePath(), piperVoiceBaseURL, piperVoicePath(), piperVoiceBaseURL),
					"piper's voice IS a model file (~60 MB); this fetches it and its config into ~/.helix",
					true
			},
			Args: func(port int) []string {
				return []string{
					"-m", "piper.http_server",
					"--model", piperVoicePath(),
					"--port", fmt.Sprint(port),
				}
			},
		},
	}
}

// helixPath returns a path inside ~/.helix.
func helixPath(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return name
	}
	return filepath.Join(home, ".helix", name)
}

// offerSidecarSetup walks a selected local speech provider from "not installed"
// to "answering", one confirmation at a time.
//
// Returns true when the sidecar ended up serving.
func offerSidecarSetup(kind, provider string) bool {
	spec, known := voiceSidecars()[provider]
	if !known {
		return false
	}

	// Preconditions first: refusing early beats walking someone through an
	// install, a pull and a start that cannot succeed.
	if spec.Unmet != nil {
		if reason, unmet := spec.Unmet(); unmet {
			fmt.Println()
			fmt.Println(shell.PanelLine(shell.Badge(shell.StateWarn, provider+" is unavailable here")))
			for _, l := range shell.PanelWrap(reason, shell.Muted) {
				fmt.Println(l)
			}
			return false
		}
	}

	binary, installed := findFirstBinary(spec.Binaries)
	if !installed {
		binary, installed = offerSidecarInstall(provider, spec)
		if !installed {
			return false
		}
	}

	if cmd, why, ok := spec.ModelHint(); ok {
		fmt.Println()
		fmt.Println(shell.PanelLine(shell.Fg(shell.HexText, provider) +
			shell.Muted(" needs one more thing before it can start")))
		for _, l := range shell.PanelWrap(why, shell.Muted) {
			fmt.Println(l)
		}
		fmt.Println(shell.Hint(cmd))
		if !commands.AskForConfirmation("Fetch it now?") {
			color.Yellow("Skipped — %s cannot start without one.", provider)
			return false
		}
		if !runVisibleCommand(cmd, 30*time.Minute) {
			return false
		}
	}

	if !commands.AskForConfirmation(fmt.Sprintf("Start %s now?", provider)) {
		return false
	}
	return startVoiceSidecar(kind, provider, binary, spec)
}

// offerSidecarInstall offers the one unambiguous install command, if there is
// one.
func offerSidecarInstall(provider string, spec voiceSidecar) (string, bool) {
	cmd, ok := spec.InstallCmd()
	if !ok {
		color.Yellow("%s is not installed, and there is no single install command", provider)
		color.Yellow("for this platform — see docs/local_runtimes.md.")
		return "", false
	}

	fmt.Println()
	color.Yellow("%s is not installed.", provider)
	color.Cyan("  Helix can install it:  %s", cmd)
	if !commands.AskForConfirmation("Run that now?") {
		return "", false
	}
	if !runVisibleCommand(cmd, 30*time.Minute) {
		return "", false
	}

	// Trust the lookup, not the exit code: a package manager can succeed while
	// putting the binary somewhere this process cannot see yet.
	binary, found := findFirstBinary(spec.Binaries)
	if !found {
		color.Yellow("Install finished but %s is still not on PATH.", provider)
		color.Yellow("Open a new shell and re-run /blackbox setup.")
		return "", false
	}
	color.Green("Installed: %s", binary)
	return binary, true
}

// startVoiceSidecar launches the server and waits for it to answer.
func startVoiceSidecar(kind, provider, binary string, spec voiceSidecar) bool {
	endpoint := sidecarEndpoint(kind, provider, voiceSidecarDefault(provider))
	port := portOfEndpoint(endpoint)
	if port <= 0 {
		port = 8080
	}
	reserved := reservedSidecarPorts(provider)
	if assigned, ok := edge.FreePortAvoiding(provider, port, reserved); !ok || assigned != port {
		color.Yellow("Port %d is unavailable or claimed by another service; using %d.", port, assigned)
		port = assigned
		applySidecarEndpoint(kind, provider, edge.ReplacePort(endpoint, port))
	}

	logPath, err := sidecar.LogPathFor(provider)
	if err != nil {
		color.Red("Cannot prepare a log file: %v", err)
		return false
	}

	proc, err := sidecar.Start(sidecar.Spec{
		Name: provider, Binary: binary, Args: spec.Args(port), LogPath: logPath,
	})
	if err != nil {
		color.Red("Could not start %s: %v", provider, err)
		return false
	}
	color.Cyan("Started %s (pid %d) on port %d…", provider, proc.PID, port)

	// Containers cold-start slower than a native binary even once pulled.
	budget := 90 * time.Second
	if provider == "kokoro-local" {
		budget = 180 * time.Second
	}
	ready := runCancellableProgressWithTimeout(
		"STARTING "+strings.ToUpper(provider),
		budget,
		func(ctx context.Context, progress func(string, int64, int64)) error {
			progress("STARTING "+strings.ToUpper(provider), 0, 0)
			return proc.WaitReady(ctx, func(probeCtx context.Context) error {
				return probeSpeechProvider(kind, provider, probeCtx)
			})
		},
	)
	if ready != nil {
		color.Red("%s did not come up: %v", provider, ready)
		for _, line := range sidecar.LogTail(proc.LogPath, 8) {
			color.Yellow("  %s", truncStr(line, 160))
		}
		return false
	}

	color.Green("%s is answering on port %d.", provider, port)
	// Detached on purpose, so it must be announced: the user now owns it.
	color.Yellow("It keeps running after Helix exits. Stop it with:  %s", proc.StopHint())
	color.Cyan("Log: %s", proc.LogPath)
	return true
}

// voiceSidecarDefault returns a sidecar's stock endpoint.
func voiceSidecarDefault(provider string) string {
	if spec, ok := sidecarSpecs()[provider]; ok {
		return spec.Default
	}
	return ""
}

// applySidecarEndpoint stores a reassigned endpoint and rebuilds the engine.
//
// Keyed by PROVIDER, not by kind. Writing it to BaseURL — a field that belongs
// to whichever provider is primary — is why whisper-local, chosen as a fallback
// and moved to a free port, was still probed on its stale default and reported
// as "did not come up" while running fine. It also silently pointed the primary
// (a cloud STT) at a localhost address.
func applySidecarEndpoint(kind, provider, endpoint string) {
	switch kind {
	case "stt":
		if cfg.Speech.STT.Endpoints == nil {
			cfg.Speech.STT.Endpoints = map[string]string{}
		}
		cfg.Speech.STT.Endpoints[provider] = endpoint
		if cfg.Speech.STT.Provider == provider {
			cfg.Speech.STT.BaseURL = endpoint
		}
	case "tts":
		if cfg.Speech.TTS.Endpoints == nil {
			cfg.Speech.TTS.Endpoints = map[string]string{}
		}
		cfg.Speech.TTS.Endpoints[provider] = endpoint
		if cfg.Speech.TTS.Provider == provider {
			cfg.Speech.TTS.BaseURL = endpoint
		}
	}
	_ = cfg.SavePreferences()
	_ = speech.Init(speechConfigFrom(cfg.Speech))
}

// probeSpeechProvider runs one provider's health check.
func probeSpeechProvider(kind, provider string, ctx context.Context) error {
	reg := speech.Default()
	if reg == nil {
		return fmt.Errorf("speech engine not initialized")
	}
	if kind == "stt" {
		p, ok := reg.STTProvider(provider)
		if !ok {
			return fmt.Errorf("%s not registered", provider)
		}
		return p.HealthCheck(ctx)
	}
	p, ok := reg.TTSProvider(provider)
	if !ok {
		return fmt.Errorf("%s not registered", provider)
	}
	return p.HealthCheck(ctx)
}

// findFirstBinary returns the first of the names present on PATH.
func findFirstBinary(names []string) (string, bool) {
	for _, n := range names {
		if path, err := exec.LookPath(n); err == nil {
			return path, true
		}
	}
	return "", false
}

// runVisibleCommand runs an install or download with the child owning the
// terminal.
//
// No spinner: package managers and downloaders draw their own progress, and
// animating over it produces interleaved garbage — two writers, one cursor.
func runVisibleCommand(cmdLine string, timeout time.Duration) bool {
	fields := strings.Fields(cmdLine)
	if len(fields) == 0 {
		return false
	}
	fmt.Println()
	color.Cyan("$ %s", cmdLine)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	unreg := utils.RegisterOperation(cancel)
	defer unreg()

	c := exec.CommandContext(ctx, fields[0], fields[1:]...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		fmt.Println()
		if ctx.Err() != nil {
			color.Yellow("Cancelled.")
		} else {
			color.Red("Failed: %v", err)
		}
		return false
	}
	fmt.Println()
	return true
}

// piperVoiceBaseURL is where the default voice model lives.
const piperVoiceBaseURL = "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/medium"

// piperVoicePath is where Helix keeps the default piper voice.
func piperVoicePath() string {
	return helixPath("piper-voices/en_US-lessac-medium.onnx")
}

// whisperModel is one downloadable speech-recognition model.
type whisperModel struct {
	Name     string
	File     string
	SizeMB   int
	Accuracy string
}

// whisperModels are the English models Helix offers, smallest first.
//
// The size difference is the accuracy difference, and it is not subtle. Measured
// on this project's own vocabulary, base.en transcribed "Helix voice loop is
// online" as "He'll expose Looper's online" — an unusable result for a shell
// whose own name it cannot hear. small.en gets it right, and on Apple Silicon
// still runs at roughly 9x real time, so the cost is disk rather than latency.
//
// That is why small.en is the default rather than the smallest option: a voice
// interface that mishears the command vocabulary is not a working voice
// interface, and 300 MB is a poor reason to ship one.
func whisperModels() []whisperModel {
	return []whisperModel{
		{Name: "base.en", File: "ggml-base.en.bin", SizeMB: 141,
			Accuracy: "fastest; struggles with proper nouns and technical terms"},
		{Name: "small.en", File: "ggml-small.en.bin", SizeMB: 465,
			Accuracy: "recommended — accurate on command vocabulary, ~9x real time"},
		{Name: "medium.en", File: "ggml-medium.en.bin", SizeMB: 1530,
			Accuracy: "highest accuracy; noticeably slower on CPU-only machines"},
	}
}

// defaultWhisperModel is the one Helix fetches unless told otherwise.
const defaultWhisperModel = "small.en"

// whisperModelURLFor builds the download URL for a model file.
func whisperModelURLFor(file string) string {
	return "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/" + file
}

// installedWhisperModel returns the best model already on disk, preferring the
// most accurate, so a user who fetched a larger one is not silently downgraded.
func installedWhisperModel() (whisperModel, bool) {
	models := whisperModels()
	for i := len(models) - 1; i >= 0; i-- {
		if _, err := os.Stat(helixPath("whisper-models/" + models[i].File)); err == nil {
			return models[i], true
		}
	}
	return whisperModel{}, false
}

// chosenWhisperModel returns the model to use: whatever is installed, else the
// default.
func chosenWhisperModel() whisperModel {
	if m, ok := installedWhisperModel(); ok {
		return m
	}
	for _, m := range whisperModels() {
		if m.Name == defaultWhisperModel {
			return m
		}
	}
	return whisperModels()[0]
}

// whisperModelPath is where Helix keeps the selected model.
func whisperModelPath() string {
	return helixPath("whisper-models/" + chosenWhisperModel().File)
}

// reservedSidecarPorts returns ports other configured services already claim.
//
// Needed because "free right now" is not "safe to use": whisper.cpp and
// llama.cpp both default to 8080, so on a machine where neither is running yet
// both are free, and assigning 8080 to whisper creates a collision that only
// surfaces later when the brain starts. Reserving what is claimed means Helix
// never manufactures that conflict.
func reservedSidecarPorts(except string) []int {
	var out []int
	add := func(endpoint string) {
		if port := portOfEndpoint(endpoint); port > 0 {
			out = append(out, port)
		}
	}

	// The LLM runtime, whether or not it is currently selected — a fallback
	// target still needs its port when the cloud goes away.
	add(llamacpp.BaseURL(cfg.LLM.LlamaCppURL))

	for name, spec := range sidecarSpecs() {
		if name == except {
			continue
		}
		add(sidecarEndpoint(sidecarKind(name), name, spec.Default))
	}
	return out
}

// sidecarKind maps a provider to the half of the speech chain it serves.
func sidecarKind(name string) string {
	if name == "whisper-local" {
		return "stt"
	}
	return "tts"
}

// kokoroImage is the container Helix runs for local TTS.
const kokoroImage = "ghcr.io/remsky/kokoro-fastapi-cpu"

// dockerImagePresent reports whether an image is already pulled, so the pull
// step is offered only when it would actually do something.
func dockerImagePresent(image string) bool {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, docker, "image", "inspect", image).Output()
	return err == nil && len(out) > 0
}
