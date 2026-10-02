package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// One request's `cd` must not decide where the next request runs (found in a
// daemon-driven session, where every later request ran in the last cd target).
func TestEachRequestStartsInTheDaemonsHome(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	t.Chdir(home)
	d := &Daemon{home: home}

	// What a previous request's `cd` step leaves behind.
	if err := os.Chdir(elsewhere); err != nil {
		t.Fatal(err)
	}
	d.restoreHome()
	if wd, _ := os.Getwd(); wd != home {
		t.Fatalf("request starts in %s, want %s", wd, home)
	}

	// A home that has disappeared is reported, not fatal.
	gone := filepath.Join(home, "gone")
	(&Daemon{home: gone}).restoreHome()
	if wd, _ := os.Getwd(); wd != home {
		t.Fatalf("a failed restore moved the process to %s", wd)
	}
}
