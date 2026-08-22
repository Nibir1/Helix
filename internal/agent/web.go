// internal/agent/web.go
//
// Purpose: the `web` planner tool — read-only network retrieval.
//
// Before this file the planner's tool set was closed at
// response|shell|git|package|recon, so a model asked "who is the current
// president" had one honest option: say it cannot search. It has training data
// with a cutoff and no way to look anything up, which makes every time-sensitive
// question either a refusal or a confidently stale answer.
//
// Two actions, both GET-only:
//
//	search → DuckDuckGo's Lite endpoint, regex-parsed into the top few results.
//	fetch  → one URL, HTML crudely stripped to text, other text types raw.
//
// Risk classification (docs/threat_model_voice.md's framing, ADR-016 tiers):
// a web step sits at the SAME tier as a read-only shell command. It cannot
// write a file, install anything, or execute code; the only capability it adds
// is outbound HTTP GET. The residual risks and their controls:
//
//   - SSRF / metadata theft. The URL is model-chosen, so `fetch` is exactly the
//     shape of an SSRF gadget. guardPublicHTTPURL refuses non-http(s) schemes,
//     credentials in the URL, and any host that is loopback, link-local
//     (169.254.169.254 — the cloud metadata address), private, or otherwise
//     non-public. Helix will not dial the machine it is running on.
//   - Exfiltration through a URL. A GET carries whatever the model puts in the
//     query string. This is the same exposure `curl` under the shell tool has
//     always had, and it is covered the same way: the Instruction Firewall's
//     critic reviews any plan URL the user did not mention (see firewall.go).
//   - Prompt injection from fetched pages. Retrieved text is fed back to the
//     planner through the harness's data-only fence, which already sanitizes
//     fence-breakout characters (harness.go, sanitizeOutput). Page content is
//     data with zero authority, like RAG and session memory.
//   - Runaway responses. Both actions are byte-capped and share one 15s budget.
//
// No new module dependencies: stdlib net/http plus regex parsing.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/commands"
)

// Bounds for one web step. Small on purpose: the output is re-sent to the
// planner on every harness iteration, so these cap recurring token cost, not
// just one response.
const (
	// webTimeout is the whole-step budget (dial + read).
	webTimeout = 15 * time.Second

	// webSearchMaxResults is how many search hits are kept.
	webSearchMaxResults = 5

	// webSearchMaxChars caps rendered search results.
	webSearchMaxChars = 4000

	// webFetchMaxBytes caps a fetched page before conversion to text.
	webFetchMaxBytes = 8 << 10

	// webUserAgent identifies Helix honestly. Not a browser string: pretending
	// to be Chrome to dodge bot detection is not something a local shell tool
	// should do on the user's behalf.
	webUserAgent = "Helix/1.0 (+https://github.com/nahasat/helix)"
)

// webSearchEndpoint is DuckDuckGo's HTML-only Lite endpoint: no JavaScript, no
// API key, and a markup shape stable enough to parse with a regex. A package
// var so tests can point it at an httptest server.
var webSearchEndpoint = "https://lite.duckduckgo.com/lite/"

// webURLGuard validates a model-chosen URL before Helix dials it.
//
// A swappable seam (the convention firewall.go's criticRun already uses) so
// transport tests can reach an httptest server on 127.0.0.1 — which the real
// guard refuses, correctly. The relaxed replacement lives in the test file;
// production code never reassigns this.
var webURLGuard = guardPublicHTTPURL

