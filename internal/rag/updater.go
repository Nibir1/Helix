// internal/rag/updater.go
package rag

import (
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

	"github.com/fatih/color"
)

const (
	kevURL         = "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"
	exploitCSVURL  = "https://gitlab.com/exploit-database/exploitdb/-/raw/main/files_exploits.csv"
	mitreSTIXURL   = "https://raw.githubusercontent.com/mitre/cti/master/enterprise-attack/enterprise-attack.json"
	defaultTimeout = 120 * time.Second
)

var (
	// nvdBaseURL is mutable for tests.
	nvdBaseURL = "https://services.nvd.nist.gov/rest/json/cves/2.0"

	// Retry tuning, mutable for tests.
	nvdRetryAttempts  = 3
	nvdRetryBaseDelay = 2 * time.Second
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

// fetchWithRetry performs a GET request with retry/backoff for transient failures.
func fetchWithRetry(
	ctx context.Context,
	client *http.Client,
	requestURL string,
	headers map[string]string,
) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt < nvdRetryAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = &APIError{
				Source:     "nvd",
				StatusCode: 0,
				URL:        requestURL,
				Body:       err.Error(),
			}

			time.Sleep(nvdRetryBaseDelay * time.Duration(attempt+1))
			continue
		}

		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return bodyBytes, nil
		}

		apiErr := &APIError{
			Source:     "nvd",
			StatusCode: resp.StatusCode,
			URL:        requestURL,
			Body:       string(bodyBytes),
		}

		// Retry only transient/rate-limit errors.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = apiErr
			time.Sleep(nvdRetryBaseDelay * time.Duration(attempt+1))
			continue
		}

		// Non-retryable client error.
		return nil, apiErr
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("request failed after retries")
	}

	return nil, lastErr
}

// UpdateAll fetches all data sources and updates the database.
func UpdateAll(db *sql.DB) error {
	color.Blue("Starting knowledge base update...")
	start := time.Now()

	// Fetch NVD CVEs (incremental)
	if err := updateNVD(db); err != nil {
		color.Yellow("NVD update skipped/failed: %v", err)
	}

	// Fetch CISA KEV
	color.Blue("Fetching CISA KEV...")
	if err := updateKEV(db); err != nil {
		color.Yellow("KEV update failed: %v", err)
	}

	// Fetch Exploit-DB
	color.Blue("Fetching Exploit-DB...")
	if err := updateExploitDB(db); err != nil {
		color.Yellow("Exploit-DB update failed: %v", err)
	}

	// Fetch MITRE ATT&CK
	color.Blue("Fetching MITRE ATT&CK...")
	if err := updateMITRE(db); err != nil {
		color.Yellow("MITRE update failed: %v", err)
	}

	// Generate embeddings for new entries (if OpenAI key configured)
	if err := generateEmbeddings(db); err != nil {
		color.Yellow("Embedding generation failed: %v", err)
	}

	// Explicit FTS reindex to ensure search works immediately
	color.Blue("Rebuilding FTS index...")
	if err := ReindexKnowledgeFTS(db); err != nil {
		color.Yellow("FTS reindex failed: %v", err)
	}

	color.Green("Knowledge base update completed in %v", time.Since(start))
	return nil
}

// updateNVD uses NVD API 2.0 with strict timeouts, browser spoofing, rate-limiting, and checkpointing.
func updateNVD(db *sql.DB) error {
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

	// FIX 1: Increase timeout to 90s. NVD servers are notoriously slow to generate large JSON payloads.
	client := &http.Client{Timeout: 90 * time.Second}

	userAgent := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	apiKey := os.Getenv("NVD_API_KEY")

	// FIX 2: Dynamic rate limiting.
	// Unauthenticated: 5 requests per 30 seconds -> sleep 6.5s to be safe.
	// Authenticated: 50 requests per 30 seconds -> sleep 1s to be safe.
	sleepDuration := 6500 * time.Millisecond
	if apiKey != "" {
		sleepDuration = 1 * time.Second
	}

	var total int
	page := 0

	// FIX 3: Incremental Checkpointing.
	// Track the latest modification date seen in this run. If we fail on page 15,
	// we save the date from page 14 so the next run doesn't start from page 1 again.
	latestModDateSeen := startTime

	for startIndex := 0; ; startIndex += 2000 {
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

		color.Blue("NVD: fetching page %d (start: %s)...", page, lastModStart)

		req, err := http.NewRequest("GET", requestURL, nil)
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
			// Save checkpoint before failing
			setMeta(db, "nvd_last_mod_date", latestModDateSeen.Format(layout))
			return fmt.Errorf("NVD network error (timeout or blocked): %w. Checkpoint saved at %s", err, latestModDateSeen.Format(layout))
		}

		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
			color.Yellow("NVD rate limited on page %d; sleeping 30s and retrying...", page)
			time.Sleep(30 * time.Second)
			startIndex -= 2000 // Retry this page
			continue
		}

		if resp.StatusCode != http.StatusOK {
			setMeta(db, "nvd_last_mod_date", latestModDateSeen.Format(layout))
			return fmt.Errorf("NVD API returned %d: %s", resp.StatusCode, string(bodyBytes[:min(len(bodyBytes), 200)]))
		}

		var nvdResp nvdResponse
		if err := json.Unmarshal(bodyBytes, &nvdResp); err != nil {
			setMeta(db, "nvd_last_mod_date", latestModDateSeen.Format(layout))
			return fmt.Errorf("decode NVD (possible Cloudflare block): %w", err)
		}

		if page == 1 && nvdResp.TotalResults > 0 {
			totalPages := (nvdResp.TotalResults + 1999) / 2000
			color.Cyan("NVD: Found %d total CVEs in this window (~%d pages)", nvdResp.TotalResults, totalPages)
		}

		for _, vuln := range nvdResp.Vulnerabilities {
			cveID := vuln.CVE.ID
			if cveID == "" {
				continue
			}

			// Track checkpoint
			if vuln.CVE.LastModified != "" {
				if t, err := time.Parse(layout, vuln.CVE.LastModified); err == nil {
					if t.After(latestModDateSeen) {
						latestModDateSeen = t
					}
				} else if t, err := time.Parse("2006-01-02T15:04:05.000Z", vuln.CVE.LastModified); err == nil {
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
			} else if len(vuln.CVE.Metrics.CVSSMetricV30) > 0 {
				cvss = vuln.CVE.Metrics.CVSSMetricV30[0].CVSSData.BaseScore
			} else if len(vuln.CVE.Metrics.CVSSMetricV2) > 0 {
				cvss = vuln.CVE.Metrics.CVSSMetricV2[0].CVSSData.BaseScore
			}
			raw, _ := json.Marshal(vuln)
			_, err := db.Exec(`INSERT OR REPLACE INTO cve(id, description, cvss_score, published_date, last_modified_date, raw_json)
				VALUES(?,?,?,?,?,?)`,
				cveID, desc, cvss,
				vuln.CVE.Published, vuln.CVE.LastModified, string(raw))
			if err != nil {
				return fmt.Errorf("insert CVE %s: %w", cveID, err)
			}
			total++
		}

		color.Green("NVD: page %d processed (%d CVEs so far)", page, total)

		if nvdResp.TotalResults == 0 || startIndex+2000 >= nvdResp.TotalResults {
			break
		}

		// FIX 4: Polite delay to respect rate limits
		time.Sleep(sleepDuration)
	}

	// Save the final checkpoint
	setMeta(db, "nvd_last_mod_date", latestModDateSeen.Format(layout))
	color.Green("NVD: %d CVEs updated successfully", total)
	return nil
}

