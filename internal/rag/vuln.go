// internal/rag/vuln.go
// Purpose: Defensive vulnerability intelligence lookup and hydration.
// Phase 0 safety requirement:
//   - no exploit recommendation,
//   - no attack execution guidance,
//   - focus on CVE, CVSS, KEV, detection, and patch prioritization.
package rag

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// VulnIntel is a defensive vulnerability intelligence record.
type VulnIntel struct {
	ID            string   `json:"id"`
	SourceType    string   `json:"source_type"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	CVSS          float64  `json:"cvss"`
	KEV           bool     `json:"kev"`
	KEVAction     string   `json:"kev_action"`
	KEVDueDate    string   `json:"kev_due_date"`
	Platform      string   `json:"platform"`
	Detection     string   `json:"detection"`
	PatchGuidance string   `json:"patch_guidance"`
	References    []string `json:"references"`
	Score         float64  `json:"score"`
}

// NormalizeVulnID normalizes CVE, EDB, and MITRE identifiers.
func NormalizeVulnID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	upper := strings.ToUpper(raw)

	if strings.HasPrefix(upper, "CVE-") {
		return upper
	}

	if strings.HasPrefix(upper, "EDB-") {
		id := strings.TrimPrefix(upper, "EDB-")
		id = strings.TrimSpace(id)
		return "EDB-" + id
	}

	if isDigits(raw) {
		return "EDB-" + raw
	}

	if strings.HasPrefix(upper, "T") && len(upper) > 1 {
		return upper
	}

	return raw
}

// LookupVulnByID performs exact defensive lookup by identifier.
func LookupVulnByID(db *sql.DB, raw string) ([]VulnIntel, error) {
	id := NormalizeVulnID(raw)
	if id == "" {
		return nil, nil
	}

	var out []VulnIntel

	switch {
	case strings.HasPrefix(id, "CVE-"):
		var description string
		var cvss float64

		err := db.QueryRow(`
			SELECT description, cvss_score
			FROM cve
			WHERE id=?
		`, id).Scan(&description, &cvss)

		if err == nil {
			kev, action, due := getKEV(db, id)

			out = append(out, VulnIntel{
				ID:            id,
				SourceType:    "cve",
				Title:         id,
				Description:   description,
				CVSS:          cvss,
				KEV:           kev,
				KEVAction:     action,
				KEVDueDate:    due,
				Detection:     safeDetectionFallback("cve"),
				PatchGuidance: defensivePatchGuidance(cvss, kev, action, due),
				References: []string{
					"https://nvd.nist.gov/vuln/detail/" + id,
				},
			})
		}

		// Add related exploit references as defensive evidence.
		rows, err := db.Query(`
			SELECT edb_id, cve_id, description, platform, type
			FROM exploit
			WHERE cve_id=?
		`, id)
		if err == nil {
			defer func() { _ = rows.Close() }()

			for rows.Next() {
				var edbID, cveID, description, platform, typ string

				if err := rows.Scan(&edbID, &cveID, &description, &platform, &typ); err != nil {
					continue
				}

				out = append(out, VulnIntel{
					ID:            "EDB-" + edbID,
					SourceType:    "exploit",
					Title:         fmt.Sprintf("Exploit reference EDB-%s", edbID),
					Description:   description,
					Platform:      platform,
					Detection:     safeDetectionFallback("exploit"),
					PatchGuidance: defensivePatchGuidance(0, false, "", ""),
					References: []string{
						"https://www.exploit-db.com/exploits/" + edbID,
					},
				})
			}
		}

	case strings.HasPrefix(id, "EDB-"):
		edb := strings.TrimPrefix(id, "EDB-")

		var edbID, cveID, description, platform, typ string

		err := db.QueryRow(`
			SELECT edb_id, cve_id, description, platform, type
			FROM exploit
			WHERE edb_id=? OR edb_id=?
			LIMIT 1
		`, edb, id).Scan(&edbID, &cveID, &description, &platform, &typ)

		if err == nil {
			cvss := 0.0
			kev := false
			kevAction := ""
			kevDue := ""

			if cveID != "" {
				_ = db.QueryRow(`
					SELECT cvss_score FROM cve WHERE id=?
				`, cveID).Scan(&cvss)

				kev, kevAction, kevDue = getKEV(db, cveID)
			}

			out = append(out, VulnIntel{
				ID:          "EDB-" + edbID,
				SourceType:  "exploit",
				Title:       fmt.Sprintf("Exploit reference EDB-%s", edbID),
				Description: description,
				CVSS:        cvss,
				KEV:         kev,
				KEVAction:   kevAction,
				KEVDueDate:  kevDue,
				Platform:    platform,
				Detection:   safeDetectionFallback("exploit"),
				PatchGuidance: defensivePatchGuidance(
					cvss,
					kev,
					kevAction,
					kevDue,
				),
				References: []string{
					"https://www.exploit-db.com/exploits/" + edbID,
				},
			})
		}

	case strings.HasPrefix(id, "T"):
		var techniqueID, name, description, platform, detection string

		err := db.QueryRow(`
			SELECT technique_id, name, description, platform, detection
			FROM mitre_technique
			WHERE technique_id=?
		`, id).Scan(&techniqueID, &name, &description, &platform, &detection)

		if err == nil {
			if detection == "" {
				detection = safeDetectionFallback("mitre")
			}

			out = append(out, VulnIntel{
				ID:            techniqueID,
				SourceType:    "mitre",
				Title:         name,
				Description:   description,
				Platform:      platform,
				Detection:     detection,
				PatchGuidance: "Apply MITRE ATT&CK detection engineering controls and vendor hardening guidance.",
				References: []string{
					"https://attack.mitre.org/techniques/" + strings.ReplaceAll(techniqueID, ".", "/"),
				},
			})
		}
	}

	if len(out) == 0 {
		return nil, nil
	}

	return out, nil
}

// SearchVulns searches defensive vulnerability intelligence.
func SearchVulns(db *sql.DB, query string, limit int) ([]VulnIntel, error) {
	entries, err := SemanticSearch(db, query, limit*3)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	out := make([]VulnIntel, 0, len(entries))

	for _, entry := range entries {
		vuln, err := hydrateKnowledgeEntry(db, entry)
		if err != nil {
			vuln = VulnIntel{
				ID:            entry.SourceID,
				SourceType:    entry.SourceType,
				Title:         entry.Title,
				Description:   entry.Description,
				Detection:     safeDetectionFallback(entry.SourceType),
				PatchGuidance: defensivePatchGuidance(0, false, "", ""),
				Score:         entry.Score,
			}
		}

		key := vuln.SourceType + ":" + vuln.ID
		if seen[key] {
			continue
		}

		seen[key] = true
		out = append(out, vuln)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}

		return out[i].ID < out[j].ID
	})

	if len(out) > limit {
		out = out[:limit]
	}

	return out, nil
}

// hydrateKnowledgeEntry converts a raw search entry into defensive intel.
func hydrateKnowledgeEntry(db *sql.DB, e KnowledgeEntry) (VulnIntel, error) {
	v := VulnIntel{
		ID:         e.SourceID,
		SourceType: e.SourceType,
		Title:      e.Title,
		Score:      e.Score,
	}

	switch e.SourceType {
	case "cve":
		var description string
		var cvss float64

		err := db.QueryRow(`
			SELECT description, cvss_score
			FROM cve
			WHERE id=?
		`, e.SourceID).Scan(&description, &cvss)
		if err != nil {
			return v, err
		}

		kev, action, due := getKEV(db, e.SourceID)

		v.Description = description
		v.CVSS = cvss
		v.KEV = kev
		v.KEVAction = action
		v.KEVDueDate = due
		v.Detection = safeDetectionFallback("cve")
		v.PatchGuidance = defensivePatchGuidance(cvss, kev, action, due)
		v.References = []string{
			"https://nvd.nist.gov/vuln/detail/" + e.SourceID,
		}

	case "kev":
		var title, action, due, notes string

		err := db.QueryRow(`
			SELECT title, required_action, due_date, notes
			FROM kev
			WHERE cve_id=?
		`, e.SourceID).Scan(&title, &action, &due, &notes)
		if err != nil {
			return v, err
		}

		cvss := 0.0
		_ = db.QueryRow(`
			SELECT cvss_score FROM cve WHERE id=?
		`, e.SourceID).Scan(&cvss)

		v.ID = e.SourceID
		v.Title = title
		v.Description = notes
		v.CVSS = cvss
		v.KEV = true
		v.KEVAction = action
		v.KEVDueDate = due
		v.Detection = safeDetectionFallback("kev")
		v.PatchGuidance = defensivePatchGuidance(cvss, true, action, due)
		v.References = []string{
			"https://www.cisa.gov/known-exploited-vulnerabilities-catalog",
			"https://nvd.nist.gov/vuln/detail/" + e.SourceID,
		}

	case "exploit":
		var edbID, cveID, description, platform, typ string

		err := db.QueryRow(`
			SELECT edb_id, cve_id, description, platform, type
			FROM exploit
			WHERE edb_id=? OR edb_id=?
			LIMIT 1
		`, e.SourceID, NormalizeVulnID(e.SourceID)).Scan(&edbID, &cveID, &description, &platform, &typ)
		if err != nil {
			return v, err
		}

		cvss := 0.0
		kev := false
		kevAction := ""
		kevDue := ""

		if cveID != "" {
			_ = db.QueryRow(`
				SELECT cvss_score FROM cve WHERE id=?
			`, cveID).Scan(&cvss)

			kev, kevAction, kevDue = getKEV(db, cveID)
		}

		v.ID = "EDB-" + edbID
		v.Title = fmt.Sprintf("Exploit reference EDB-%s", edbID)
		v.Description = description
		v.CVSS = cvss
		v.KEV = kev
		v.KEVAction = kevAction
		v.KEVDueDate = kevDue
		v.Platform = platform
		v.Detection = safeDetectionFallback("exploit")
		v.PatchGuidance = defensivePatchGuidance(cvss, kev, kevAction, kevDue)
		v.References = []string{
			"https://www.exploit-db.com/exploits/" + edbID,
		}

	case "mitre":
		var techniqueID, name, description, platform, detection string

		err := db.QueryRow(`
			SELECT technique_id, name, description, platform, detection
			FROM mitre_technique
			WHERE technique_id=?
		`, e.SourceID).Scan(&techniqueID, &name, &description, &platform, &detection)
		if err != nil {
			return v, err
		}

		if detection == "" {
			detection = safeDetectionFallback("mitre")
		}

		v.ID = techniqueID
		v.Title = name
		v.Description = description
		v.Platform = platform
		v.Detection = detection
		v.PatchGuidance = "Apply MITRE ATT&CK detection engineering controls and vendor hardening guidance."
		v.References = []string{
			"https://attack.mitre.org/techniques/" + strings.ReplaceAll(techniqueID, ".", "/"),
		}

	default:
		v.Description = e.Description
		v.Detection = safeDetectionFallback(e.SourceType)
		v.PatchGuidance = defensivePatchGuidance(0, false, "", "")
	}

	return v, nil
}

// getKEV checks whether a CVE appears in CISA KEV.
func getKEV(db *sql.DB, cveID string) (bool, string, string) {
	var action, due string

	err := db.QueryRow(`
		SELECT required_action, due_date
		FROM kev
		WHERE cve_id=?
	`, cveID).Scan(&action, &due)
	if err != nil {
		return false, "", ""
	}

	return true, action, due
}

// defensivePatchGuidance returns safe defensive remediation guidance.
func defensivePatchGuidance(cvss float64, kev bool, kevAction, kevDue string) string {
	if kev && strings.TrimSpace(kevAction) != "" {
		guidance := kevAction
		if strings.TrimSpace(kevDue) != "" {
			guidance += fmt.Sprintf(" CISA due date: %s.", kevDue)
		}
		return guidance
	}

	switch {
	case cvss >= 9.0:
		return "Critical severity: apply vendor security updates immediately and verify affected assets."
	case cvss >= 7.0:
		return "High severity: prioritize vendor patching and review exposure."
	case cvss >= 4.0:
		return "Medium severity: schedule vendor patching and monitor advisories."
	case cvss > 0:
		return "Low severity: apply vendor updates during normal maintenance."
	default:
		return "Apply vendor security guidance and monitor trusted advisories."
	}
}

// safeDetectionFallback returns defensive detection guidance.
func safeDetectionFallback(sourceType string) string {
	switch sourceType {
	case "cve":
		return "Monitor vendor advisories, vulnerability scanners, and asset inventory for affected versions."
	case "kev":
		return "Treat as actively exploited: alert on affected products and validate patch completion."
	case "exploit":
		return "Use exploit references defensively to validate detection coverage and patch status."
	case "mitre":
		return "Map logs and telemetry to MITRE ATT&CK data sources and validate detection rules."
	default:
		return "Review trusted security telemetry and vendor guidance."
	}
}

// isDigits reports whether s contains only ASCII digits.
func isDigits(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}

	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}
