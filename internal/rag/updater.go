// internal/rag/updater.go
// Purpose: Live threat-intelligence sync pipeline (NVD, CISA KEV, Exploit-DB,
// MITRE ATT&CK) with ETag conditional-GET caching, checkpointing, retries,
// and stage/progress hooks for the live TrueColor bar.
// Hardening: UpdateAll now checks the caller context between every stage so
// Ctrl+C (via the interrupt manager) aborts the pipeline promptly instead of
// continuing to fetch the remaining feeds.
package rag

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/utils"

	"github.com/fatih/color"
)

var (
	// Feed URLs are vars so tests can point them at local servers.
	kevURL        = "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"
	exploitCSVURL = "https://gitlab.com/exploit-database/exploitdb/-/raw/main/files_exploits.csv"
	mitreSTIXURL  = "https://raw.githubusercontent.com/mitre/cti/master/enterprise-attack/enterprise-attack.json"
	// nvdBaseURL is mutable for tests.
	nvdBaseURL = "https://services.nvd.nist.gov/rest/json/cves/2.0"
	// Retry tuning, mutable for tests.
	nvdRetryAttempts  = 3
	nvdRetryBaseDelay = 2 * time.Second
)

const (
	defaultTimeout = 120 * time.Second
	// meta keys for conditional-GET (ETag) caching.
	metaETagKEV     = "etag_kev"
	metaETagExploit = "etag_exploitdb"
	metaETagMitre   = "etag_mitre"
	// Polite identification for threat-intel feeds.
	threatUserAgent = "Helix/1.0 (defensive threat intelligence; https://github.com/Nibir1/Helix)"
)

// APIError is a structured upstream API error.
type APIError struct {
	Source     string
	StatusCode int
	URL        string
	Body       string
}

// Error implements error.
func (e *APIError) Error() string {
	if e.StatusCode == 0 {
		return fmt.Sprintf("%s request failed: %s", e.Source, e.Body)
	}
	return fmt.Sprintf("%s API returned %d: %s", e.Source, e.StatusCode, errorSnippet(e.Body, 300))
}

// errorSnippet truncates error bodies for readable logs.
func errorSnippet(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// interfaceSliceToString converts []interface{} to []string
func interfaceSliceToString(in []interface{}) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = fmt.Sprint(v)
	}
	return out
}

// generateEmbeddings batches and stores OpenAI embeddings for new records.
func generateEmbeddings(ctx context.Context, db *sql.DB, interactive bool) error {
	if !ai.HasOpenAIKey() {
		// FIX: Route the skip message through the progress bar hook if it exists,
		// otherwise print to stdout only if we are in an interactive session.
		if OnUpdateStage != nil {
			notifyStage("EMBEDDINGS SKIPPED")
		} else if interactive {
			color.Yellow("No OpenAI key configured; skipping embedding generation.")
		}
		return nil
	}
	sources := []string{"cve", "kev", "exploit", "mitre_technique"}
	for _, src := range sources {
		// Get up to 50 rows without embedding
		query := fmt.Sprintf(`SELECT %s.id, %s.description FROM %s
LEFT JOIN embeddings e ON e.source_type=? AND e.source_id=%s.id
WHERE e.embedding IS NULL LIMIT 50`, idColumn(src), descColumn(src), src, idColumn(src))
		rows, err := db.Query(query, src)
		if err != nil {
			continue
		}
		var batch []string
		var ids []string
		for rows.Next() {
			var id, text string
			if err := rows.Scan(&id, &text); err != nil {
				continue
			}
			if text == "" {
				text = id
			}
			batch = append(batch, text)
			ids = append(ids, id)
		}
		_ = rows.Close()
		if len(batch) == 0 {
			continue
		}
		notifyProgress("EMBEDDING "+strings.ToUpper(src), len(ids), len(ids))
		embeddings, err := ai.GetEmbeddings(batch)
		if err != nil {
			return fmt.Errorf("get embeddings for %s: %w", src, err)
		}
		for i, emb := range embeddings {
			if emb == nil {
				continue
			}
			blob, _ := json.Marshal(emb)
			_, err := db.Exec(`INSERT OR REPLACE INTO embeddings(source_type, source_id, model, embedding)
VALUES(?,?,?,?)`, src, ids[i], "text-embedding-3-small", blob)
			if err != nil {
				return fmt.Errorf("insert embedding: %w", err)
			}
		}
	}
	return nil
}

