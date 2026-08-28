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

// archiveRel turns an archive entry name into a path that is safe to use
// relative to the destination root, or reports that it is not usable.
//
// This is the zip-slip / tar-slip guard. An entry named
// "../../.ssh/authorized_keys" would otherwise be written wherever it points,
// and this archive is fetched over the network.
//
// Two things are checked, and neither is redundant:
//
//   - filepath.IsLocal rejects anything absolute, anything rooted, anything
//     that walks out with "..", and (on Windows) reserved device names like
//     NUL and COM1. A hand-rolled prefix comparison gets the Windows cases
//     wrong; this is the standard library's own answer.
//   - Entry names are always slash-separated, whatever the host is, so they
//     are converted before any path logic touches them. Skipping this makes
//     "..\\evil" look like an ordinary filename on Linux and a traversal on
//     Windows.
//
// The caller then writes through an *os.Root, which enforces the same
// property at the syscall layer — see extractInto.
func archiveRel(name string) (string, bool) {
	rel := filepath.FromSlash(stripTop(name))
	if rel == "" || !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.Clean(rel), true
}

// stripTop removes the archive's single top-level directory.
func stripTop(name string) string {
	if i := strings.Index(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return ""
}

// extractInto opens dir as a confined root.
//
// os.Root is the part that makes this genuinely safe rather than merely
// checked. A string comparison can only judge the name an archive declares;
// it cannot know what the filesystem will do with it. An archive that first
// creates a symlink "x" -> "/etc" and then writes "x/passwd" passes every
// name-based test — both entries look perfectly local — and still lands in
// /etc. Every operation through a Root is resolved inside the root by the
// kernel, so that archive fails instead, and so does anything racing the
// check by swapping a directory for a symlink after it passed.
func extractInto(dir string) (*os.Root, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s for extraction: %w", dir, err)
	}
	return root, nil
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

	root, err := extractInto(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("archive read failed: %w", err)
		}
		rel, ok := archiveRel(hdr.Name)
		if !ok {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(rel, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeExtracted(root, rel, tr, os.FileMode(hdr.Mode)); err != nil {
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

	root, err := extractInto(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	for _, entry := range zr.File {
		rel, ok := archiveRel(entry.Name)
		if !ok {
			continue
		}
		if entry.FileInfo().IsDir() {
			if err := root.MkdirAll(rel, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return err
		}
		err = writeExtracted(root, rel, rc, entry.Mode())
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
func writeExtracted(root *os.Root, rel string, src io.Reader, mode os.FileMode) error {
	if dir := filepath.Dir(rel); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	perm := os.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	// Every operation here is relative to root, which the kernel confines to
	// the destination directory — rel cannot reach outside it even if the
	// filesystem underneath changes while this runs.
	out, err := root.OpenFile(rel, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
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
