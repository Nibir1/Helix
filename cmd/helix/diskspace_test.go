// cmd/helix/diskspace_test.go
//
// Purpose: refuse a multi-gigabyte operation the disk cannot hold, before it
// starts rather than partway through.
//
// Observed on a machine with 3.3 GB free: a release build of candle compiled
// for minutes and then died with "No space left on device" across a dozen
// crates at once. Everything after it failed for the same reason and none of it
// said so — a curl that could not write a voice model, and a config save
// reporting the same error two panels later. The disk was the cause and the
// least visible line on the screen.
package main

import (
	"os"
	"strings"
	"testing"
)

// The measurement must work on this host, and be plausible.
func TestFreeBytesMeasuresSomething(t *testing.T) {
	free, ok := freeBytes(os.TempDir())
	if !ok {
		t.Skip("this filesystem cannot be measured")
	}
	if free == 0 {
		t.Error("free space reported as zero on a filesystem that just held a test binary")
	}
	t.Logf("%s free on %s", humanBytes(free), os.TempDir())
}

// A requirement nothing could satisfy must be refused.
func TestImpossibleRequirementIsRefused(t *testing.T) {
	if _, ok := freeBytes(os.TempDir()); !ok {
		t.Skip("this filesystem cannot be measured")
	}
	// Larger than any disk this will run on.
	const absurd = 1 << 62
	if enoughDiskFor("a test", os.TempDir(), absurd) {
		t.Error("an operation needing 4 exabytes was allowed to start")
	}
}

// A modest requirement must not block a normal machine.
func TestSmallRequirementIsAllowed(t *testing.T) {
	if !enoughDiskFor("a test", os.TempDir(), 1<<10) {
		t.Error("a 1 KB requirement was refused; the check is too aggressive")
	}
}

// An unmeasurable filesystem must not block anything.
//
// Guessing that a disk is too small on a host where the syscall failed would
// stop a machine that is probably fine, and the operation's own error is the
// fallback.
func TestUnmeasurableFilesystemIsAllowed(t *testing.T) {
	if !enoughDiskFor("a test", "/helix-no-such-path-anywhere", 1<<40) {
		t.Error("an unmeasurable path was treated as too small")
	}
}

func TestHumanBytesReadsLikeASize(t *testing.T) {
	cases := map[uint64]string{
		512:      "512 B",
		1 << 10:  "1.0 KB",
		1 << 20:  "1.0 MB",
		1 << 30:  "1.0 GB",
		12 << 30: "12.0 GB",
		1 << 40:  "1.0 TB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

// Both multi-gigabyte operations must check before starting.
func TestBigOperationsCheckTheDiskFirst(t *testing.T) {
	build := functionBody(readSourceFile(t, "csm_install.go"), "func installCSMServer(")
	if !strings.Contains(stripComments(build), "enoughDiskFor(") {
		t.Error("the CSM build does not check for space; cargo compiles for " +
			"minutes before failing on a full disk")
	}
	fetch := functionBody(readSourceFile(t, "hf_gate.go"), "func fetchCSMWeights(")
	if !strings.Contains(stripComments(fetch), "enoughDiskFor(") {
		t.Error("the weights download does not check for space")
	}
}
