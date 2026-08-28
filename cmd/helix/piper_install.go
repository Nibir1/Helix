// cmd/helix/piper_install.go
// Purpose: download, VERIFY and extract Piper's standalone binary.
//
// In Go rather than as a shell command, for a reason that is not stylistic:
// runVisibleCommand splits its input with strings.Fields and execs it directly.
// There is no shell. Every existing InstallCmd is one executable with flat
// arguments, so a pipeline of `curl … && shasum -c … && tar -xz …` would have
// been split on spaces and handed to mkdir as arguments. It could never have
// worked, and it would have failed at the worst possible moment — in front of a
// new user, during setup.
//
// Doing it here also makes the integrity check real. Threat V8 says Helix's
// sidecar installers "pin versions and publish checksums", and the Ollama
// installer already refuses to run a script whose SHA-256 does not match. This
// is a ~26 MB executable payload fetched over the network and then run; a
// download piped into tar unverified would have made that claim false the
// moment it shipped.
package main

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
	"os"
	"path/filepath"
	"strings"
	"time"

	"helix/internal/commands"
	"helix/internal/shell"
	"helix/internal/speech"
)

// piperDownloadTimeout bounds the fetch. Generous: ~26 MB over a slow edge
// connection is minutes, and a Pi on wifi is exactly the machine this serves.
const piperDownloadTimeout = 20 * time.Minute

// installPiperBinary fetches the standalone piper into ~/.helix/piper.
//
// Returns the path to the executable. The archive wraps everything in a
// top-level piper/ directory and the binary needs its espeak-ng data and shared
// libraries BESIDE it, so the whole tree is extracted, not just the executable —
// extracting only the binary produces one that starts and cannot phonemize.
func installPiperBinary() (string, error) {
	asset, ok := speech.PiperReleaseAsset()
	if !ok {
		return "", fmt.Errorf("no standalone piper for this platform")
	}
	want, ok := speech.PiperReleaseSHA256()
	if !ok {
		return "", fmt.Errorf("no pinned checksum for %s", asset)
	}

	dir := helixPath("piper")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", dir, err)
	}

	url := fmt.Sprintf("https://github.com/rhasspy/piper/releases/download/%s/%s",
		speech.PiperReleaseVersion, asset)

	archive := filepath.Join(dir, asset)
	if err := downloadFile(url, archive); err != nil {
		_ = os.Remove(archive)
		return "", err
	}
	// Always remove the archive: a verified-bad file is not a partial download
	// to resume, and a verified-good one has already been extracted.
	defer func() { _ = os.Remove(archive) }()

	got, err := fileSHA256(archive)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(got, want) {
		return "", fmt.Errorf(
			"checksum mismatch for %s\n  got  %s\n  want %s\nrefusing to extract it",
			asset, got, want)
	}

	if strings.HasSuffix(asset, ".zip") {
		err = extractZip(archive, dir)
	} else {
		err = extractTarGz(archive, dir)
	}
	if err != nil {
		return "", err
	}

	bin, err := speech.FindPiperBinary()
	if err != nil {
		return "", fmt.Errorf("extracted %s but found no piper executable in %s", asset, dir)
	}
	return bin, nil
}

// downloadFile streams a URL to disk with a visible progress bar.
func downloadFile(url, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), piperDownloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d from %s", resp.StatusCode, url)
	}

	out, err := os.Create(dest) //nolint:gosec // path is built from helixPath
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("download interrupted: %w", err)
	}
	return nil
}

// fileSHA256 hashes a file by streaming it.
//
// Streamed rather than read whole: these archives are ~26 MB, and on a Pi Zero
// holding that in memory alongside everything else is not free.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path is built from helixPath
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// safeJoin resolves an archive entry against a destination directory and
// refuses anything that escapes it.
//
// This is the zip-slip / tar-slip guard. An archive entry named
// "../../.ssh/authorized_keys" would otherwise be written wherever it points,
// and this archive is fetched over the network. The checksum makes that
// unlikely; it does not make it impossible to get wrong later, and a path check
// costs nothing.
func safeJoin(dir, name string) (string, error) {
	clean := filepath.Clean(filepath.Join(dir, name))
	if clean != dir && !strings.HasPrefix(clean, dir+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the destination", name)
	}
	return clean, nil
}

// stripTop removes the archive's single top-level directory.
func stripTop(name string) string {
	if i := strings.Index(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return ""
}

func extractTarGz(archive, dir string) error {
	f, err := os.Open(archive) //nolint:gosec // path is built from helixPath
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a gzip archive: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("archive read failed: %w", err)
		}
		rel := stripTop(hdr.Name)
		if rel == "" {
			continue
		}
		path, err := safeJoin(dir, rel)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeExtracted(path, tr, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		}
	}
}

func extractZip(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("not a zip archive: %w", err)
	}
	defer func() { _ = zr.Close() }()

	for _, entry := range zr.File {
		rel := stripTop(entry.Name)
		if rel == "" {
			continue
		}
		path, err := safeJoin(dir, rel)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return err
		}
		err = writeExtracted(path, rc, entry.Mode())
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// writeExtracted creates one file, preserving the executable bit.
//
// The bit matters: piper and espeak-ng arrive executable and are useless
// without it, and Go's archive readers do not restore permissions for you.
func writeExtracted(path string, src io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm) //nolint:gosec // path checked by safeJoin
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	//nolint:gosec // archive is checksum-pinned; entries are bounded by it
	if _, err := io.Copy(out, src); err != nil {
		return err
	}
	return out.Chmod(perm)
}

// offerPiperBinary walks the download with consent, matching the sidecar rule:
// nothing installs implicitly, and the user sees what will happen first.
func offerPiperBinary() (string, bool) {
	asset, ok := speech.PiperReleaseAsset()
	if !ok {
		return "", false
	}
	sum, _ := speech.PiperReleaseSHA256()

	fmt.Println()
	fmt.Println(shell.PanelTitle("piper without python"))
	for _, l := range shell.PanelWrap(
		"Piper publishes a standalone binary, so the local voice needs no Python "+
			"interpreter and no HTTP server. Helix holds it open with the voice "+
			"model resident, which is faster than the server it replaces.",
		shell.Muted) {
		fmt.Println(l)
	}
	w := shell.KVWidth("DOWNLOAD", "VERIFY", "INSTALL TO")
	fmt.Println(shell.PanelGap())
	fmt.Println(shell.KV("DOWNLOAD", shell.Value(asset)+
		shell.Muted("  from rhasspy/piper "+speech.PiperReleaseVersion), w))
	fmt.Println(shell.KV("VERIFY", shell.Muted("SHA-256 "+shortSum(sum)+
		"  ·  refused on mismatch"), w))
	fmt.Println(shell.KV("INSTALL TO", shell.Muted(helixPath("piper")), w))
	fmt.Println(shell.PanelEnd())

	if !commands.AskForConfirmation("Download it now?") {
		return "", false
	}

	bin, err := installPiperBinary()
	if err != nil {
		fmt.Println()
		for _, l := range shell.PanelWrap("Could not install piper: "+err.Error(), shell.Muted) {
			fmt.Println(l)
		}
		return "", false
	}
	uiOK("installed", bin)
	return bin, true
}

// shortSum abbreviates a digest for display without hiding that it is checked.
func shortSum(sum string) string {
	if len(sum) <= 16 {
		return sum
	}
	return sum[:16] + "…"
}
