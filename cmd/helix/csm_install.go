// cmd/helix/csm_install.go
//
// Purpose: build the Sesame CSM-1B voice server, instead of printing four
// commands and hoping.
//
// This used to be a deliberate refusal, and the reasoning was sound as far as
// it went: csm.rs is BUILT rather than installed, the candle backend is a
// COMPILE-TIME choice, and "picking mkl for someone with a 3080 would silently
// hand them a CPU build". Helix's rule is that it only installs when there is
// one obvious command that makes no choices on the user's behalf.
//
// The flaw was in the word "silently". The objection is not that the backend is
// unknowable — it is that GUESSING it is wrong. An RTX 3080 announces itself
// through nvidia-smi, and Apple Silicon through GOARCH; the choice is
// detectable, and a detected choice that is stated out loud is not a choice
// made on someone's behalf. So the backend is probed, the evidence is printed
// with it, and the user sees `cuda — nvidia-smi reports NVIDIA GeForce RTX 3080`
// before anything compiles.
//
// One thing here genuinely cannot be automated, and it is not technical: the
// weights are LICENCE-GATED. Accepting Sesame's terms is an act of consent that
// belongs to a person and an account, so the build finishes and then stops,
// naming the single remaining step. That is a boundary, not a gap.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"helix/internal/shell"
)

const (
	csmRepoURL = "https://github.com/cartesia-one/csm.rs"

	// A from-source build of a candle project pulls hundreds of crates and
	// compiles them. On a laptop this is tens of minutes, and a CUDA build is
	// slower still — the budget has to fit the worst honest case, not the best.
	csmBuildTimeout = 90 * time.Minute
	csmFetchTimeout = 20 * time.Minute

	// csmBuildDiskBytes is what a release build of candle needs for its object
	// files and archives. Measured at roughly 8 GB on a completed build; the
	// margin is deliberate, because running out at 95% costs the whole compile.
	csmBuildDiskBytes = 12 << 30

	// csmWeightsDiskBytes covers model.safetensors plus the small files beside
	// it. The sharded second copy is excluded from the download (see
	// csmDownloadFilters), so this is the real figure rather than the repo's.
	csmWeightsDiskBytes = 8 << 30
)

// csmSourceDir is where Helix keeps the checkout it builds.
//
// Inside ~/.helix rather than a temp directory: the build is expensive enough
// that a re-run should update and rebuild incrementally rather than start over,
// and /purge already knows how to reclaim this tree.
func csmSourceDir() string { return helixPath("csm.rs") }

// csmServerPath is the binary cargo produces.
func csmServerPath() string {
	name := "server"
	if runtime.GOOS == "windows" {
		name = "server.exe"
	}
	return filepath.Join(csmSourceDir(), "target", "release", name)
}

// findCSMServer returns a usable csm.rs server binary.
//
// Helix's own build is preferred over PATH for the same reason piper's is: this
// one is known to be the thing Helix compiled, with the backend it chose.
func findCSMServer() (string, bool) {
	if p := csmServerPath(); isRunnableFile(p) {
		return p, true
	}
	if p, err := exec.LookPath("csm-server"); err == nil {
		return p, true
	}
	return "", false
}

// csmBackend picks the candle feature to compile against, and says why.
//
// The "why" is not decoration. It is the difference between Helix choosing for
// the user and Helix reporting what it found: a wrong detection is visible and
// arguable when the evidence is on screen, and invisible when it is not.
func csmBackend() (feature, why string) {
	if gpu, ok := nvidiaGPU(); ok {
		return "cuda", "nvidia-smi reports " + gpu
	}
	switch {
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		return "metal", "Apple Silicon — candle's Metal backend targets it"
	case runtime.GOOS == "darwin":
		// candle's Metal backend targets Apple Silicon, so an Intel Mac runs
		// this on CPU whatever is requested. Accelerate is the fast CPU path.
		return "accelerate", "Intel Mac — Metal targets Apple Silicon, so this is a CPU build"
	default:
		return "mkl", "no NVIDIA GPU detected — CPU build"
	}
}

// nvidiaGPU reports the first GPU nvidia-smi names, if it answers at all.
func nvidiaGPU() (string, bool) {
	bin, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return "", false
	}
	out, err := exec.Command(bin, "--query-gpu=name", "--format=csv,noheader").Output() //nolint:gosec // resolved from PATH
	if err != nil {
		// Present but not answering means no usable driver, which is a CPU
		// build — not a reason to abandon the install.
		return "", false
	}
	name := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if name == "" {
		return "", false
	}
	return name, true
}

// csmBuildEnv returns the environment for a backend's build.
//
// target-cpu=native is what makes the CPU backends worth having, and it is
// meaningless for cuda/metal where the work is not on the CPU.
func csmBuildEnv(feature string) []string {
	switch feature {
	case "accelerate", "mkl":
		return []string{"RUSTFLAGS=-C target-cpu=native"}
	default:
		return nil
	}
}

