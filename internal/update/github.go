// internal/update/github.go
//
// Purpose: resolving the latest published release, and refusing to leave GitHub
// while doing it.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// allowedHosts are the only hosts this package will talk to.
//
// A pin rather than a scheme check. "It must be HTTPS" says nothing about WHO
// answers, and the entire attack this guards against is a response — an API
// reply, a redirect — that points the download somewhere else. The release
// asset host has changed once already in GitHub's history, which is why the
// download hosts are listed explicitly rather than derived from the API reply.
var allowedHosts = map[string]bool{
	"api.github.com":                       true,
	"github.com":                           true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
}

// httpClient is the client every request in this package uses.
//
// Redirects are inspected rather than followed blindly: CheckRedirect refuses a
// hop to any host outside the pin, so an API response cannot walk the download
// off GitHub. Ten hops is Go's own default limit, kept so a redirect loop ends.
var httpClient = &http.Client{
	Timeout: 60 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		if err := requireAllowedURL(req.URL); err != nil {
			return fmt.Errorf("refused redirect: %w", err)
		}
		return nil
	},
}

// requireAllowedURL enforces HTTPS and the host pin.
func requireAllowedURL(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("refusing a non-HTTPS update URL (%s)", u.Scheme)
	}
	if !allowedHosts[strings.ToLower(u.Hostname())] {
		return fmt.Errorf("refusing an update URL outside GitHub: %s", u.Hostname())
	}
	return nil
}

// maxAPIResponse bounds the release JSON. A release body embeds the whole of
// RELEASE_NOTES.md, so this is generous — but unbounded is not a size, and the
// reply is attacker-shaped input the moment DNS is.
const maxAPIResponse = 4 << 20 // 4 MiB

// ghRelease is the slice of GitHub's release JSON this needs.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// checkGitHub resolves the latest release and picks this machine's asset.
func checkGitHub(ctx context.Context, opts Options) (*Candidate, error) {
	repo := strings.TrimSpace(opts.Repo)
	if repo == "" {
		repo = DefaultRepo
	}
	if strings.Count(repo, "/") != 1 || strings.ContainsAny(repo, " ?#") {
		return nil, fmt.Errorf("update repo must be \"owner/name\", got %q", repo)
	}

	rel, err := latestRelease(ctx, repo)
	if err != nil {
		return nil, err
	}
	if rel.Draft {
		return nil, nil // a draft is not published
	}

	version, err := ParseVersion(rel.TagName)
	if err != nil {
		return nil, fmt.Errorf("release tag %q is not a version: %w", rel.TagName, err)
	}
	if !version.Newer(opts.Current) {
		return nil, nil
	}

	osHint, archHint := platformAssetHints()
	var assetURL, checksumURL string
	var size int64
	for _, a := range rel.Assets {
		name := a.Name
		switch {
		case isChecksumAsset(name):
			checksumURL = a.URL
		case strings.Contains(name, osHint) && strings.Contains(name, archHint) &&
			isArchiveOrBinary(name):
			assetURL, size = a.URL, a.Size
		}
	}
	if assetURL == "" {
		return nil, errNoAsset
	}
	if checksumURL == "" {
		// Not a warning. Without the checksums file there is nothing to verify
		// the download against, and a release that cannot be verified is not
		// installable — saying so is the control.
		return nil, fmt.Errorf(
			"release %s publishes no checksums file; refusing to install an unverifiable download",
			rel.TagName)
	}

	sum, err := fetchChecksum(ctx, checksumURL, assetURL)
	if err != nil {
		return nil, err
	}

	notes := strings.TrimSpace(rel.Name)
	if notes == "" {
		notes = rel.TagName
	}
	if rel.Prerelease {
		notes += " (pre-release)"
	}

	return &Candidate{
		Source:       SourceGitHub,
		Version:      version,
		Tag:          rel.TagName,
		Notes:        notes,
		URL:          assetURL,
		SHA256:       sum,
		Size:         size,
		Published:    rel.PublishedAt,
		VersionKnown: true,
	}, nil
}

// latestRelease fetches the newest non-draft release.
func latestRelease(ctx context.Context, repo string) (*ghRelease, error) {
	endpoint := "https://api.github.com/repos/" + repo + "/releases/latest"
	body, err := getBounded(ctx, endpoint, maxAPIResponse, map[string]string{
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	})
	if err != nil {
		return nil, err
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("unreadable release data: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("no published release found for %s", repo)
	}
	return &rel, nil
}

// fetchChecksum reads the release's checksums file and returns the SHA-256 of
// the asset we intend to download.
//
// Matching on the asset's FILENAME rather than its position: goreleaser writes
// one line per artifact in an order nothing guarantees, and picking by index
// would eventually verify one file against another's hash — which passes a
// checksum check while proving nothing.
func fetchChecksum(ctx context.Context, checksumURL, assetURL string) (string, error) {
	body, err := getBounded(ctx, checksumURL, 1<<20, nil)
	if err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}
	u, err := url.Parse(assetURL)
	if err != nil {
		return "", fmt.Errorf("bad asset URL: %w", err)
	}
	return parseChecksumManifest(body, pathBase(u.Path))
}

// parseChecksumManifest finds one asset's SHA-256 in a checksums file.
//
// Split from the fetch so the matching rule is testable without a network,
// because the rule IS the control: every refusal below is a case where the
// download could not be verified, and each of them must stay a refusal rather
// than degrade into "install it anyway".
func parseChecksumManifest(body []byte, want string) (string, error) {
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		// The GNU coreutils format marks binary mode with a leading "*".
		name := strings.TrimPrefix(fields[1], "*")
		if pathBase(name) != want {
			continue
		}
		sum := strings.ToLower(fields[0])
		if len(sum) != 64 || strings.Trim(sum, "0123456789abcdef") != "" {
			return "", fmt.Errorf("checksums file has a malformed entry for %s", want)
		}
		return sum, nil
	}
	return "", fmt.Errorf("checksums file has no entry for %s; refusing to install it", want)
}

// getBounded performs a pinned HTTPS GET with a hard size limit.
func getBounded(ctx context.Context, rawURL string, limit int64, headers map[string]string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("bad URL: %w", err)
	}
	if err := requireAllowedURL(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "helix-updater")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("not found (%s)", u.Path)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned %s", resp.Status)
	}
	// LimitReader with one spare byte, so an oversized body is DETECTED rather
	// than silently truncated into something that parses.
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response larger than the %d-byte limit", limit)
	}
	return body, nil
}

// isChecksumAsset reports whether an asset name is the checksums manifest.
func isChecksumAsset(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "checksum") && strings.HasSuffix(lower, ".txt")
}

// isArchiveOrBinary excludes the signature, certificate and SBOM artifacts that
// sit beside the archive and match the same platform hints.
func isArchiveOrBinary(name string) bool {
	lower := strings.ToLower(name)
	for _, skip := range []string{".sig", ".pem", ".sbom", ".json", ".txt", ".sha256"} {
		if strings.HasSuffix(lower, skip) {
			return false
		}
	}
	return strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".zip") ||
		!strings.Contains(pathBase(lower), ".")
}

// pathBase is filepath.Base for slash-separated URL paths, on every OS.
func pathBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
