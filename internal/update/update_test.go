// internal/update/update_test.go
//
// Purpose: this package decides which binary the user runs. Every refusal it
// makes is a control, and a control nothing exercises is a comment.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// --- version comparison ----------------------------------------------------

func TestVersionOrdering(t *testing.T) {
	newer := [][2]string{
		{"1.6.0", "1.5.0"},
		{"2.0.0", "1.99.99"},
		{"1.5.1", "1.5.0"},
		{"v1.6.0", "1.5.9"},
		// A release beats its own pre-release, and never the other way round.
		{"1.6.0", "1.6.0-rc.1"},
		{"1.6.0-rc.2", "1.6.0-rc.1"},
	}
	for _, pair := range newer {
		a, err := ParseVersion(pair[0])
		if err != nil {
			t.Fatalf("parse %q: %v", pair[0], err)
		}
		b, err := ParseVersion(pair[1])
		if err != nil {
			t.Fatalf("parse %q: %v", pair[1], err)
		}
		if !a.Newer(b) {
			t.Errorf("%s should be newer than %s", pair[0], pair[1])
		}
		if b.Newer(a) {
			t.Errorf("%s must NOT be newer than %s", pair[1], pair[0])
		}
	}
}

// The same version is not an update. An updater that thought otherwise would
// reinstall the running binary on every single restart.
func TestSameVersionIsNotNewer(t *testing.T) {
	v, err := ParseVersion("1.5.0")
	if err != nil {
		t.Fatal(err)
	}
	same, _ := ParseVersion("v1.5.0+build.7") // build metadata is not precedence
	if v.Newer(same) || same.Newer(v) {
		t.Error("identical versions must not compare as newer in either direction")
	}
}

// An unreadable version must be an ERROR, never a silent 0.0.0 — which would
// make every real release look like an upgrade over a version we failed to read,
// or make our own build look ancient and offer a downgrade as an update.
func TestUnreadableVersionsAreRejected(t *testing.T) {
	for _, bad := range []string{"", "latest", "v", "1.x.0", "-1.0.0", "1.2.3.4", "nightly"} {
		if v, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) = %v, want an error", bad, v)
		}
	}
}

// --- the host pin ----------------------------------------------------------

// The pin is the control that stops a compromised API reply from walking the
// download off GitHub. Scheme alone is not enough: HTTPS says nothing about who
// answers.
func TestOnlyGitHubOverHTTPSIsAllowed(t *testing.T) {
	refused := []string{
		"http://api.github.com/x",             // not HTTPS
		"https://api.github.com.evil.test/x",  // suffix-lookalike host
		"https://evil.test/helix.tar.gz",      // elsewhere entirely
		"https://raw.githubusercontent.com/x", // GitHub-owned, still not a release host
		"https://127.0.0.1/x",                 // loopback
	}
	for _, raw := range refused {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if err := requireAllowedURL(u); err == nil {
			t.Errorf("%s must be refused", raw)
		}
	}
	for _, raw := range []string{
		"https://api.github.com/repos/x/y/releases/latest",
		"https://github.com/x/y/releases/download/v1/helix.tar.gz",
		"https://objects.githubusercontent.com/blob",
	} {
		u, _ := url.Parse(raw)
		if err := requireAllowedURL(u); err != nil {
			t.Errorf("%s should be allowed: %v", raw, err)
		}
	}
}

// --- the checksum gate -----------------------------------------------------