func idColumn(src string) string {
	switch src {
	case "cve":
		return "id"
	case "kev":
		return "cve_id"
	case "exploit":
		return "edb_id"
	case "mitre_technique":
		return "technique_id"
	}
	return "id"
}

func descColumn(src string) string {
	switch src {
	case "cve", "exploit", "mitre_technique":
		return "description"
	case "kev":
		return "notes"
	}
	return "description"
}

// nvdResponse matches actual NVD API 2.0 response.
type nvdResponse struct {
	TotalResults    int `json:"totalResults"`
	Vulnerabilities []struct {
		CVE struct {
			ID           string `json:"id"`
			Published    string `json:"published"`
			LastModified string `json:"lastModified"`
			Descriptions []struct {
				Lang  string `json:"lang"`
				Value string `json:"value"`
			} `json:"descriptions"`
			Metrics struct {
				CVSSMetricV31 []cvssMetric `json:"cvssMetricV31"`
				CVSSMetricV30 []cvssMetric `json:"cvssMetricV30"`
				CVSSMetricV2  []cvssMetric `json:"cvssMetricV2"`
			} `json:"metrics"`
		} `json:"cve"`
	} `json:"vulnerabilities"`
}

type cvssMetric struct {
	CVSSData struct {
		BaseScore float64 `json:"baseScore"`
	} `json:"cvssData"`
}

// meta helpers
func getMeta(db *sql.DB, key string) string {
	var val string
	if err := db.QueryRow("SELECT value FROM meta WHERE key=?", key).Scan(&val); err != nil {
		return ""
	}
	return val
}

func setMeta(db *sql.DB, key, value string) {
	_, _ = db.Exec("INSERT OR REPLACE INTO meta(key, value) VALUES(?,?)", key, value)
}

// UpdateAll fetches all data sources and updates the database.
// internet-gated, fast-sources-first, stage-hooked, silent unless
// HELIX_DEBUG=1.
// FIX (interrupt hardening): the caller context is checked between every
// stage, so a Ctrl+C cancellation aborts the pipeline promptly and unwinds
// all registered interrupt scopes back to the live prompt.
//
// Args:
//   - ctx: cancellation/timeout context (registered by the caller).
//   - db: open knowledge database handle.
//   - interactive: whether consented embedding bootstraps may prompt.
//
// Returns: error (context.Canceled when interrupted; ErrOffline when offline).
// Complexity: O(network sync time).
func UpdateAll(ctx context.Context, db *sql.DB, interactive bool) error {
	notifyStage("STARTING KNOWLEDGE UPDATE")
	start := time.Now()
	// INTERNET GATE: every fetch in this pipeline is network-backed.
	// Fail fast instead of burning per-source network errors while offline.
	if !utils.IsOnline(3 * time.Second) {
		notifyStage("OFFLINE - SKIPPING NETWORK FETCHES")
		return ErrOffline
	}
	// Fast, high-value sources FIRST.
	notifyStage("FETCHING CISA KEV")
	if err := updateKEV(ctx, db); err != nil && utils.IsDebugMode() {
		color.Yellow("KEV update failed: %v", err)
	}
	// FIX: Ctrl+C aborts the pipeline between stages.
	if ctx.Err() != nil {
		notifyStage("KNOWLEDGE UPDATE CANCELLED")
		return ctx.Err()
	}
	notifyStage("FETCHING EXPLOIT-DB")
	if err := updateExploitDB(ctx, db); err != nil && utils.IsDebugMode() {
		color.Yellow("Exploit-DB update failed: %v", err)
	}
	if ctx.Err() != nil {
		notifyStage("KNOWLEDGE UPDATE CANCELLED")
		return ctx.Err()
	}
	notifyStage("FETCHING MITRE ATT&CK")
	if err := updateMITRE(ctx, db); err != nil && utils.IsDebugMode() {
		color.Yellow("MITRE update failed: %v", err)
	}
	if ctx.Err() != nil {
		notifyStage("KNOWLEDGE UPDATE CANCELLED")
		return ctx.Err()
	}
	// NVD last: it is the slowest feed (rate-limited) and checkpoints itself,
	// so a cancellation resumes on the next run.
	notifyStage("FETCHING NVD CVES")
	if err := updateNVD(ctx, db); err != nil && utils.IsDebugMode() {
		color.Yellow("NVD update skipped/failed: %v", err)
	}
	if ctx.Err() != nil {
		notifyStage("KNOWLEDGE UPDATE CANCELLED")
		return ctx.Err()
	}
	// FIX: Pass the interactive flag down to the embedding generator.
	notifyStage("GENERATING EMBEDDINGS")
	if err := generateEmbeddings(ctx, db, interactive); err != nil && utils.IsDebugMode() {
		color.Yellow("Embedding generation failed: %v", err)
	}
	if ctx.Err() != nil {
		notifyStage("KNOWLEDGE UPDATE CANCELLED")
		return ctx.Err()
	}
	notifyStage("REBUILDING FTS INDEX")
	// FIX: Pass the progress callback to satisfy the new database.go signature.
	if err := ReindexKnowledgeFTS(db, func(current, total int) {
		notifyStage(fmt.Sprintf("REBUILDING FTS INDEX (%d/%d)", current, total))
	}); err != nil && utils.IsDebugMode() {
		color.Yellow("FTS reindex failed: %v", err)
	}
	notifyStage("KNOWLEDGE UPDATE COMPLETE")
	if utils.IsDebugMode() {
		color.Green("Knowledge base update completed in %v", time.Since(start))
	}
	return nil
}

