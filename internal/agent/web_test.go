// internal/agent/web_test.go
// Purpose: the `web` tool — both actions end to end against httptest, the byte
// caps, content-type handling, and the SSRF guard that stands between a
// model-chosen URL and the host's own network.
package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"helix/internal/ai"
)

// allowAnyHTTPURL is the test-only relaxation of guardPublicHTTPURL: it keeps
// the scheme check and drops the address check, so a test can dial an httptest
// server on 127.0.0.1 — which the real guard refuses, correctly.
func allowAnyHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u, nil
	default:
		return nil, fmt.Errorf("scheme %q is not allowed", u.Scheme)
	}
}

// relaxWebGuard points the fetcher at loopback for the duration of one test.
func relaxWebGuard(t *testing.T) {
	t.Helper()
	prev := webURLGuard
	webURLGuard = allowAnyHTTPURL
	t.Cleanup(func() { webURLGuard = prev })
}

// useSearchEndpoint redirects the search action at a stub server.
func useSearchEndpoint(t *testing.T, url string) {
	t.Helper()
	prev := webSearchEndpoint
	webSearchEndpoint = url
	t.Cleanup(func() { webSearchEndpoint = prev })
}

// liteResultsPage renders a DuckDuckGo-Lite-shaped results table.
func liteResultsPage(n int) string {
	var b strings.Builder
	b.WriteString("<html><body><table>")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b,
			`<tr><td><a rel="nofollow" class="result-link" href="https://example.com/%d">Result &amp; Title %d</a></td></tr>`+
				`<tr><td class="result-snippet">Snippet number %d with <b>markup</b>.</td></tr>`, i, i, i)
	}
	b.WriteString("</table></body></html>")
	return b.String()
}

func TestHandleWebStepSearch(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "Helix/") {
			t.Errorf("User-Agent = %q, want a descriptive Helix agent", ua)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(liteResultsPage(8)))
	}))
	defer srv.Close()
	useSearchEndpoint(t, srv.URL+"/lite/")

	ag, _ := newTestAgent(t)
	out, err := ag.handleWebStep(ai.PlanStep{
		Tool: "web", Action: "search", Args: map[string]string{"query": "current us president"},
	}, false)
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if gotQuery != "current us president" {
		t.Errorf("query sent = %q, want the step's query", gotQuery)
	}
	// Titles, URLs, and snippets all reach the observation, entities decoded.
	for _, want := range []string{"Result & Title 1", "https://example.com/1", "Snippet number 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("results are missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "<b>") {
		t.Errorf("markup survived into the observation:\n%s", out)
	}
	// The result count is capped, so a huge page cannot flood the planner.
	if n := strings.Count(out, "https://example.com/"); n > webSearchMaxResults {
		t.Errorf("kept %d results, want at most %d", n, webSearchMaxResults)
	}
}

func TestHandleWebStepFetchHTML(t *testing.T) {
	relaxWebGuard(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><style>body{color:red}</style>` +
			`<script>var secret=1</script></head><body>` +
			`<h1>Release notes</h1><p>Go 1.25 is out &amp; stable.</p></body></html>`))
	}))
	defer srv.Close()

	ag, _ := newTestAgent(t)
	out, err := ag.handleWebStep(ai.PlanStep{
		Tool: "web", Action: "fetch", Args: map[string]string{"url": srv.URL},
	}, false)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	for _, want := range []string{"Release notes", "Go 1.25 is out & stable."} {
		if !strings.Contains(out, want) {
			t.Errorf("text is missing %q:\n%s", want, out)
		}
	}
	// Script and style bodies are not prose and would eat the byte budget.
	for _, unwanted := range []string{"var secret", "color:red", "<p>"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("%q must be stripped from fetched HTML:\n%s", unwanted, out)
		}
	}
}

// JSON must come back verbatim: tag-stripping it would corrupt the payload.
func TestHandleWebStepFetchNonHTMLContentTypes(t *testing.T) {
	relaxWebGuard(t)
	const body = `{"version":"1.25","notes":"a < b"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	ag, _ := newTestAgent(t)
	out, err := ag.handleWebStep(ai.PlanStep{
		Tool: "web", Action: "fetch", Args: map[string]string{"url": srv.URL},
	}, false)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if out != body {
		t.Errorf("JSON was rewritten:\ngot  %s\nwant %s", out, body)
	}
}

// A PDF or an image cannot be read by the planner, and dumping its bytes into a
// prompt is worse than saying so.
func TestHandleWebStepFetchRejectsBinaryContent(t *testing.T) {
	relaxWebGuard(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7\x00\x01binary"))
	}))
	defer srv.Close()

	ag, _ := newTestAgent(t)
	_, err := ag.handleWebStep(ai.PlanStep{
		Tool: "web", Action: "fetch", Args: map[string]string{"url": srv.URL},
	}, false)
	if err == nil {
		t.Fatal("a non-text content type must be reported, not forwarded as bytes")
	}
	if !strings.Contains(err.Error(), "not text") {
		t.Errorf("error should name the cause, got %v", err)
	}
}

