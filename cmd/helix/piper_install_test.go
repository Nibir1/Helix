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
	"strings"
	"testing"
)

// An archive entry that escapes the destination must be refused. The checksum
// makes a hostile archive unlikely; it does not make this check pointless, and
// a path guard costs nothing.
func TestSafeJoinRefusesEscapingEntries(t *testing.T) {
	dir := t.TempDir()
	for _, evil := range []string{
		"../outside.txt",
		"../../etc/passwd",
		"nested/../../escape",
	} {
		if _, err := safeJoin(dir, evil); err == nil {
			t.Errorf("entry %q escapes %s and must be refused", evil, dir)
		}
	}
	for _, ok := range []string{"piper", "espeak-ng-data/en_dict", "./piper"} {
		if _, err := safeJoin(dir, ok); err != nil {
			t.Errorf("entry %q is inside the destination but was refused: %v", ok, err)
		}
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
	if info.Mode()&0o111 == 0 {
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
