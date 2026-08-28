// tests/e2e/main_test.go
// Purpose: Build the helix binary once for the e2e suite. Lives in a
// build-tag-free file so the non-PTY (daemon remote) tests share the same
// binary on Windows as well as macOS/Linux.
package e2e

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// binPath is the compiled helix binary built once in TestMain.
var binPath string

// TestMain builds the helix binary once for all e2e tests.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "helix-e2e-bin-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: temp dir:", err)
		os.Exit(1)
	}
	binPath = filepath.Join(tmp, "helix")
	if runtime.GOOS == "windows" {
		binPath += ".exe" // go build appends .exe on Windows
	}
	// Make the source tree part of THIS test binary's cache key.
	//
	// Go caches a package's test result on its own inputs, and this package's
	// inputs do not include cmd/helix: the binary under test is built by a
	// subprocess below, which Go's dependency tracking cannot see. So after a
	// change to cmd/helix, `go test ./tests/e2e/` happily replays a PASS
	// recorded against the OLD binary — the suite reports green without having
	// run against the code it exists to test.
	//
	// That is not hypothetical. It masked a real /doctor regression: the panel
	// rewrite changed the output an e2e test asserts on, `go test ./... ./tests/...`
	// reported green, and the failure surfaced only when `make e2e` ran it with
	// -count=1. Reading the sources here folds their content into the cache key,
	// so a stale pass is no longer possible whatever command invokes the suite.
	//
	// -count=1 in the Makefile remains correct and is now a belt to this braces:
	// this makes the caching CORRECT rather than relying on every caller
	// remembering to defeat it.
	//
	// The stamping itself lives in TestSourceTreeIsPartOfTheCacheKey rather
	// than here. It has to: `go test` records opened files through a logger
	// that `m.Run` installs, so anything read from TestMain BEFORE that call is
	// invisible to the cache. Measured — reads placed here left the input ID
	// byte-identical across a source change.
	build := exec.Command("go", "build", "-o", binPath, "helix/cmd/helix")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: build failed: %v\n", err)
		_ = os.RemoveAll(tmp)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

// TestSourceTreeIsPartOfTheCacheKey reads every Go source file the binary under
// test is built from, so their contents enter this package's test cache key.
//
// It asserts nothing. The READ is the whole mechanism, and it must happen
// inside the test phase where the testlog is active.
func TestSourceTreeIsPartOfTheCacheKey(t *testing.T) {
	n := stampSourceInputs()
	if n == 0 {
		t.Fatal("read no sources; the e2e cache key would not track the binary under test")
	}
	t.Logf("cache key covers %d source files", n)
}

// stampSourceInputs reads every Go source file the binary under test is built
// from, and reports how many it read.
//
// Reading is the mechanism: `go test` records the files a test opens and
// invalidates a cached result when any of them changes. Nothing is done with
// the bytes — the read itself is the point.
//
// Failures are ignored deliberately. A missing or unreadable tree is the build
// step's problem to report a moment later, with a better message than anything
// this could produce, and refusing to run the suite over an unreadable file
// would trade a stale pass for no result at all.
func stampSourceInputs() int {
	n := 0
	for _, root := range []string{"../../cmd", "../../internal"} {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil //nolint:nilerr // an unreadable tree is the build's problem
			}
			if _, rerr := os.ReadFile(path); rerr == nil {
				n++
			}
			return nil
		})
	}
	return n
}