// handleWebStep executes one planner web step.
//
// Args:
//   - step: a validated step with action=search|fetch.
//   - escalated: true when the firewall flagged this fetch's URL as copied from
//     retrieved context — it then requires explicit confirmation.
//
// Returns: the retrieved text (the observation the planner answers from), and
// an error when the step could not run at all.
// Complexity: O(1) HTTP round trip, bounded by webTimeout and the byte caps.
func (a *Agent) handleWebStep(step ai.PlanStep, escalated bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), webTimeout)
	defer cancel()

	switch step.Action {
	case "search":
		query := strings.TrimSpace(step.Args["query"])
		if query == "" {
			return "", errors.New("web step missing args.query")
		}
		a.render.PrintCommand("Web search: " + query)
		out, err := webSearch(ctx, query)
		if err != nil {
			return "", err
		}
		a.renderWebResult(out)
		return out, nil

	case "fetch":
		target := strings.TrimSpace(step.Args["url"])
		if target == "" {
			return "", errors.New("web step missing args.url")
		}
		if escalated {
			a.render.PrintWarning("Instruction Firewall: this URL came from retrieved content, " +
				"not from you — it may be a planted destination.")
			if !commands.AskForConfirmation(fmt.Sprintf("Fetch %s anyway?", target)) {
				return "", errors.New("web fetch declined (provenance escalation)")
			}
		}
		a.render.PrintCommand("Web fetch: " + target)
		out, err := webFetch(ctx, target)
		if err != nil {
			return "", err
		}
		a.renderWebResult(out)
		return out, nil

	default:
		return "", fmt.Errorf("unsupported web action %q (want search or fetch)", step.Action)
	}
}

// renderWebResult shows what came back. Printing it matters even though the
// planner also receives it: on a non-agentic turn the retrieved text is the only
// thing the user gets, and on an agentic one it is how they check the answer
// against its source.
func (a *Agent) renderWebResult(out string) {
	if strings.TrimSpace(out) == "" {
		a.render.PrintInfo("No results.")
		return
	}
	a.render.PrintData(out)
}

// webSearch queries the Lite endpoint and renders the top results.
func webSearch(ctx context.Context, query string) (string, error) {
	endpoint := webSearchEndpoint + "?" + url.Values{"q": {query}}.Encode()
	body, _, err := webGet(ctx, endpoint, webFetchMaxBytes*4)
	if err != nil {
		return "", fmt.Errorf("web search: %w", err)
	}

	results := parseLiteResults(string(body))
	if len(results) == 0 {
		return "", nil
	}

	var b strings.Builder
	for i, r := range results {
		_, _ = fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			_, _ = fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
	}
	return capText(b.String(), webSearchMaxChars), nil
}

// webFetch retrieves one URL as text.
func webFetch(ctx context.Context, raw string) (string, error) {
	target, err := webURLGuard(raw)
	if err != nil {
		return "", fmt.Errorf("web fetch: %w", err)
	}

	body, contentType, err := webGet(ctx, target.String(), webFetchMaxBytes)
	if err != nil {
		return "", fmt.Errorf("web fetch: %w", err)
	}

	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml":
		return capText(htmlToText(string(body)), webFetchMaxBytes), nil
	case strings.HasPrefix(mediaType, "text/"),
		mediaType == "application/json",
		mediaType == "application/xml",
		strings.HasSuffix(mediaType, "+json"),
		mediaType == "":
		// Already text (or unlabelled, which servers do): hand it back as-is.
		// Stripping tags from JSON would corrupt it.
		return capText(string(body), webFetchMaxBytes), nil
	default:
		// A PDF or an image is not something the planner can read, and dumping
		// its bytes into a prompt is worse than saying so.
		return "", fmt.Errorf("web fetch: content type %q is not text", mediaType)
	}
}

// webClient is the shared client for the web tool. Redirects are followed but
// re-guarded, so a public URL cannot 302 into the loopback interface.
var webClient = &http.Client{
	Timeout: webTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if _, err := webURLGuard(req.URL.String()); err != nil {
			return fmt.Errorf("redirect blocked: %w", err)
		}
		return nil
	},
}

