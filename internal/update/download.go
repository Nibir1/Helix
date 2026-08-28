// internal/update/download.go
//
// Purpose: fetching a release archive, proving it is the file the release says
// it is, and getting the binary out of it.
package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// maxDownload bounds a release archive.
//
// A Helix archive is a few tens of megabytes. The cap is not about disk, it is
// about an unbounded read from a host that is only as trustworthy as DNS: a
// download with no ceiling is a way to fill a small board's filesystem.
const maxDownload = 256 << 20 // 256 MiB

// Fetch downloads a candidate and returns the path to a verified Helix binary.
//
// The returned file lives in dir and is the caller's to install or remove. The
// order of operations is the security property: download, hash, COMPARE, and
// only then extract. Extracting first and checking afterwards would mean
// unpacking an unverified archive, which is where archive parsers get exploited.
func Fetch(ctx context.Context, c *Candidate, dir string) (string, error) {
	if c.Source != SourceGitHub {
		return "", fmt.Errorf("fetch is only for release candidates")
	}
	if c.SHA256 == "" {
		// Belt and braces: checkGitHub already refuses a release with no
		// checksum, and this makes it impossible to reach the network by
		// constructing a Candidate some other way.
		return "", fmt.Errorf("refusing to download a candidate with no checksum")
	}
	u, err := url.Parse(c.URL)
	if err != nil {
		return "", fmt.Errorf("bad download URL: %w", err)
	}
	if err := requireAllowedURL(u); err != nil {
		return "", err
	}

	archive := filepath.Join(dir, "download-"+pathBase(u.Path))
	sum, err := downloadTo(ctx, u.String(), archive)
	if err != nil {
		return "", err
	}
	if err := verifyChecksum(sum, c.SHA256); err != nil {
		_ = os.Remove(archive)
		return "", err
	}

	binary, err := extractHelix(archive, dir)
	_ = os.Remove(archive)
	if err != nil {
		return "", err
	}

	info, err := Inspect(binary)
	if err != nil {
		_ = os.Remove(binary)
		return "", fmt.Errorf("the downloaded file is not a Helix binary: %w", err)
	}
	if err := info.RequirePlatform(runtime.GOOS, runtime.GOARCH); err != nil {
		_ = os.Remove(binary)
		return "", fmt.Errorf("wrong build: %w", err)
	}
	return binary, nil
}

// verifyChecksum compares what was downloaded against what the release said.
//
// Its own function because it is the single most important comparison in the
// codebase and everything around it needs a network. A constant-time compare is
// deliberately NOT used: this is an integrity check against a public manifest,
// not a secret, and there is no oracle to leak — reaching for one here would be
// cargo cult rather than a control.
func verifyChecksum(got, want string) error {
	want = strings.ToLower(strings.TrimSpace(want))
	got = strings.ToLower(strings.TrimSpace(got))
	if want == "" {
		return fmt.Errorf("refusing to install a download with no expected checksum")
	}
	if len(want) != 64 {
		return fmt.Errorf("refusing to install against a malformed checksum")
	}
	if got != want {
		return fmt.Errorf(
			"checksum mismatch — the download does not match the release manifest "+
				"(expected %s…, got %s…)", want[:16], got[:16])
	}
	return nil
}

// downloadTo streams a URL to a file and returns its SHA-256.
//
// Hashed while streaming rather than by re-reading the file afterwards: one
// pass, and the bytes that were written are provably the bytes that were
// hashed.
func downloadTo(ctx context.Context, rawURL, dest string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "helix-updater")
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxDownload+1))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return "", closeErr
	}
	if n > maxDownload {
		_ = os.Remove(dest)
		return "", fmt.Errorf("download exceeded the %d-byte limit", maxDownload)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractHelix pulls the helix binary out of a verified archive.
//
// A raw binary asset is returned as-is. Archives are walked for an entry NAMED
// helix — never simply "the biggest file" or "the first executable", because
// the name is the only thing that says which member is the program.
func extractHelix(archive, dir string) (string, error) {
	lower := strings.ToLower(archive)
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(archive, dir)
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(archive, dir)
	default:
		// A bare binary asset. Copy rather than rename so the caller's cleanup
		// of the archive path cannot take the binary with it.
		out := filepath.Join(dir, "helix-new")
		if err := copyFile(archive, out, 0o700); err != nil {
			return "", err
		}
		return out, nil
	}
}

func extractTarGz(archive, dir string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("unreadable archive: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("unreadable archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || !isHelixEntry(hdr.Name) {
			continue
		}
		out := filepath.Join(dir, "helix-new")
		if err := writeCapped(tr, out); err != nil {
			return "", err
		}
		return out, nil
	}
	return "", fmt.Errorf("the archive contains no helix binary")
}

func extractZip(archive, dir string) (string, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return "", fmt.Errorf("unreadable archive: %w", err)
	}
	defer func() { _ = zr.Close() }()

	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() || !isHelixEntry(entry.Name) {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return "", err
		}
		out := filepath.Join(dir, "helix-new")
		err = writeCapped(rc, out)
		_ = rc.Close()
		if err != nil {
			return "", err
		}
		return out, nil
	}
	return "", fmt.Errorf("the archive contains no helix binary")
}

// isHelixEntry matches the program inside an archive by name.
//
// The base name only, so a crafted entry called "../../etc/cron.d/helix" cannot
// be selected — the classic archive path-traversal, prevented by never using
// the entry's path for anything. The output filename is always ours.
func isHelixEntry(name string) bool {
	base := strings.ToLower(pathBase(strings.ReplaceAll(name, `\`, "/")))
	return base == "helix" || base == "helix.exe"
}

// writeCapped copies at most maxDownload bytes into a new 0700 file.
func writeCapped(r io.Reader, dest string) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxDownload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	if n > maxDownload {
		_ = os.Remove(dest)
		return fmt.Errorf("archive member exceeded the %d-byte limit", maxDownload)
	}
	return nil
}

func copyFile(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}