// installCSMServer builds csm.rs and returns the server binary.
//
// Returns ("", false) when a step fails; every failure has already been
// reported by the time it does.
func installCSMServer() (string, bool) {
	if bin, ok := findCSMServer(); ok {
		// Already built, but the account and the weights are separate state and
		// may still be missing — a previous run can have built the server and
		// stopped at the licence. Both steps below are no-ops when already done.
		settleCSMWeights()
		return bin, true
	}

	feature, why := csmBackend()
	dir := csmSourceDir()

	fmt.Println()
	fmt.Println(shell.PanelTitle("building csm-1b"))
	for _, l := range shell.PanelWrap(
		"csm.rs ships as source because its compute backend is chosen at compile "+
			"time. Helix detects the backend rather than guessing it, and shows "+
			"you what it found before anything is built.", shell.Muted) {
		fmt.Println(l)
	}
	w := shell.KVWidth("BACKEND", "SOURCE", "BUILD IN", "TAKES")
	fmt.Println(shell.PanelGap())
	fmt.Println(shell.KV("BACKEND", shell.Value(feature)+shell.Muted("  "+why), w))
	fmt.Println(shell.KV("SOURCE", shell.Muted(csmRepoURL), w))
	fmt.Println(shell.KV("BUILD IN", shell.Muted(dir), w))
	fmt.Println(shell.KV("TAKES", shell.Muted("tens of minutes — it compiles candle from source"), w))
	fmt.Println(shell.PanelEnd())

	// Refuse before starting, not halfway through.
	//
	// A release build of candle writes several gigabytes of object files, and
	// cargo does not check first — it compiles for minutes and then dies with
	// "No space left on device" across a dozen crates at once. Everything after
	// it fails too, in ways that look unrelated: a curl that cannot write a
	// voice model, and a config save that reports the same thing. The disk is
	// the cause and it is the least visible line on the screen.
	if !enoughDiskFor("the CSM build", dir, csmBuildDiskBytes) {
		return "", false
	}

	if !fetchCSMSource(dir) {
		return "", false
	}

	fmt.Println(shell.Step(shell.StateIdle, "csm-local",
		fmt.Sprintf("building with the %s backend", feature)))
	build := "cargo build --release --features " + feature
	if !runVisibleCommandIn(build, dir, csmBuildEnv(feature), csmBuildTimeout) {
		return "", false
	}

	// Trust the artifact, not the exit code: cargo can succeed while producing
	// a binary under a name or path this does not expect.
	bin, ok := findCSMServer()
	if !ok {
		fmt.Println(shell.Step(shell.StateBad, "csm-local",
			"the build reported success but produced no server binary"))
		for _, l := range shell.StepDetail(
			"Expected "+csmServerPath()+" — see docs/local_runtimes.md.", shell.Muted) {
			fmt.Println(l)
		}
		return "", false
	}
	fmt.Println(shell.Step(shell.StateGood, "csm-local", "built"))
	fmt.Println(shell.StepCommand(bin))
	settleCSMWeights()
	return bin, true
}

// fetchCSMSource clones the repository, or updates an existing checkout.
func fetchCSMSource(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		fmt.Println(shell.Step(shell.StateIdle, "csm.rs", "updating the existing checkout"))
		// A failed update is not fatal: the checkout already present builds,
		// and refusing to build because a fetch failed would make an offline
		// machine worse off than a machine with no checkout at all.
		if !runVisibleCommandIn("git pull --ff-only", dir, nil, csmFetchTimeout) {
			fmt.Println(shell.Step(shell.StateWarn, "csm.rs",
				"could not update — building the checkout already here"))
		}
		return true
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		fmt.Println(shell.Step(shell.StateBad, "csm.rs", "cannot create "+filepath.Dir(dir)))
		return false
	}
	return runVisibleCommand("git clone --depth 1 "+csmRepoURL+" "+dir, csmFetchTimeout)
}

// reportCSMWeightsGate names the one step Helix will not take.
//
// Not a failure and not a gap: accepting a model's licence is consent tied to a
// person and an account. Helix says exactly what to run and stops, rather than
// leaving the user to discover it from a download error on first synthesis.
func reportCSMWeightsGate() {
	fmt.Println()
	fmt.Println(shell.PanelTitle("one step is yours"))
	for _, l := range shell.PanelWrap(
		"CSM's weights are gated. Accepting Sesame's terms is your decision and "+
			"needs your Hugging Face account, so Helix does not do it for you. "+
			"Once you have, csm.rs downloads the weights on first run.", shell.Muted) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelGap())
	fmt.Println(shell.PanelLine(shell.Muted("accept the terms at")))
	fmt.Println(shell.StepCommand("https://huggingface.co/sesame/csm-1b"))
	fmt.Println(shell.PanelLine(shell.Muted("then log in")))
	fmt.Println(shell.StepCommand("huggingface-cli login"))
	fmt.Println(shell.PanelEnd())
}

// isRunnableFile reports whether path is a regular file with an execute bit.
func isRunnableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true // no execute bit; the extension decides
	}
	return info.Mode()&0o111 != 0
}