func TestHandleWebStepFetchCapsSize(t *testing.T) {
	relaxWebGuard(t)
	huge := strings.Repeat("plain text payload. ", 5000) // ~100 KB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(huge))
	}))
	defer srv.Close()

	ag, _ := newTestAgent(t)
	out, err := ag.handleWebStep(ai.PlanStep{
		Tool: "web", Action: "fetch", Args: map[string]string{"url": srv.URL},
	}, false)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(out) > webFetchMaxBytes+64 { // +64 for the truncation marker
		t.Errorf("fetched %d bytes, want ≤%d", len(out), webFetchMaxBytes)
	}
	if len(out) == 0 {
		t.Error("the cap must truncate, not discard")
	}
}

func TestHandleWebStepReportsHTTPErrors(t *testing.T) {
	relaxWebGuard(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	ag, _ := newTestAgent(t)
	if _, err := ag.handleWebStep(ai.PlanStep{
		Tool: "web", Action: "fetch", Args: map[string]string{"url": srv.URL},
	}, false); err == nil {
		t.Fatal("a 404 must surface as a step error, not as page text")
	}
}

func TestHandleWebStepRejectsBadSteps(t *testing.T) {
	ag, _ := newTestAgent(t)
	cases := map[string]ai.PlanStep{
		"unknown action":  {Tool: "web", Action: "post", Args: map[string]string{"url": "https://example.com"}},
		"search no query": {Tool: "web", Action: "search", Args: map[string]string{}},
		"fetch no url":    {Tool: "web", Action: "fetch", Args: map[string]string{}},
	}
	for name, step := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ag.handleWebStep(step, false); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

// The SSRF guard is the control that separates "read the public web" from "probe
// the machine Helix runs on". The URL comes from a model whose context may hold
// attacker-authored pages, so this list is the threat, not a formality.
func TestGuardPublicHTTPURLRefusesNonPublicTargets(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1:8080/admin",
		"http://localhost/",
		"http://[::1]/",
		"http://169.254.169.254/latest/meta-data/", // cloud metadata service
		"http://10.0.0.5/",
		"http://192.168.1.1/",
		"http://172.16.0.1/",
		"http://100.64.0.1/", // carrier-grade NAT
		"http://0.0.0.0/",
		"file:///etc/passwd",
		"gopher://example.com/",
		"https://user:pass@example.com/", // credentials in a model-written URL
		"http:///nohost",
	}
	for _, raw := range blocked {
		t.Run(raw, func(t *testing.T) {
			if _, err := guardPublicHTTPURL(raw); err == nil {
				t.Fatalf("%s must be refused", raw)
			}
		})
	}
}

func TestGuardPublicHTTPURLAllowsPublicHosts(t *testing.T) {
	for _, raw := range []string{"https://example.com/path?q=1", "http://93.184.216.34/"} {
		if _, err := guardPublicHTTPURL(raw); err != nil {
			t.Errorf("%s should be allowed, got %v", raw, err)
		}
	}
}

// A public URL must not be able to 302 into the loopback interface.
func TestWebFetchReGuardsRedirects(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secret internal page"))
	}))
	defer internal.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer redirector.Close()

	// The guard is left in production form for the redirect target; only the
	// FIRST hop is permitted, mimicking a public URL that redirects inward.
	prev := webURLGuard
	first := true
	webURLGuard = func(raw string) (*url.URL, error) {
		if first {
			first = false
			return allowAnyHTTPURL(raw)
		}
		return guardPublicHTTPURL(raw)
	}
	t.Cleanup(func() { webURLGuard = prev })

	out, err := webFetch(context.Background(), redirector.URL)
	if err == nil {
		t.Fatalf("a redirect into loopback must be blocked, got page: %q", out)
	}
	if strings.Contains(out, "secret internal page") {
		t.Error("the internal page was read despite the guard")
	}
}

