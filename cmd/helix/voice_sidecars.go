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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"helix/internal/deps"
	"helix/internal/edge"
	"helix/internal/providers/llamacpp"
	"helix/internal/shell"
	"helix/internal/sidecar"
	"helix/internal/speech"
	"helix/internal/utils"
)

// voiceSidecar describes how to obtain, feed, and run one local speech server.
type voiceSidecar struct {
	// Binaries are the executable names to look for, in preference order.
	Binaries []string

	// InstallCmd is a single unambiguous install command, or "".
	//
	// Same rule as llama.cpp: Helix runs an install only when there is one
	// obvious command that makes no choices on the user's behalf. Anything
	// requiring a build with backend flags is printed, not executed.
	InstallCmd func() (string, bool)

	// GoInstall installs this sidecar in Go, for the ones whose install is not
	// a single command.
	//
	// runVisibleCommand execs a string with no shell, so anything that is a
	// pipeline, a checksum, or a build with an environment cannot be expressed
	// as InstallCmd. Piper (download, verify, extract) and CSM (detect backend,
	// fetch, compile) both live here. Declared on the spec rather than
	// special-cased by name in offerSidecarInstall, so the table stays the one
	// place that says how a sidecar is installed.
	GoInstall func() (string, bool)

	// Prereqs names catalogue entries (internal/deps) this sidecar needs on the
	// host before InstallCmd or GoInstall can succeed.
	//
	// They are installed on demand, not at first run. The distinction matters:
	// piper-local's InstallCmd declines on a host with no Python, and until
	// this existed that decline was reported as "there is no single install
	// command for this platform" — a true statement about a situation Helix
	// could have fixed in one step.
	Prereqs []string

	// InstallBlocker explains why this sidecar cannot be installed
	// automatically, when that is a deliberate refusal rather than a gap.
	//
	// Some things Helix must not decide: which compute backend to compile
	// against, or whether someone accepts a model's licence. Naming the
	// specific reason is what separates "Helix will not do this for you, and
	// here is the one thing to do" from a dead end pointing at a document.
	InstallBlocker func() (string, bool)

	// ModelHint describes what the server needs besides the binary, and how to
	// get it. Empty when the binary is self-sufficient.
	ModelHint func() (cmd string, why string, ok bool)

	// Args renders the launch arguments for a port.
	Args func(port int) []string

	// Verify confirms the binary can actually RUN this sidecar, which is not
	// the same question as the binary existing.
	//
	// piper is the case that proves it: its "binary" is python3, so a machine
	// with any Python at all passed the presence check and Helix concluded
	// piper was installed. It then skipped the install step, downloaded a 60 MB
	// voice model, launched the server, and the process died on
	// ModuleNotFoundError — after three confirmations and a long download, none
	// of which could ever have led anywhere. Presence of an interpreter says
	// nothing about the module it is being asked to run.
	//
	// Returns a reason when the runtime is unusable. A nil Verify means the
	// binary is self-sufficient.
	Verify func(binary string) (reason string, ok bool)

	// PreStart reports a reason this sidecar cannot START, checked after it is
	// installed and immediately before launch.
	//
	// Distinct from Unmet, which runs FIRST and blocks the whole setup. Some
	// preconditions only apply to running: csm.rs builds perfectly well without
	// a Hugging Face account and then exits with 401 Unauthorized fetching its
	// tokenizer, because the weights are gated. Blocking that at Unmet would
	// refuse to build it at all; not checking at all — which is what happened —
	// starts a server Helix already knows cannot serve, and reports the crash
	// instead of the cause.
	PreStart func() (reason string, blocked bool)

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
			InstallBlocker: func() (string, bool) {
				// Reached only when brew is absent, which is every Windows host
				// and any Linux one without it. whisper.cpp is not carried by
				// apt, dnf, winget or choco under a name Helix can rely on, and
				// the rule here is absolute: never run a guessed package name on
				// someone's machine. So this says what is true rather than
				// inventing a command that would fail — or worse, succeed and
				// install something else.
				return "Helix knows one verified install for whisper.cpp — Homebrew's " +
					"whisper-cpp formula — and this host has no brew. No other " +
					"package manager carries it under a name Helix will guess at. " +
					"Build it from source (see docs/local_runtimes.md), or use a " +
					"cloud STT provider with whisper-local as a fallback once it " +
					"is on PATH.", true
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
			// Built, not installed — see csm_install.go. The backend is
			// DETECTED and reported rather than guessed, which is what changed
			// the old refusal into something Helix can honestly do.
			GoInstall: installCSMServer,
			Prereqs:   []string{"git", "rust"},
			PreStart: func() (string, bool) {
				// csm.rs fetches its tokenizer from the gated repo on startup.
				// Without a token that is a 401 a second after launch, and the
				// user reads a stack of HTTP errors instead of the one thing
				// they have to do.
				bin, ok := findHuggingFaceCLI()
				if ok && hfLoggedIn(bin) {
					return "", false
				}
				step := "huggingface-cli login"
				if ok {
					step = strings.Join(hfAuthArgv(bin, "login"), " ")
				}
				return "csm.rs downloads sesame/csm-1b at startup and those weights " +
					"are gated, so it exits with 401 Unauthorized until this machine " +
					"is signed in. Accept the terms at " + csmModelURL +
					" then run: " + step, true
			},
			InstallCmd: func() (string, bool) {
				// Not a single command: GoInstall above detects the backend,
				// fetches the source and builds it. The old refusal here read
				// "a CSM build chooses a compute backend, so it is printed" —
				// true of a GUESSED backend, and not of a detected one.
				return "", false
			},
			ModelHint: func() (string, string, bool) {
				// Nothing here. settleCSMWeights (hf_gate.go) handles the
				// gated weights properly — installing the CLI, opening the
				// terms page and running the login.
				//
				// It used to return
				//   "huggingface-cli login   # then accept the terms at <url>"
				// which was written to be PRINTED. Once model hints started
				// running without a confirmation, that string was exec'd — and
				// there is no shell here, so the "#" and the URL after it
				// became arguments to a login command. It failed every time,
				// and it could never have done anything else.
				return "", "", false
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
			InstallBlocker: func() (string, bool) {
				return "kokoro runs in a container, and installing a container " +
					"runtime is a licence decision and a system-wide change Helix " +
					"will not make for you. piper-local is the docker-free local " +
					"voice and Helix installs it end to end.", true
			},
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
			GoInstall: offerPiperBinary,
			// If it comes to the Python path, the interpreter is installable —
			// so a host without one is no longer a dead end. The standalone
			// binary is still tried first and needs none of this.
			Prereqs: []string{"python3"},
			// The native binary first, then the Python interpreter. Order is
			// the preference: a machine with the standalone piper needs no
			// interpreter at all, which is the whole point of shipping it.
			Binaries: []string{"piper", "piper-tts", "python3", "python"},
			InstallCmd: func() (string, bool) {
				// The binary install is NOT expressible here: runVisibleCommand
				// execs this string directly with no shell, so a download +
				// checksum + extract pipeline would be split on spaces and
				// handed to mkdir as arguments. offerSidecarInstall calls
				// offerPiperBinary first instead, which does it in Go.
				//
				// Offer the Python install ONLY to a machine that has Python.
				//
				// It used to be offered unconditionally, so a host with no
				// interpreter was told to run
				// `python3 -m pip install --user piper-tts flask` — a command
				// beginning with the thing that is missing. Helix walked the
				// user into an install that could not run, which is the exact
				// failure `Unmet` was introduced to prevent one sidecar over.
				//
				// Without Python, the honest answer is the standalone binary,
				// and offerPiperBinary below handles that path.
				if _, err := exec.LookPath("python3"); err != nil {
					if _, err := exec.LookPath("python"); err != nil {
						return "", false
					}
				}
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
			Verify: func(bin string) (string, bool) {
				// The native binary needs no module check: it IS the runtime.
				if !isPythonInterpreter(bin) {
					return "", true
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				// Import the SERVER module, not the package root: piper-tts can
				// be installed while `piper.http_server` still fails, because
				// the HTTP server imports flask and flask is not one of its
				// dependencies. Importing the package root would report success
				// for exactly the configuration that dies on startup.
				if err := exec.CommandContext(ctx, bin, "-c", "import piper.http_server").Run(); err != nil {
					return bin + " runs, but `import piper.http_server` fails — " +
						"the piper module (or its flask dependency) is not installed for this interpreter", false
				}
				return "", true
			},
			Args: func(port int) []string { return speech.PiperArgs(port) },
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
	// A binary on PATH is necessary and not sufficient. Verify runs the real
	// question — can this interpreter actually serve? — BEFORE the model
	// download and the three confirmations that follow, so a machine that
	// cannot run the sidecar is told so first rather than last.
	if installed && spec.Verify != nil {
		if reason, ok := spec.Verify(binary); !ok {
			fmt.Println()
			for _, l := range shell.PanelWrap(reason, shell.Muted) {
				fmt.Println(l)
			}
			installed = false
		}
	}
	if !installed {
		binary, installed = offerSidecarInstall(provider, spec)
		if !installed {
			return false
		}
		// The install claimed success; confirm it actually satisfied the thing
		// that was missing. pip can exit 0 having installed into a different
		// interpreter than the one Helix is about to launch.
		if spec.Verify != nil {
			if reason, ok := spec.Verify(binary); !ok {
				fmt.Println(shell.Step(shell.StateBad, provider,
					"still cannot start after the install"))
				for _, l := range shell.StepDetail(reason, shell.Muted) {
					fmt.Println(l)
				}
				return false
			}
		}
	}

	if cmd, why, ok := spec.ModelHint(); ok {
		fmt.Println()
		fmt.Println(shell.PanelLine(shell.Fg(shell.HexText, provider) +
			shell.Muted(" needs one more thing before it can start")))
		for _, l := range shell.PanelWrap(why, shell.Muted) {
			fmt.Println(l)
		}
		// Fetched, not offered. The user has already chosen this chain; asking
		// again whether they want the file it cannot start without is a
		// question with one sensible answer, and answering "n" leaves them with
		// a configuration Helix just told them to pick.
		if !runVisibleCommand(cmd, 30*time.Minute) {
			return false
		}
	}

	// The native piper needs no server: it is a CLI Helix runs per synthesis, so
	// there is no port to bind, nothing to leave running after Helix exits, and
	// nothing for the macOS AirPlay-on-5000 collision to collide with. Offering
	// to "start" it would be offering to start nothing.
	if provider == "piper-local" && !isPythonInterpreter(binary) {
		fmt.Println()
		fmt.Println(shell.PanelLine(shell.Badge(shell.StateGood, "ready") +
			shell.Muted("  piper runs on demand — no server, no port")))
		return true
	}

	if spec.PreStart != nil {
		if reason, blocked := spec.PreStart(); blocked {
			fmt.Println(shell.Step(shell.StateWarn, provider, "installed, but cannot start yet"))
			for _, l := range shell.StepDetail(reason, shell.Muted) {
				fmt.Println(l)
			}
			return false
		}
	}

	return startVoiceSidecar(kind, provider, binary, spec)
}

// offerSidecarInstall offers the one unambiguous install command, if there is
// one.
func offerSidecarInstall(provider string, spec voiceSidecar) (string, bool) {
	// A Go-driven install runs first where one exists: it is the more capable
	// path, and for piper it avoids needing an interpreter at all.
	if spec.GoInstall != nil {
		if err := ensureSidecarPrereqs(provider, spec); err != nil {
			fmt.Println(shell.Step(shell.StateBad, provider, err.Error()))
			return "", false
		}
		if bin, ok := spec.GoInstall(); ok {
			return bin, true
		}
	}

	cmd, ok := spec.InstallCmd()
	if !ok {
		// Before calling this a dead end, try to make it not one.
		//
		// A sidecar's InstallCmd can decline for two very different reasons:
		// Helix genuinely does not know how to build the thing (csm.rs picks a
		// compute backend; kokoro wants Docker), or it knows exactly how and is
		// only blocked because a PREREQUISITE is missing. piper-local was the
		// second kind: with no interpreter on the host it returned false, and
		// the user — who had just picked this chain — was told to read the docs.
		// Installing the prerequisite and asking again is the obvious move, and
		// the wizard was not making it.
		if installed := installSidecarPrereqs(provider, spec); installed {
			if cmd, ok = spec.InstallCmd(); !ok {
				return sidecarInstallDeadEnd(provider, spec)
			}
		} else {
			return sidecarInstallDeadEnd(provider, spec)
		}
	}

	fmt.Println(shell.Step(shell.StateWarn, provider, "not installed — installing it"))
	if !runVisibleCommand(cmd, 30*time.Minute) {
		return "", false
	}

	// Trust the lookup, not the exit code: a package manager can succeed while
	// putting the binary somewhere this process cannot see yet.
	binary, found := findFirstBinary(spec.Binaries)
	if !found {
		fmt.Println(shell.Step(shell.StateBad, provider, "installed, but still not on PATH"))
		for _, l := range shell.StepDetail(
			"Open a new shell and re-run /blackbox setup.", shell.Muted) {
			fmt.Println(l)
		}
		return "", false
	}
	fmt.Println(shell.Step(shell.StateGood, provider, "installed"))
	fmt.Println(shell.StepCommand(binary))
	return binary, true
}

// ensureSidecarPrereqs installs what a Go-driven install needs, and reports
// what is still missing afterwards.
//
// Distinct from installSidecarPrereqs, which is advisory: this one gates a
// build that cannot start without cargo, so "tried and failed" has to stop the
// flow rather than fall through to an error from a compiler that is not there.
func ensureSidecarPrereqs(provider string, spec voiceSidecar) error {
	if len(spec.Prereqs) == 0 {
		return nil
	}
	installSidecarPrereqs(provider, spec)

	var missing []string
	for _, name := range spec.Prereqs {
		if dep, ok := deps.Lookup(name); ok && !dep.Present() {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("needs %s, which could not be installed here",
		strings.Join(missing, " and "))
}

// sidecarInstallDeadEnd reports a provider Helix genuinely cannot install, and
// says WHY rather than pointing at a document.
//
// "See docs/local_runtimes.md" is the right answer only when the reason is a
// choice Helix must not make for someone. Naming that choice is what turns a
// dead end into a next step.
func sidecarInstallDeadEnd(provider string, spec voiceSidecar) (string, bool) {
	fmt.Println(shell.Step(shell.StateBad, provider, "cannot be installed automatically"))
	reason := "Helix has no verified install command for this platform."
	if spec.InstallBlocker != nil {
		if why, ok := spec.InstallBlocker(); ok {
			reason = why
		}
	}
	for _, l := range shell.StepDetail(reason, shell.Muted) {
		fmt.Println(l)
	}
	for _, l := range shell.StepDetail("See docs/local_runtimes.md.", shell.Muted) {
		fmt.Println(l)
	}
	return "", false
}

// installSidecarPrereqs installs the host packages a sidecar needs before its
// own installer can run, and reports whether anything was installed.
//
// This is the deps catalogue the first-run flow uses, reached on demand: the
// entries are marked Optional so they stay out of the first-boot offer, but a
// user who has now chosen a chain that needs one gets it installed rather than
// being told to go and read something.
func installSidecarPrereqs(provider string, spec voiceSidecar) bool {
	if len(spec.Prereqs) == 0 {
		return false
	}
	manager := deps.DetectManager()
	progress := false
	for _, name := range spec.Prereqs {
		dep, known := deps.Lookup(name)
		if !known || dep.Present() {
			continue
		}
		if manager == deps.ManagerUnknown {
			fmt.Println(shell.Step(shell.StateWarn, name, "missing, and no package manager was found"))
			for _, l := range shell.StepDetail(deps.ManagerHint(), shell.Muted) {
				fmt.Println(l)
			}
			continue
		}
		cmd, ok := dep.InstallCommand(manager)
		if !ok {
			// Same rule the catalogue applies everywhere: never run a guessed
			// package name under Helix's name.
			fmt.Println(shell.Step(shell.StateWarn, name,
				fmt.Sprintf("no verified install command for %s on %s", name, manager)))
			continue
		}
		fmt.Println(shell.Step(shell.StateWarn, name,
			fmt.Sprintf("%s needs it — installing", provider)))
		if !runVisibleCommand(cmd, 30*time.Minute) {
			continue
		}
		// Trust the lookup, not the exit code: a manager can succeed and still
		// leave the binary somewhere this process cannot see yet.
		if !dep.Present() {
			fmt.Println(shell.Step(shell.StateWarn, name, "installed, but still not on PATH"))
			for _, l := range shell.StepDetail(
				"Open a new shell and re-run /blackbox setup.", shell.Muted) {
				fmt.Println(l)
			}
			continue
		}
		fmt.Println(shell.Step(shell.StateGood, name, "installed"))
		progress = true
	}
	return progress
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
		fmt.Println(shell.Step(shell.StateWarn, provider,
			fmt.Sprintf("port %d is claimed by another service — using %d instead", port, assigned)))
		port = assigned
		applySidecarEndpoint(kind, provider, edge.ReplacePort(endpoint, port))
	}

	logPath, err := sidecar.LogPathFor(provider)
	if err != nil {
		fmt.Println(shell.Step(shell.StateBad, provider,
			fmt.Sprintf("cannot prepare a log file: %v", err)))
		return false
	}

	proc, err := sidecar.Start(sidecar.Spec{
		Name: provider, Binary: binary, Args: spec.Args(port), LogPath: logPath,
	})
	if err != nil {
		fmt.Println(shell.Step(shell.StateBad, provider, fmt.Sprintf("could not start: %v", err)))
		return false
	}
	fmt.Println(shell.Step(shell.StateIdle, provider,
		fmt.Sprintf("starting — pid %d, port %d", proc.PID, port)))

	// Containers cold-start slower than a native binary even once pulled, and a
	// 1B model has to be read off disk and mapped before the port answers —
	// which is startup, not a download, and so belongs in this budget. The
	// download itself is fetched separately; see settleCSMWeights.
	budget := 90 * time.Second
	switch provider {
	case "kokoro-local":
		budget = 180 * time.Second
	case "csm-local":
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
		// Rendered line by line, because this error is usually several.
		//
		// A local-sidecar diagnosis carries the address it dialled AND the
		// command that starts it, separated by newlines. Handing that whole
		// string to Step printed the first line inside the panel and let the
		// rest escape its gutter — "Start it:" and half a command sitting
		// outside the box, with the other half back inside it.
		lines := strings.Split(strings.TrimSpace(ready.Error()), "\n")
		fmt.Println(shell.Step(shell.StateBad, provider, "did not come up: "+strings.TrimSpace(lines[0])))
		for _, line := range lines[1:] {
			for _, l := range shell.StepDetail(strings.TrimSpace(line), shell.Muted) {
				fmt.Println(l)
			}
		}
		// Wrapped, not truncated. At 160 characters a Python traceback loses
		// exactly its payload: the real failure printed
		// "(ModuleNotFoundError: No modul…" and cut off before naming the
		// module, which is the only part anyone can act on.
		for _, line := range sidecar.LogTail(proc.LogPath, 8) {
			for _, l := range shell.StepDetail(line, shell.Muted) {
				fmt.Println(l)
			}
		}
		return false
	}

	fmt.Println(shell.Step(shell.StateGood, provider, fmt.Sprintf("answering on port %d", port)))
	// Detached on purpose, so it must be announced: the user now owns it. Two
	// KV rows rather than two more sentences, because these are facts to come
	// back to — the command that stops it, and where to read why it died.
	w := shell.KVWidth("KEEP-ALIVE", "LOG")
	fmt.Println(shell.KV("KEEP-ALIVE", shell.Muted("runs after Helix exits · stop with  ")+
		shell.Value(proc.StopHint()), w))
	fmt.Println(shell.KV("LOG", shell.Value(proc.LogPath), w))
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
	// Reported, not swallowed. An endpoint that fails to save is exactly the
	// bug this function exists to prevent, and it would be invisible.
	if err := cfg.SavePreferences(); err != nil {
		wizStep(shell.StateWarn, provider, fmt.Sprintf("could not save the new endpoint: %v", err))
	}
	if err := speech.Init(speechConfigFrom(cfg.Speech)); err != nil {
		wizStep(shell.StateWarn, provider, fmt.Sprintf("speech engine rebuild failed: %v", err))
	}
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
	return runVisibleCommandIn(cmdLine, "", nil, timeout)
}

// runVisibleCommandIn is runVisibleCommand with a working directory and extra
// environment.
//
// Both are needed by builds rather than installs: `cargo build` runs inside a
// checkout, and the CPU backends want RUSTFLAGS. Neither can be expressed in
// the command STRING, because it is split on spaces and exec'd with no shell —
// "RUSTFLAGS=... cargo" would look for a binary with an equals sign in its name.
func runVisibleCommandIn(cmdLine, dir string, env []string, timeout time.Duration) bool {
	return runVisibleArgv(strings.Fields(cmdLine), dir, env, timeout)
}

// runVisibleArgv is runVisibleCommandIn for a command that is already split.
//
// Splitting a string on spaces is fine for the fixed command lines in this
// package, and wrong the moment a PATH is involved: pip installs user scripts
// under the home directory, and a home directory with a space in it turns
// "/Users/Jo Smith/Library/Python/3.14/bin/hf login" into four arguments and a
// binary that does not exist. Callers holding a resolved path pass argv.
func runVisibleArgv(fields []string, dir string, env []string, timeout time.Duration) bool {
	return runVisibleArgvOpts(fields, dir, env, timeout, false)
}

// runVisibleStreamed hands the child the REAL terminal instead of a pipe.
//
// For a long download this is the difference between a progress bar and a
// frozen line. os/exec only passes the terminal's file descriptor through when
// cmd.Stdout is an *os.File; wrap it in an io.MultiWriter to capture the output
// and os/exec inserts an os.Pipe, the child's isatty check fails, and a tool
// like `hf download` falls back to a display that cannot redraw. The same
// mechanism is documented in internal/commands/sandbox.go, where capture is
// opt-in for exactly this reason — it was simply not applied here.
//
// The cost is the captured copy used by diagnoseInstallFailure. That is the
// right trade for a multi-gigabyte fetch: the failure output is on screen
// either way, and what a download needs is to be watchable while it works.
func runVisibleStreamed(fields []string, dir string, env []string, timeout time.Duration) bool {
	return runVisibleArgvOpts(fields, dir, env, timeout, true)
}

func runVisibleArgvOpts(
	fields []string, dir string, env []string, timeout time.Duration, streamed bool,
) bool {
	if len(fields) == 0 {
		return false
	}
	cmdLine := strings.Join(fields, " ")
	fmt.Println(shell.StepCommand(cmdLine))
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	unreg := utils.RegisterOperation(cancel)
	defer unreg()

	// Streamed to the terminal AND captured. Streaming alone is right for the
	// progress — package managers draw their own, and animating over it makes
	// interleaved garbage — but it leaves nothing to diagnose with, and pip's
	// resolver failure is sixty lines of version list ending in one sentence
	// that matters. The tee is bounded so a runaway installer cannot grow the
	// shell's heap.
	var captured boundedLog
	c := exec.CommandContext(ctx, fields[0], fields[1:]...)
	c.Dir = dir
	// Stdin is inherited, because some of these genuinely ask.
	//
	// `sudo apt-get install` wants a password on a host that has not cached
	// one, and `huggingface-cli login` wants a token. Without a stdin those do
	// not prompt — they fail, or hang, with the reason off-screen.
	c.Stdin = os.Stdin
	if len(env) > 0 {
		c.Env = append(os.Environ(), env...)
	}
	if streamed {
		// The terminal's own descriptors, so the child sees a TTY.
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
	} else {
		c.Stdout = io.MultiWriter(os.Stdout, &captured)
		c.Stderr = io.MultiWriter(os.Stderr, &captured)
	}

	if err := c.Run(); err != nil {
		fmt.Println()
		if ctx.Err() != nil {
			fmt.Println(shell.Step(shell.StateWarn, "cancelled", ""))
			return false
		}
		fmt.Println(shell.Step(shell.StateBad, "failed", err.Error()))
		// The output is already on screen; what is missing is the reading of it.
		for _, line := range diagnoseInstallFailure(cmdLine, captured.String()) {
			fmt.Println(line)
		}
		return false
	}
	fmt.Println()
	return true
}

// boundedLog keeps the tail of a command's output for diagnosis.
//
// A ring rather than a growing buffer: this tees a subprocess that Helix does
// not control, and "however much it prints" is not a size.
type boundedLog struct {
	buf []byte
}

// boundedLogMax is the tail kept. Generous enough for a pip resolver failure,
// whose one useful sentence arrives at the very end after the version list.
const boundedLogMax = 64 << 10

func (b *boundedLog) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if len(b.buf) > boundedLogMax {
		b.buf = b.buf[len(b.buf)-boundedLogMax:]
	}
	return len(p), nil
}

func (b *boundedLog) String() string { return string(b.buf) }

// diagnoseInstallFailure reads an installer's own output and says what to do.
//
// The failure that prompted this, from a live session on an Intel Mac:
// `python3 -m pip install --user piper-tts flask` printed sixty lines of
// candidate versions and ended in ResolutionImpossible, because onnxruntime
// publishes no wheels for macOS x86_64 on Python 3.14. Helix showed all sixty
// lines and then said "failed exit status 1" — which is true, useless, and
// blames the wrong layer. The user is left believing Helix is broken when the
// actual answer is "this interpreter cannot have this package, pick another
// voice or another Python".
//
// Diagnosis rather than prediction, deliberately: which Python versions have
// wheels for which package on which architecture is a moving target that Helix
// would get wrong, while the installer has just finished saying so precisely.
//
// Args: cmdLine: what was run; output: its captured stdout+stderr tail.
// Returns: rendered panel lines, empty when nothing is recognised.
func diagnoseInstallFailure(cmdLine, output string) []string {
	lower := strings.ToLower(output)
	var out []string
	add := func(lines ...string) { out = append(out, lines...) }

	switch {
	case strings.Contains(lower, "no matching distribution") ||
		strings.Contains(lower, "resolutionimpossible"):
		missing := missingDistributions(output)
		detail := "this Python cannot install " + strings.Join(missing, " or ")
		if len(missing) == 0 {
			detail = "this Python has no compatible build of a required package"
		}
		add(shell.Step(shell.StateBad, "not installable here", detail))
		for _, l := range shell.StepDetail(
			"It is not a Helix problem and not a network problem: the package "+
				"publishes no wheel for "+runtime.GOOS+"/"+runtime.GOARCH+
				" on "+pythonVersionLabel()+", and there is no source build "+
				"either. Three ways forward, in the order most people want them:",
			shell.Muted) {
			add(l)
		}
		add(shell.StepCommand("pick a different voice: /blackbox setup → cheapest cloud"))
		add(shell.StepCommand("or install a Python that has wheels, then re-run /blackbox setup"))
		add(shell.StepCommand("or use kokoro-local if you already run Docker"))

	case strings.Contains(lower, "externally-managed-environment"):
		add(shell.Step(shell.StateBad, "blocked by the OS",
			"this Python refuses user installs (PEP 668) — its packages belong to the system"))
		for _, l := range shell.StepDetail(
			"Install the distribution's own package if it has one, or make a "+
				"virtualenv and point Helix at it.", shell.Muted) {
			add(l)
		}

	case strings.Contains(lower, "permission denied"):
		add(shell.Step(shell.StateBad, "permission denied",
			"the install target is not writable by this user"))
	}

	if len(out) > 0 {
		add(shell.Hint("/blackbox setup offers every alternative with prices"))
	}
	_ = cmdLine
	return out
}

// missingDistributions pulls package names out of pip's own summary.
//
// pip ends a resolution failure with "some packages ... have no matching
// distributions available for your environment:" followed by one indented name
// per line. Reading that beats guessing, and beats reprinting sixty lines.
func missingDistributions(output string) []string {
	const marker = "no matching distributions available for your environment:"
	i := strings.Index(strings.ToLower(output), marker)
	if i < 0 {
		return nil
	}
	var names []string
	for _, line := range strings.Split(output[i+len(marker):], "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		// The block ends at the first line that is not an indented bare name.
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			break
		}
		if strings.ContainsAny(name, " :\t") {
			break
		}
		names = append(names, name)
		if len(names) == 6 {
			break
		}
	}
	return names
}

// pythonVersionLabel reports the interpreter's version, for the diagnosis.
//
// Best-effort and never fatal: a diagnosis that cannot name the version is
// still worth printing, and this runs only after an install has already failed.
func pythonVersionLabel() string {
	for _, bin := range []string{"python3", "python"} {
		path, err := exec.LookPath(bin)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := exec.CommandContext(ctx, path, "-c",
			"import sys;print('Python %d.%d'%sys.version_info[:2])").Output()
		cancel()
		if err == nil {
			if v := strings.TrimSpace(string(out)); v != "" {
				return v
			}
		}
	}
	return "this Python"
}

// piperVoiceBaseURL is where the default voice model lives.
const piperVoiceBaseURL = "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/medium"

// piperVoicePath is where Helix keeps the default piper voice. Defined in
// internal/speech so the launcher, the diagnosis and the wizard hint cannot
// disagree about it again.
func piperVoicePath() string { return speech.PiperVoicePath() }

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

// isPythonInterpreter reports whether a resolved binary is a Python, as opposed
// to Piper's own standalone executable.
//
// Matters because the two need completely different readiness checks: the
// interpreter must be able to import `piper.http_server`, while the native
// binary simply has to run. Asking the module question of a native piper would
// fail it for missing something it does not use.
func isPythonInterpreter(bin string) bool {
	base := strings.ToLower(filepath.Base(bin))
	return strings.HasPrefix(base, "python")
}

// piperBinaryInstallCmd renders the download for Piper's standalone binary.
//
// Extracted into ~/.helix/piper with --strip-components=1, because the archive
// wraps everything in a top-level piper/ directory and the executable needs its
// espeak-ng data and shared libraries sitting BESIDE it — extracting only the
// binary produces one that cannot start.
