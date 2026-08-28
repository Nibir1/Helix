// cmd/helix/piper_install_test.go
// Purpose: the piper binary install is a ~26 MB executable payload fetched over
// the network and then RUN, so its integrity check and its extraction are
// pinned rather than trusted.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// An archive entry that escapes the destination must be refused. The checksum
// makes a hostile archive unlikely; it does not make this check pointless, and
// a path guard costs nothing.
//
// Entry names arrive with the archive's top-level directory still attached,
// which is what archiveRel strips before judging them.
func TestArchiveRelRefusesEscapingEntries(t *testing.T) {
	for _, evil := range []string{
		"piper/../outside.txt",
		"piper/../../etc/passwd",
		"piper/nested/../../escape",
		"piper//etc/passwd",
		"piper/" + string(filepath.Separator) + "etc",
		"piper/..",
	} {
		if rel, ok := archiveRel(evil); ok {
			t.Errorf("entry %q escapes the destination and must be refused (got %q)", evil, rel)
		}
	}
	for _, good := range []string{
		"piper/piper",
		"piper/espeak-ng-data/en_dict",
		"piper/lib/libpiper.so",
	} {
		if _, ok := archiveRel(good); !ok {
			t.Errorf("entry %q is inside the destination but was refused", good)
		}
	}
}

// An absolute entry name must be refused on every host, including the Windows
// forms a Unix-only prefix check would wave through.
func TestArchiveRelRefusesAbsoluteAndReservedNames(t *testing.T) {
	for _, evil := range []string{
		"piper//abs",
		"piper/C:/Windows/System32/drivers/etc/hosts",
		"piper/\\\\server\\share\\x",
		"piper/..\\..\\escape",
	} {
		if rel, ok := archiveRel(evil); ok && filepath.IsAbs(rel) {
			t.Errorf("entry %q resolved to an absolute path %q", evil, rel)
		}
	}
}

// The real reason this writes through an os.Root rather than a checked string.
//
// Extraction runs over a directory that already exists — ~/.helix/piper, re-used
// across installs and upgrades. If anything in that tree is a symlink pointing
// out of it, an entry named "piper/lib/x" is still perfectly local by every
// name-based test, and a string-checked write follows the link straight out.
// A Root is resolved by the kernel inside the root, so it refuses instead.
func TestExtractWillNotWriteThroughAPreexistingSymlink(t *testing.T) {
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	// The destination already contains a link out of itself.
	if err := os.Symlink(outside, filepath.Join(dest, "lib")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := "pwned"
	if err := tw.WriteHeader(&tar.Header{
		Name: "piper/lib/victim.txt", Mode: 0o644,
		Size: int64(len(body)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	// Extraction may fail or skip the entry; what it must NOT do is write out.
	_ = extractTarGz(archive, dest)

	got, err := os.ReadFile(victim) //nolint:gosec // test fixture
	if err != nil {
		t.Fatalf("victim file disappeared: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("extraction followed a symlink out of the destination: "+
			"%s now contains %q", victim, got)
	}
}

// The archive wraps everything in a top-level piper/ directory, and the binary
// needs its libraries BESIDE it — so the tree is extracted with that level
// stripped, not just the executable.
func TestExtractTarGzStripsTopLevelAndKeepsTheExecutableBit(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	write := func(name string, mode int64, body string) {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("piper/piper", 0o755, "#!/bin/sh\n")
	write("piper/libespeak-ng.so", 0o644, "lib")
	write("piper/espeak-ng-data/en_dict", 0o644, "data")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := extractTarGz(archive, dest); err != nil {
		t.Fatalf("extract: %v", err)
	}

	// Top level stripped: piper/piper lands at <dest>/piper, not <dest>/piper/piper.
	bin := filepath.Join(dest, "piper")
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("binary not extracted to the top level: %v", err)
	}
	// Windows has no executable bit — it runs piper.exe by extension — and
	// os.Stat there reports 0666 for every writable file, so this asserts the
	// property only where it exists.
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		t.Error("the executable bit was lost — piper would be unusable")
	}
	// The libraries must land beside it, or the binary starts and cannot phonemize.
	for _, sibling := range []string{"libespeak-ng.so", "espeak-ng-data/en_dict"} {
		if _, err := os.Stat(filepath.Join(dest, sibling)); err != nil {
			t.Errorf("%s must be extracted beside the binary: %v", sibling, err)
		}
	}
}

// fileSHA256 is what stands between a network download and exec. It must hash
// the actual bytes.
func TestFileSHA256MatchesKnownDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	// SHA-256("abc"), a published test vector.
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	got, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, want) {
		t.Errorf("fileSHA256 = %s, want %s", got, want)
	}
}
