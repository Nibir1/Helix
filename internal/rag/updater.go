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
	"helix/internal/utils"

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

// Helper to convert []interface{} to []string
func interfaceSliceToString(in []interface{}) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = fmt.Sprint(v)
	}
	return out
}

// generateEmbeddings batches and stores OpenAI embeddings for new records.
func generateEmbeddings(ctx context.Context, db *sql.DB) error {
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
		_ = rows.Close()
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

// UpdateAll fetches all data sources and updates the database.
// Phase 4: internet-gated, fast-sources-first, stage-hooked, silent unless
// HELIX_DEBUG=1.
func UpdateAll(ctx context.Context, db *sql.DB) error {
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

	notifyStage("FETCHING EXPLOIT-DB")
	if err := updateExploitDB(ctx, db); err != nil && utils.IsDebugMode() {
		color.Yellow("Exploit-DB update failed: %v", err)
	}

	notifyStage("FETCHING MITRE ATT&CK")
	if err := updateMITRE(ctx, db); err != nil && utils.IsDebugMode() {
		color.Yellow("MITRE update failed: %v", err)
	}

	// NVD last: it is the slowest feed (rate-limited) and checkpoints itself,
	// so a cancellation resumes on the next run.
	notifyStage("FETCHING NVD CVES")
	if err := updateNVD(ctx, db); err != nil && utils.IsDebugMode() {
		color.Yellow("NVD update skipped/failed: %v", err)
	}

	notifyStage("GENERATING EMBEDDINGS")
	if err := generateEmbeddings(ctx, db); err != nil && utils.IsDebugMode() {
		color.Yellow("Embedding generation failed: %v", err)
	}

	notifyStage("REBUILDING FTS INDEX")
	if err := ReindexKnowledgeFTS(db); err != nil && utils.IsDebugMode() {
		color.Yellow("FTS reindex failed: %v", err)
	}

	notifyStage("KNOWLEDGE UPDATE COMPLETE")
	if utils.IsDebugMode() {
		color.Green("Knowledge base update completed in %v", time.Since(start))
	}
	return nil
}

// updateNVD uses NVD API 2.0 with strict timeouts, browser spoofing, rate-limiting, and checkpointing.
// Phase 0 Hardening: Uses http.NewRequestWithContext for cancellation support.
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

	for startIndex := 0; ; startIndex += 2000 {
		// Check for context cancellation before starting a new page
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
		notifyStage(fmt.Sprintf("FETCHING NVD CVES · PAGE %d", page))
		if utils.IsDebugMode() {
			color.Blue("NVD: fetching page %d (start: %s)...", page, lastModStart)
		}

		// HARDENING: Use NewRequestWithContext
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
			return fmt.Errorf("NVD network error: %w. Checkpoint saved at %s", err, latestModDateSeen.Format(layout))
		}

		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
			notifyStage(fmt.Sprintf("NVD RATE LIMITED · RETRY PAGE %d", page))
			if utils.IsDebugMode() {
				color.Yellow("NVD rate limited on page %d; sleeping 30s and retrying...", page)
			}
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
			return fmt.Errorf("decode NVD: %w", err)
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

		if nvdResp.TotalResults == 0 || startIndex+2000 >= nvdResp.TotalResults {
			break
		}
		time.Sleep(sleepDuration)
	}

	setMeta(db, "nvd_last_mod_date", latestModDateSeen.Format(layout))
	color.Green("NVD: %d CVEs updated successfully", total)
	return nil
}

// updateKEV fetches and replaces KEV entries.
// Phase 0 Hardening: Context-aware HTTP request.
func updateKEV(ctx context.Context, db *sql.DB) error {
	client := &http.Client{Timeout: defaultTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kevURL, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

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
	defer func() { _ = tx.Rollback() }()
	_, _ = tx.Exec("DELETE FROM kev")

	for _, v := range kevData.Vulnerabilities {
		_, err := tx.Exec(`INSERT OR REPLACE INTO kev(cve_id, title, date_added, required_action, due_date, notes) VALUES(?,?,?,?,?,?)`,
			v.CVEID, v.VendorProduct, v.DateAdded, v.RequiredAction, v.DueDate, v.Notes)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// updateExploitDB parses the CSV and replaces all exploit entries.
// Phase 0 Hardening: Context-aware HTTP request.
func updateExploitDB(ctx context.Context, db *sql.DB) error {
	client := &http.Client{Timeout: defaultTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, exploitCSVURL, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

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
	}
	return tx.Commit()
}

// updateMITRE fetches and parses MITRE ATT&CK STIX.
// Phase 0 Hardening: Context-aware HTTP request.
func updateMITRE(ctx context.Context, db *sql.DB) error {
	client := &http.Client{Timeout: defaultTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mitreSTIXURL, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

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
	defer func() { _ = tx.Rollback() }()
	_, _ = tx.Exec("DELETE FROM mitre_technique")

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
		}
	}
	return tx.Commit()
}

// OnUpdateStage is an optional hook invoked at each update stage so callers
// (e.g. /knowledge-update) can drive a live Helix progress bar. Nil-safe.
var OnUpdateStage func(stage string)

func notifyStage(stage string) {
	if OnUpdateStage != nil {
		OnUpdateStage(stage)
	}
}