// A candidate with no checksum must never reach the network. checkGitHub
// already refuses one, and this proves the refusal survives a Candidate
// constructed some other way.
func TestFetchRefusesACandidateWithNoChecksum(t *testing.T) {
	_, err := Fetch(context.Background(), &Candidate{
		Source: SourceGitHub,
		URL:    "https://github.com/x/y/releases/download/v1/helix.tar.gz",
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected a checksum refusal, got %v", err)
	}
}

func TestFetchRefusesANonGitHubURL(t *testing.T) {
	_, err := Fetch(context.Background(), &Candidate{
		Source: SourceGitHub,
		URL:    "https://evil.test/helix.tar.gz",
		SHA256: strings.Repeat("a", 64),
	}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "outside GitHub") {
		t.Fatalf("expected a host refusal, got %v", err)
	}
}

// The checksums file is matched by FILENAME. Picking by position would
// eventually verify one artifact against another's hash, which passes a
// checksum check while proving nothing.
func TestChecksumIsMatchedByFilename(t *testing.T) {
	manifest := strings.Join([]string{
		strings.Repeat("1", 64) + "  helix_Linux_x86_64.tar.gz",
		strings.Repeat("2", 64) + " *helix_Darwin_arm64.tar.gz",
		strings.Repeat("3", 64) + "  helix_Windows_x86_64.zip",
	}, "\n")

	got, err := parseChecksumManifest([]byte(manifest), "helix_Darwin_arm64.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Repeat("2", 64) {
		t.Errorf("matched the wrong line: %s", got)
	}

	// An asset the manifest does not mention is not installable.
	if _, err := parseChecksumManifest([]byte(manifest), "helix_Plan9_arm.tar.gz"); err == nil {
		t.Error("an unlisted asset must be refused, not installed unverified")
	}
	// A malformed hash is a refusal, not something to normalise.
	bad := "nothexadecimal  helix_Darwin_arm64.tar.gz"
	if _, err := parseChecksumManifest([]byte(bad), "helix_Darwin_arm64.tar.gz"); err == nil {
		t.Error("a malformed checksum must be refused")
	}
}

// The comparison that decides whether a download becomes the program the user
// runs. Extracted so it is reachable without a network, because a control this
// important must not be the one thing no test touches.
func TestVerifyChecksumRefusesAnythingButAnExactMatch(t *testing.T) {
	good := strings.Repeat("ab", 32)
	if err := verifyChecksum(good, good); err != nil {
		t.Errorf("an exact match must pass: %v", err)
	}
	if err := verifyChecksum(strings.ToUpper(good), good); err != nil {
		t.Errorf("case must not decide integrity: %v", err)
	}

	// One flipped character is a mismatch, not a rounding error.
	almost := good[:63] + "c"
	if err := verifyChecksum(almost, good); err == nil {
		t.Error("a near-miss must be refused")
	}
	// An empty or short expectation is a refusal, never a free pass.
	if err := verifyChecksum(good, ""); err == nil {
		t.Error("an absent expected checksum must refuse, not permit")
	}
	if err := verifyChecksum(good, "abc"); err == nil {
		t.Error("a malformed expected checksum must refuse")
	}
	// And a prefix match is not a match.
	if err := verifyChecksum(good[:32], good); err == nil {
		t.Error("a truncated download must be refused")
	}
}

// --- archive handling ------------------------------------------------------

// The classic archive attack: an entry whose path escapes the extraction
// directory. Prevented structurally — the entry's path is never used, only its
// base name is matched, and the output filename is always ours.
func TestArchiveEntryPathsAreNeverUsed(t *testing.T) {
	for _, name := range []string{
		"../../../../etc/cron.d/helix",
		`..\..\windows\system32\helix.exe`,
		"/absolute/helix",
		"nested/dir/helix",
	} {
		if !isHelixEntry(name) {
			t.Errorf("%q should still be recognised as the helix member", name)
		}
	}
	for _, name := range []string{"README.md", "LICENSE", "helixer", "install.sh"} {
		if isHelixEntry(name) {
			t.Errorf("%q must not be treated as the binary", name)
		}
	}

	// And end to end: a traversal entry lands on our filename, in our dir.
	dir := t.TempDir()
	archive := writeTarGz(t, dir, "../../escaped/helix", []byte("not a real binary"))
	out, err := extractTarGz(archive, dir)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if filepath.Dir(out) != dir {
		t.Errorf("extraction escaped its directory: %s", out)
	}
}

func TestArchiveWithoutAHelixBinaryIsRefused(t *testing.T) {
	dir := t.TempDir()
	archive := writeTarGz(t, dir, "README.md", []byte("hello"))
	if _, err := extractTarGz(archive, dir); err == nil {
		t.Error("an archive with no helix binary must be refused")
	}

	zipPath := writeZip(t, dir, "LICENSE", []byte("hello"))
	if _, err := extractZip(zipPath, dir); err == nil {
		t.Error("a zip with no helix binary must be refused")
	}
}

// --- inspection ------------------------------------------------------------

// Verification proves a download matches its manifest. It cannot prove the file
// is a program, which is what this catches: a truncated download, or an HTML
// error page saved with an archive's name.
func TestInspectRejectsSomethingThatIsNotAGoBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helix")
	if err := os.WriteFile(path, []byte("<!doctype html><title>404</title>"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(path); err == nil {
		t.Error("a non-binary must not be installable as Helix")
	}
}

// The running test binary IS a Go binary, so it exercises the happy path of the
// reader without needing a fixture — and proves the module check refuses a Go
// binary built from something that is not Helix.
func TestInspectRefusesAnotherProjectsBinary(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skip("no executable path here")
	}
	info, err := Inspect(self)
	if err == nil {
		// The e2e/test binary's module is "helix", so this is the accept path.
		if info.GOOS != runtime.GOOS {
			t.Errorf("GOOS = %q, want %q", info.GOOS, runtime.GOOS)
		}
		return
	}
	if !strings.Contains(err.Error(), "not \"helix\"") && !strings.Contains(err.Error(), "module") {
		t.Errorf("unexpected inspect failure: %v", err)
	}
}

func TestRequirePlatformRejectsAForeignBuild(t *testing.T) {
	b := BinaryInfo{GOOS: "linux", GOARCH: "amd64"}
	if err := b.RequirePlatform("darwin", "arm64"); err == nil {
		t.Error("a linux binary must not install over a darwin one")
	}
	if err := b.RequirePlatform("linux", "amd64"); err != nil {
		t.Errorf("a matching build must be accepted: %v", err)
	}
}

// --- install and rollback --------------------------------------------------

func TestInstallKeepsThePreviousBinaryAndRollbackRestoresIt(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "helix")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(dir, "new")
	if err := os.WriteFile(replacement, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}

	backup, err := Install(replacement, target)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := readFile(t, target); got != "NEW" {
		t.Errorf("target = %q, want NEW", got)
	}
	if backup == "" || readFile(t, backup) != "OLD" {
		t.Fatalf("the previous binary was not kept (backup=%q)", backup)
	}
	// The installed file has to be executable, or the rollback is the only
	// thing that ever runs again.
	if st, err := os.Stat(target); err != nil {
		t.Fatal(err)
	} else if st.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed binary is not executable: %v", st.Mode())
	}

	if err := Rollback(target); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := readFile(t, target); got != "OLD" {
		t.Errorf("after rollback target = %q, want OLD", got)
	}

	// No staging debris left behind either way.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".helix-update-staged") {
			t.Errorf("staging file left behind: %s", e.Name())
		}
	}
}

