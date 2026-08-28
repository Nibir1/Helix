// cmd/helix/csm_install_test.go
//
// Purpose: the backend must be DETECTED, and the detection must be reported.
//
// The old refusal to build csm.rs rested on one sentence — "picking mkl for
// someone with a 3080 would silently hand them a CPU build" — and the load
// bearing word in it is "silently". Guessing is what was wrong, not choosing.
// These tests hold the two properties that make choosing acceptable: the
// decision follows the hardware, and it arrives with its evidence attached.
package main

import (
	"runtime"
	"strings"
	"testing"
)

// The chosen feature must be one csm.rs actually accepts. A typo here compiles
// nothing and is only discovered tens of minutes into a build.
func TestCSMBackendIsAKnownCandleFeature(t *testing.T) {
	known := map[string]bool{"cuda": true, "cudnn": true, "metal": true, "accelerate": true, "mkl": true}
	feature, why := csmBackend()
	if !known[feature] {
		t.Errorf("csmBackend chose %q, which is not a candle feature csm.rs builds with", feature)
	}
	if strings.TrimSpace(why) == "" {
		t.Error("csmBackend gave no reason — a detected choice with no evidence " +
			"on screen is indistinguishable from a guess")
	}
}

// On this host the answer must match the platform, since no NVIDIA GPU is
// present in CI or on a Mac.
func TestCSMBackendFollowsThePlatform(t *testing.T) {
	if _, ok := nvidiaGPU(); ok {
		t.Skip("this host has an NVIDIA GPU; the cuda branch is correct here")
	}
	feature, why := csmBackend()
	var want string
	switch {
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		want = "metal"
	case runtime.GOOS == "darwin":
		want = "accelerate"
	default:
		want = "mkl"
	}
	if feature != want {
		t.Errorf("on %s/%s csmBackend chose %q, want %q (%s)",
			runtime.GOOS, runtime.GOARCH, feature, want, why)
	}
}

// target-cpu=native belongs to the CPU backends and nowhere else: it is
// meaningless where the work is on a GPU, and passing it there is noise in a
// build log that is already long.
func TestCSMBuildEnvOnlyTunesCPUBackends(t *testing.T) {
	for _, gpu := range []string{"cuda", "cudnn", "metal"} {
		if env := csmBuildEnv(gpu); len(env) != 0 {
			t.Errorf("%s backend got build env %v, want none", gpu, env)
		}
	}
	for _, cpu := range []string{"accelerate", "mkl"} {
		env := csmBuildEnv(cpu)
		if len(env) == 0 {
			t.Errorf("%s backend got no RUSTFLAGS; target-cpu=native is what "+
				"makes a CPU build worth having", cpu)
			continue
		}
		if !strings.Contains(strings.Join(env, " "), "target-cpu=native") {
			t.Errorf("%s backend env %v does not set target-cpu=native", cpu, env)
		}
	}
}

// The env must be shaped for exec, not for a shell: runVisibleCommandIn appends
// these to os.Environ(), so each entry has to be a bare KEY=VALUE.
func TestCSMBuildEnvIsExecShaped(t *testing.T) {
	for _, e := range csmBuildEnv("mkl") {
		if !strings.Contains(e, "=") {
			t.Errorf("env entry %q is not KEY=VALUE", e)
		}
		if strings.HasPrefix(e, "export ") || strings.Contains(e, "$") {
			t.Errorf("env entry %q is written for a shell; there is no shell here", e)
		}
	}
}

// csm-local must be installable now, and must still declare what it needs.
func TestCSMDeclaresItsBuildPrerequisites(t *testing.T) {
	sc, ok := voiceSidecars()["csm-local"]
	if !ok {
		t.Fatal("csm-local is not in the sidecar table")
	}
	if sc.GoInstall == nil {
		t.Fatal("csm-local has no GoInstall — it is a printed command again")
	}
	need := map[string]bool{}
	for _, p := range sc.Prereqs {
		need[p] = true
	}
	// Without either, the build fails partway with a compiler error rather
	// than a sentence naming the missing tool.
	for _, want := range []string{"git", "rust"} {
		if !need[want] {
			t.Errorf("csm-local does not declare %q as a prerequisite", want)
		}
	}
}