func TestParseLiteResultsHandlesMissingSnippets(t *testing.T) {
	html := `<a class="result-link" href="https://a.example/">Alpha</a>` +
		`<a class="result-link" href="https://b.example/">Beta</a>` +
		`<td class="result-snippet">only one snippet</td>`

	got := parseLiteResults(html)
	if len(got) != 2 {
		t.Fatalf("parsed %d results, want 2: %+v", len(got), got)
	}
	if got[0].Snippet != "only one snippet" {
		t.Errorf("first snippet = %q", got[0].Snippet)
	}
	// Fewer snippets than links must leave the extra result snippet-less, never
	// paired with the wrong one.
	if got[1].Snippet != "" {
		t.Errorf("second result should have no snippet, got %q", got[1].Snippet)
	}
}

func TestParseLiteResultsIgnoresJunk(t *testing.T) {
	if got := parseLiteResults("<html><body>no results here</body></html>"); len(got) != 0 {
		t.Errorf("a page with no results must parse to none, got %+v", got)
	}
}

func TestUnescapeHTMLDoesNotDoubleDecode(t *testing.T) {
	// &amp;lt; must become "&lt;", not "<": resolving &amp; first would let an
	// escaped entity turn back into markup.
	if got := unescapeHTML("&amp;lt;script&amp;gt;"); got != "&lt;script&gt;" {
		t.Errorf("unescapeHTML double-decoded: %q", got)
	}
}

func TestCapTextMarksTruncation(t *testing.T) {
	got := capText(strings.Repeat("a", 100), 20)
	if !strings.Contains(got, "[truncated]") {
		t.Errorf("a cut must be visible, got %q", got)
	}
	if short := capText("fits", 20); short != "fits" {
		t.Errorf("text within the cap must be unchanged, got %q", short)
	}
	// Multi-byte input must not be split mid-rune.
	if got := capText(strings.Repeat("é", 50), 21); !strings.ContainsRune(got, 'é') ||
		strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("capText split a rune: %q", got)
	}
}

// TestParseLiteResultsToleratesAttributeVariation is the regression that made
// every web search return "No results".
//
// The old pattern required class="..." in DOUBLE quotes and class BEFORE href.
// DuckDuckGo emits <a rel="nofollow" href="..." class='result-link'> — single
// quotes, href first — so nothing matched. The failure was worse than an error:
// the planner then answered from the model's own training data and presented an
// ungrounded answer as retrieved.
func TestParseLiteResultsToleratesAttributeVariation(t *testing.T) {
	cases := map[string]string{
		"single quotes, href first": `
			<a rel="nofollow" href="https://example.com/a" class='result-link'>First Result</a>
			<td class='result-snippet'>Snippet one</td>`,
		"double quotes, class first": `
			<a class="result-link" href="https://example.com/a">First Result</a>
			<td class="result-snippet">Snippet one</td>`,
		"extra classes and spacing": `
			<a rel="nofollow" href = 'https://example.com/a' class = "foo result-link bar" >First Result</a>
			<td class = 'x result-snippet y'>Snippet one</td>`,
	}
	for name, html := range cases {
		got := parseLiteResults(html)
		if len(got) != 1 {
			t.Errorf("%s: parsed %d results, want 1", name, len(got))
			continue
		}
		if got[0].URL != "https://example.com/a" {
			t.Errorf("%s: url = %q", name, got[0].URL)
		}
		if got[0].Title != "First Result" {
			t.Errorf("%s: title = %q", name, got[0].Title)
		}
	}
}

// TestParseLiteResultsPairsHrefWithItsOwnLink guards the index pairing after
// href moved out of the main capture group.
func TestParseLiteResultsPairsHrefWithItsOwnLink(t *testing.T) {
	html := `
		<a rel="nofollow" href="https://first.example" class='result-link'>First</a>
		<td class='result-snippet'>one</td>
		<a rel="nofollow" href="https://second.example" class='result-link'>Second</a>
		<td class='result-snippet'>two</td>`

	got := parseLiteResults(html)
	if len(got) != 2 {
		t.Fatalf("parsed %d results, want 2", len(got))
	}
	if got[0].URL != "https://first.example" || got[0].Title != "First" {
		t.Errorf("result 0 mispaired: %+v", got[0])
	}
	if got[1].URL != "https://second.example" || got[1].Title != "Second" {
		t.Errorf("result 1 mispaired: %+v", got[1])
	}
}