// updateKEV fetches and replaces KEV entries.
func updateKEV(db *sql.DB) error {
	client := &http.Client{Timeout: defaultTimeout}
	resp, err := client.Get(kevURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("KEV API returned %d", resp.StatusCode)
	}

	var kevData struct {
		Vulnerabilities []struct {
			CVEID          string `json:"cveID"`
			VendorProduct  string `json:"vendorProject"`
			DateAdded      string `json:"dateAdded"`
			RequiredAction string `json:"requiredAction"`
			DueDate        string `json:"dueDate"`
			Notes          string `json:"notes"`
		} `json:"vulnerabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&kevData); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM kev")
	for _, v := range kevData.Vulnerabilities {
		_, err := tx.Exec(`INSERT OR REPLACE INTO kev(cve_id, title, date_added, required_action, due_date, notes)
    		VALUES(?,?,?,?,?,?)`, v.CVEID, v.VendorProduct, v.DateAdded, v.RequiredAction, v.DueDate, v.Notes)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	color.Green("KEV: %d entries loaded", len(kevData.Vulnerabilities))
	return nil
}

// updateExploitDB parses the CSV and replaces all exploit entries.
func updateExploitDB(db *sql.DB) error {
	client := &http.Client{Timeout: defaultTimeout}
	resp, err := client.Get(exploitCSVURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Exploit-DB CSV returned %d", resp.StatusCode)
	}

	reader := csv.NewReader(resp.Body)
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
	// Find column indices
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.TrimSpace(h)] = i
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM exploit")
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
		description := get("description")
		platform := get("platform")
		typ := get("type")
		datePub := get("date_published")
		author := get("author")
		cveID := get("cve_id")
		_, err := tx.Exec(`INSERT OR REPLACE INTO exploit(edb_id, cve_id, description, platform, type, date_published, author, raw_text)
			VALUES(?,?,?,?,?,?,?,?)`,
			edbID, cveID, description, platform, typ, datePub, author, description)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	color.Green("Exploit-DB: %d exploits loaded", len(records)-1)
	return nil
}

// updateMITRE fetches and parses MITRE ATT&CK STIX.
func updateMITRE(db *sql.DB) error {
	client := &http.Client{Timeout: defaultTimeout}
	resp, err := client.Get(mitreSTIXURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("MITRE STIX returned %d", resp.StatusCode)
	}

	var bundle struct {
		Objects []json.RawMessage `json:"objects"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 50<<20)).Decode(&bundle); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM mitre_technique")
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

			_, err := tx.Exec(`INSERT OR REPLACE INTO mitre_technique(technique_id, name, description, platform, detection)
				VALUES(?,?,?,?,?)`, techID, name, desc, platforms, detection)
			if err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	color.Green("MITRE: techniques updated")
	return nil
}

// Helper to convert []interface{} to []string
func interfaceSliceToString(in []interface{}) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = fmt.Sprint(v)
	}
	return out
}

// generateEmbeddings batches and stores OpenAI embeddings for new records.
func generateEmbeddings(db *sql.DB) error {
	if !ai.HasOpenAIKey() {
		color.Yellow("No OpenAI key configured; skipping embedding generation.")
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
		rows.Close()
		if len(batch) == 0 {
			continue
		}

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

// NVD response structures – careful to match actual API 2.0 response.
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