// webGet performs one bounded GET.
//
// Args:
//   - ctx: the step's deadline.
//   - target: an absolute http(s) URL.
//   - maxBytes: read limit; the body is truncated, not rejected, past it.
//
// Returns: the body bytes, the raw Content-Type header, or an error.
// Complexity: O(min(response size, maxBytes)).
func webGet(ctx context.Context, target string, maxBytes int) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json;q=0.9,*/*;q=0.5")

	resp, err := webClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)))
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// webResult is one parsed search hit.
type webResult struct {
	Title   string
	URL     string
	Snippet string
}

// Lite-endpoint markup shapes. DuckDuckGo Lite renders results as a table:
// a link cell (class "result-link") followed by a snippet cell
// (class "result-snippet"). Matching on those class names rather than on the
// table structure survives the whitespace and attribute-order churn that
// breaks position-based parsing.
var (
	// Quote-agnostic and attribute-order-agnostic, both learned the hard way.
	//
	// These previously required class="..." in DOUBLE quotes and class BEFORE
	// href. DuckDuckGo now emits <a rel="nofollow" href="..." class='result-link'>
	// — single quotes, href first — so the pattern matched nothing and every
	// search returned "No results". That failure is worse than an error: the
	// planner then answered from the model's own training data, presenting an
	// ungrounded answer as if it had been retrieved.
	liteLinkRe = regexp.MustCompile(
		`(?is)<a\b(?:[^>]*\bclass\s*=\s*["'][^"']*result-link[^"']*["'])[^>]*>(.*?)</a>`)
	liteLinkOpenRe = regexp.MustCompile(
		`(?is)<a\b[^>]*\bclass\s*=\s*["'][^"']*result-link[^"']*["'][^>]*>`)
	hrefRe        = regexp.MustCompile(`(?is)\bhref\s*=\s*["']([^"']+)["']`)
	liteSnippetRe = regexp.MustCompile(
		`(?is)<td\b[^>]*\bclass\s*=\s*["'][^"']*result-snippet[^"']*["'][^>]*>(.*?)</td>`)
	tagRe         = regexp.MustCompile(`(?is)<[^>]*>`)
	scriptStyleRe = regexp.MustCompile(`(?is)<(script|style|noscript|head)\b.*?</(script|style|noscript|head)>`)
	whitespaceRe  = regexp.MustCompile(`[ \t]{2,}`)
	blankLinesRe  = regexp.MustCompile(`\n{3,}`)
)

// parseLiteResults extracts the top hits from a Lite results page.
//
// Titles and snippets are paired by index: the Lite layout emits them in the
// same order, and a page with fewer snippets than links (an ad row, a truncated
// response) simply yields results with an empty snippet rather than a
// mispaired one.
//
// Args: html: the response body.
// Returns: up to webSearchMaxResults results.
// Complexity: O(len(html)).
func parseLiteResults(html string) []webResult {
	// Open tags and inner text are matched separately so href can be pulled from
	// the tag regardless of where it sits among the attributes.
	opens := liteLinkOpenRe.FindAllString(html, webSearchMaxResults)
	links := liteLinkRe.FindAllStringSubmatch(html, webSearchMaxResults)
	snippets := liteSnippetRe.FindAllStringSubmatch(html, webSearchMaxResults)

	out := make([]webResult, 0, len(links))
	for i, m := range links {
		href := ""
		if i < len(opens) {
			if h := hrefRe.FindStringSubmatch(opens[i]); len(h) > 1 {
				href = strings.TrimSpace(unescapeHTML(h[1]))
			}
		}
		// Tags first, then entities: decoding first would let "&lt;b&gt;" turn
		// into a tag that the stripper then eats, silently deleting real text.
		title := squashSpace(unescapeHTML(stripTags(m[1])))
		if href == "" || title == "" {
			continue
		}
		// Lite sometimes emits protocol-relative or redirect-wrapped hrefs.
		if strings.HasPrefix(href, "//") {
			href = "https:" + href
		}
		r := webResult{Title: title, URL: href}
		if i < len(snippets) {
			r.Snippet = squashSpace(unescapeHTML(stripTags(snippets[i][1])))
		}
		out = append(out, r)
	}
	return out
}

// htmlToText is a crude tag-stripper: enough to give the planner readable prose
// from an article page, deliberately not an HTML parser (no new dependency, and
// a full DOM buys nothing for text extraction).
func htmlToText(html string) string {
	// Script and style bodies are not prose and would dominate the byte budget.
	html = scriptStyleRe.ReplaceAllString(html, " ")
	// Block-level boundaries become newlines so paragraphs survive.
	html = regexp.MustCompile(`(?is)</(p|div|li|tr|h[1-6]|section|article|br)\s*>|<br\s*/?>`).
		ReplaceAllString(html, "\n")
	text := unescapeHTML(stripTags(html))

	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if s := strings.TrimSpace(whitespaceRe.ReplaceAllString(line, " ")); s != "" {
			kept = append(kept, s)
		}
	}
	return blankLinesRe.ReplaceAllString(strings.Join(kept, "\n"), "\n\n")
}

// stripTags removes every HTML tag.
func stripTags(s string) string { return tagRe.ReplaceAllString(s, " ") }

// unescapeHTML resolves the handful of entities that actually matter in titles,
// snippets, and hrefs. &amp; is resolved LAST so "&amp;lt;" cannot become "<".
func unescapeHTML(s string) string {
	return strings.NewReplacer(
		"&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&#x27;", "'",
		"&nbsp;", " ", "&amp;", "&",
	).Replace(s)
}

// squashSpace collapses all whitespace runs into single spaces.
func squashSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// capText truncates to at most max bytes on a rune boundary, marking the cut so
// neither the planner nor the user mistakes a fragment for the whole page.
func capText(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + "\n… [truncated]"
}

// utf8Start reports whether b begins a UTF-8 sequence (i.e. is not a
// continuation byte).
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// guardPublicHTTPURL is the SSRF gate for a model-chosen URL.
//
// `fetch` takes a URL from an LLM whose context may include retrieved web pages,
// so it is precisely the shape attackers use to reach things only the host can
// see: the cloud metadata service (169.254.169.254), an admin port on
// localhost, a printer on the LAN. Nothing Helix needs from the web lives at a
// non-public address, so those are refused outright rather than heuristically
// scored.
//
// Args: raw: the URL as the planner wrote it.
// Returns: the parsed URL, or an error naming what was refused.
// Complexity: O(1) plus one DNS resolution for a hostname.
func guardPublicHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("unparseable URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("scheme %q is not allowed (http/https only)", u.Scheme)
	}
	if u.User != nil {
		// Credentials in a model-written URL are either a hallucination or an
		// attempt to send something somewhere.
		return nil, errors.New("URLs with embedded credentials are refused")
	}
	host := u.Hostname()
	if host == "" {
		return nil, errors.New("URL has no host")
	}

	// A literal IP is checked directly; a name is resolved, because the guard
	// has to be about where the request LANDS, not how it is spelled.
	if ip := net.ParseIP(host); ip != nil {
		if !publicIP(ip) {
			return nil, fmt.Errorf("host %s is not a public address", host)
		}
		return u, nil
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return nil, fmt.Errorf("host %q is local", host)
	}

	addrs, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve %q: %w", host, err)
	}
	for _, ip := range addrs {
		if !publicIP(ip) {
			return nil, fmt.Errorf("host %q resolves to the non-public address %s", host, ip)
		}
	}
	return u, nil
}

// publicIP reports whether an address is routable on the public internet.
func publicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsPrivate() {
		return false
	}
	// Carrier-grade NAT (100.64.0.0/10) and the IPv4 benchmark range are not
	// covered by the stdlib predicates but are equally not "the public web".
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
		if v4[0] == 198 && (v4[1] == 18 || v4[1] == 19) {
			return false
		}
	}
	return true
}