// fetchConditional performs an ETag-aware GET against a threat feed.
func fetchConditional(ctx context.Context, client *http.Client, url, storedETag string) ([]byte, string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set("User-Agent", threatUserAgent)
	if storedETag != "" {
		req.Header.Set("If-None-Match", storedETag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified {
		return nil, "", false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("%s returned %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, "", false, err
	}
	return body, resp.Header.Get("ETag"), true, nil
}

// updateNVD uses NVD API 2.0 with strict timeouts, browser spoofing, rate-limiting, and checkpointing.
// ENHANCED: Includes real-time ETA calculation for progress transparency.
func updateNVD(ctx context.Context, db *sql.DB) error {
	lastMod := getMeta(db, "nvd_last_mod_date")
	layout := "2006-01-02T15:04:05.000"
	var startTime time.Time
	if lastMod == "" {
		startTime = time.Now().UTC().AddDate(0, 0, -119)
	} else {
		if t, err := time.Parse(layout, lastMod); err == nil {
			startTime = t
		} else if t, err := time.Parse("2006-01-02T15:04:05.000Z", lastMod); err == nil {
			startTime = t
		} else {
			startTime = time.Now().UTC().AddDate(0, 0, -119)
		}
	}
	client := &http.Client{Timeout: 90 * time.Second}
	userAgent := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	apiKey := os.Getenv("NVD_API_KEY")
	sleepDuration := 6500 * time.Millisecond
	if apiKey != "" {
		sleepDuration = 1 * time.Second
	}
	var total int
	page := 0
	latestModDateSeen := startTime
	// ETA tracking
	syncStart := time.Now()
	var totalPages int
	for startIndex := 0; ; startIndex += 2000 {
		select {
		case <-ctx.Done():
			setMeta(db, "nvd_last_mod_date", latestModDateSeen.Format(layout))
			return ctx.Err()
		default:
		}
		page++
		endTime := time.Now().UTC()
		if endTime.Sub(startTime) > 119*24*time.Hour {
			endTime = startTime.AddDate(0, 0, 119)
		}
		lastModStart := startTime.Format(layout)
		lastModEnd := endTime.Format(layout)
		requestURL := fmt.Sprintf(
			"%s?lastModStartDate=%s&lastModEndDate=%s&resultsPerPage=2000&startIndex=%d",
			nvdBaseURL, lastModStart, lastModEnd, startIndex,
		)
		notifyStage(fmt.Sprintf("NVD · PAGE %d", page))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return fmt.Errorf("NVD request creation: %w", err)
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")
		if apiKey != "" {
			req.Header.Set("apiKey", apiKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			setMeta(db, "nvd_last_mod_date", latestModDateSeen.Format(layout))
			return fmt.Errorf("NVD network error: %w", err)
		}
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
			notifyStage("NVD · RATE LIMITED")
			time.Sleep(30 * time.Second)
			startIndex -= 2000
			continue
		}
		if resp.StatusCode != http.StatusOK {
			setMeta(db, "nvd_last_mod_date", latestModDateSeen.Format(layout))
			return fmt.Errorf("NVD API returned %d", resp.StatusCode)
		}
		var nvdResp nvdResponse
		if err := json.Unmarshal(bodyBytes, &nvdResp); err != nil {
			setMeta(db, "nvd_last_mod_date", latestModDateSeen.Format(layout))
			return fmt.Errorf("decode NVD: %w", err)
		}
		// Capture total pages for ETA calculation
		if totalPages == 0 && nvdResp.TotalResults > 0 {
			totalPages = (nvdResp.TotalResults + 1999) / 2000 // Ceiling division
		}
		for _, vuln := range nvdResp.Vulnerabilities {
			cveID := vuln.CVE.ID
			if cveID == "" {
				continue
			}
			if vuln.CVE.LastModified != "" {
				if t, err := time.Parse(layout, vuln.CVE.LastModified); err == nil {
					if t.After(latestModDateSeen) {
						latestModDateSeen = t
					}
				}
			}
			desc := ""
			if len(vuln.CVE.Descriptions) > 0 {
				desc = vuln.CVE.Descriptions[0].Value
			}
			cvss := 0.0
			if len(vuln.CVE.Metrics.CVSSMetricV31) > 0 {
				cvss = vuln.CVE.Metrics.CVSSMetricV31[0].CVSSData.BaseScore
			}
			raw, _ := json.Marshal(vuln)
			_, err := db.Exec(`INSERT OR REPLACE INTO cve(id, description, cvss_score, published_date, last_modified_date, raw_json) VALUES(?,?,?,?,?,?)`,
				cveID, desc, cvss, vuln.CVE.Published, vuln.CVE.LastModified, string(raw))
			if err != nil {
				return fmt.Errorf("insert CVE %s: %w", cveID, err)
			}
			total++
		}
		// ENHANCED: Progress with ETA
		if nvdResp.TotalResults > 0 {
			etaStr := calculateETA(syncStart, page, totalPages)
			notifyProgress(fmt.Sprintf("NVD · PAGE %d (ETA: %s)", page, etaStr), total, nvdResp.TotalResults)
		}
		if nvdResp.TotalResults == 0 || startIndex+2000 >= nvdResp.TotalResults {
			break
		}
		time.Sleep(sleepDuration)
	}
	setMeta(db, "nvd_last_mod_date", latestModDateSeen.Format(layout))
	if OnUpdateStage != nil {
		notifyProgress("NVD SYNCED", total, total)
	} else {
		color.Green("NVD: %d CVEs updated successfully", total)
	}
	return nil
}

// calculateETA computes a human-readable ETA based on progress rate.
func calculateETA(syncStart time.Time, currentPage, totalPages int) string {
	if currentPage == 0 || totalPages == 0 || currentPage >= totalPages {
		return "calculating..."
	}
	elapsed := time.Since(syncStart)
	rate := float64(currentPage) / elapsed.Seconds()
	if rate <= 0 {
		return "calculating..."
	}
	remainingPages := totalPages - currentPage
	remainingSeconds := float64(remainingPages) / rate
	eta := time.Duration(remainingSeconds * float64(time.Second))
	// Format ETA nicely
	if eta < time.Minute {
		return fmt.Sprintf("%ds", int(eta.Seconds()))
	} else if eta < time.Hour {
		return fmt.Sprintf("%dm%ds", int(eta.Minutes()), int(eta.Seconds())%60)
	} else {
		hours := int(eta.Hours())
		mins := int(eta.Minutes()) % 60
		return fmt.Sprintf("%dh%dm", hours, mins)
	}
}

// updateKEV fetches and replaces KEV entries.
func updateKEV(ctx context.Context, db *sql.DB) error {
	client := &http.Client{Timeout: defaultTimeout}
	body, newETag, changed, err := fetchConditional(ctx, client, kevURL, getMeta(db, metaETagKEV))
	if err != nil {
		return err
	}
	if !changed {
		notifyStage("KEV UNCHANGED (HTTP 304)")
		return nil
	}
	var kevData struct {
		Vulnerabilities []struct {
			CVEID          string `json:"cveID"`
			VendorProject  string `json:"vendorProject"`
			DateAdded      string `json:"dateAdded"`
			RequiredAction string `json:"requiredAction"`
			DueDate        string `json:"dueDate"`
			Notes          string `json:"notes"`
		} `json:"vulnerabilities"`
	}
	if err := json.Unmarshal(body, &kevData); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, _ = tx.Exec("DELETE FROM kev")
	for _, v := range kevData.Vulnerabilities {
		_, err := tx.Exec(`INSERT OR REPLACE INTO kev(cve_id, title, date_added, required_action, due_date, notes) VALUES(?,?,?,?,?,?)`,
			v.CVEID, v.VendorProject, v.DateAdded, v.RequiredAction, v.DueDate, v.Notes)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if newETag != "" {
		setMeta(db, metaETagKEV, newETag)
	}
	notifyProgress("KEV SYNCED", len(kevData.Vulnerabilities), len(kevData.Vulnerabilities))
	return nil
}

// updateExploitDB parses the CSV and replaces all exploit entries.
func updateExploitDB(ctx context.Context, db *sql.DB) error {
	client := &http.Client{Timeout: defaultTimeout}
	body, newETag, changed, err := fetchConditional(ctx, client, exploitCSVURL, getMeta(db, metaETagExploit))
	if err != nil {
		return err
	}
	if !changed {
		notifyStage("EXPLOIT-DB UNCHANGED (HTTP 304)")
		return nil
	}
	reader := csv.NewReader(bytes.NewReader(body))
	reader.Comma = ','
	reader.LazyQuotes = true
	records, err := reader.ReadAll()
	if err != nil {
		return err
	}
	if len(records) < 2 {
		return fmt.Errorf("empty CSV")
	}
	header := records[0]
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.TrimSpace(h)] = i
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, _ = tx.Exec("DELETE FROM exploit")
	rows := 0
	for _, row := range records[1:] {
		get := func(col string) string {
			if i, ok := idx[col]; ok && i < len(row) {
				return strings.TrimSpace(row[i])
			}
			return ""
		}
		edbID := get("id")
		if edbID == "" {
			continue
		}
		_, err := tx.Exec(`INSERT OR REPLACE INTO exploit(edb_id, cve_id, description, platform, type, date_published, author, raw_text) VALUES(?,?,?,?,?,?,?,?)`,
			edbID, get("cve_id"), get("description"), get("platform"), get("type"), get("date_published"), get("author"), get("description"))
		if err != nil {
			return err
		}
		rows++
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if newETag != "" {
		setMeta(db, metaETagExploit, newETag)
	}
	notifyProgress("EDB SYNCED", rows, rows)
	return nil
}

// updateMITRE fetches and parses MITRE ATT&CK STIX.
func updateMITRE(ctx context.Context, db *sql.DB) error {
	client := &http.Client{Timeout: defaultTimeout}
	body, newETag, changed, err := fetchConditional(ctx, client, mitreSTIXURL, getMeta(db, metaETagMitre))
	if err != nil {
		return err
	}
	if !changed {
		notifyStage("MITRE UNCHANGED (HTTP 304)")
		return nil
	}
	var bundle struct {
		Objects []json.RawMessage `json:"objects"`
	}
	if err := json.Unmarshal(body, &bundle); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, _ = tx.Exec("DELETE FROM mitre_technique")
	techs := 0
	for _, obj := range bundle.Objects {
		var objMap map[string]interface{}
		if err := json.Unmarshal(obj, &objMap); err != nil {
			continue
		}
		if typ, _ := objMap["type"].(string); typ == "attack-pattern" {
			refs, _ := objMap["external_references"].([]interface{})
			var techID string
			for _, ref := range refs {
				refMap, ok := ref.(map[string]interface{})
				if !ok {
					continue
				}
				if src, _ := refMap["source_name"].(string); src == "mitre-attack" {
					techID, _ = refMap["external_id"].(string)
					break
				}
			}
			if techID == "" {
				continue
			}
			name, _ := objMap["name"].(string)
			desc, _ := objMap["description"].(string)
			xPlatforms, _ := objMap["x_mitre_platforms"].([]interface{})
			platforms := strings.Join(interfaceSliceToString(xPlatforms), ",")
			detection, _ := objMap["x_mitre_detection"].(string)
			_, err := tx.Exec(`INSERT OR REPLACE INTO mitre_technique(technique_id, name, description, platform, detection) VALUES(?,?,?,?,?)`,
				techID, name, desc, platforms, detection)
			if err != nil {
				return err
			}
			techs++
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if newETag != "" {
		setMeta(db, metaETagMitre, newETag)
	}
	notifyProgress("MITRE SYNCED", techs, techs)
	return nil
}

// OnUpdateStage is an optional hook invoked at each update stage.
var OnUpdateStage func(stage string, current, total int)

// notifyStage reports an indeterminate stage transition.
func notifyStage(stage string) { notifyProgress(stage, 0, 0) }

// notifyProgress reports a determinate progress snapshot.
func notifyProgress(stage string, current, total int) {
	if OnUpdateStage != nil {
		OnUpdateStage(stage, current, total)
	}
}