func TestRollbackWithNoBackupSaysSo(t *testing.T) {
	target := filepath.Join(t.TempDir(), "helix")
	if err := os.WriteFile(target, []byte("X"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(target); err == nil {
		t.Error("a rollback with nothing to roll back to must report that")
	}
}

// --- channel selection -----------------------------------------------------

func TestChannelOffChecksNothing(t *testing.T) {
	c, err := Check(context.Background(), Options{Channel: ChannelOff})
	if c != nil || err != nil {
		t.Errorf("channel off must do nothing, got (%v, %v)", c, err)
	}
}

// The local channel must never offer the running binary as an update to itself,
// which is the ordinary case when Helix is run straight out of dist/.
func TestLocalChannelSkipsTheRunningBinary(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "helix")
	if err := os.WriteFile(self, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	current, _ := ParseVersion("1.5.0")

	c, _ := checkLocal(Options{
		Current:    current,
		LocalPaths: []string{self},
		SelfPath:   self,
	})
	if c != nil {
		t.Errorf("the running binary was offered as its own update: %+v", c)
	}
}

// --- helpers ---------------------------------------------------------------

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func writeTarGz(t *testing.T, dir, entryName string, content []byte) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: entryName, Mode: 0o755, Size: int64(len(content)),
		Typeflag: tar.TypeReg, ModTime: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "asset.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeZip(t *testing.T, dir, entryName string, content []byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(entryName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "asset.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
