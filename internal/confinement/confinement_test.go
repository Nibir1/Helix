// internal/confinement/confinement_test.go
// Purpose: Platform-neutral verification of profile/argv generation for all
// three confinement backends.
package confinement

import (
	"strings"
	"testing"
)

func TestBuildSeatbeltProfile(t *testing.T) {
	prof := BuildSeatbeltProfile(Profile{Root: "/private/tmp/jail", ExtraRW: []string{"/private/tmp/x"}})
	for _, want := range []string{
		"(version 1)",
		"(allow default)",
		`(deny file-write* (subpath "/"))`,
		`(allow file-write* (subpath (param "HELIX_ROOT")))`,
		`(allow file-write* (subpath (param "HELIX_EXTRA_0")))`,
	} {
		if !strings.Contains(prof, want) {
			t.Fatalf("seatbelt profile missing %q", want)
		}
	}
}

func TestBuildBwrapArgs(t *testing.T) {
	joined := strings.Join(BuildBwrapArgs(Profile{Root: "/home/u/proj"}), " ")
	for _, want := range []string{
		"--ro-bind / /", "--bind /home/u/proj /home/u/proj",
		"--proc /proc", "--dev /dev", "--unshare-pid", "--unshare-ipc", "--die-with-parent",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("bwrap args missing %q", want)
		}
	}
}

func TestBuildLandlockChildArgs(t *testing.T) {
	args, err := BuildLandlockChildArgs("/usr/local/bin/helix",
		Profile{Root: "/home/u/proj", Cwd: "/home/u/proj"}, []string{"/bin/sh", "-c", "touch ok"})
	if err != nil {
		t.Fatalf("BuildLandlockChildArgs: %v", err)
	}
	if args[0] != "/usr/local/bin/helix" || args[1] != "--confined-child" || args[3] != "--" {
		t.Fatalf("malformed child argv: %v", args)
	}
	if !strings.Contains(args[2], "/home/u/proj") {
		t.Fatalf("spec missing root: %s", args[2])
	}
	if args[4] != "/bin/sh" || args[5] != "-c" {
		t.Fatal("command argv not preserved")
	}
}

func TestResolveRootNoPanic(t *testing.T) {
	if got := ResolveRoot(t.TempDir()); got == "" {
		t.Fatal("expected non-empty resolved root")
	}
}
