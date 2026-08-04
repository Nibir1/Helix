// internal/rag/search_test.go
package rag

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "helix_test.db")
	dsn := "file:" + dbPath + "?_journal_mode=WAL&_foreign_keys=on"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func TestSanitizeFTSQuery(t *testing.T) {
	got := sanitizeFTSQuery("log4j rce!")
	want := `"log4j" OR "rce"`

	if got != want {
		t.Fatalf("sanitizeFTSQuery() = %q, want %q", got, want)
	}
}

func TestFTSReindexAndSearch(t *testing.T) {
	db := newTestDB(t)

	_, err := db.Exec(`
		INSERT INTO cve(id, description, cvss_score)
		VALUES('CVE-2021-44228', 'Apache Log4j2 remote code execution vulnerability', 10.0)
	`)
	if err != nil {
		t.Fatalf("failed to insert CVE: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO exploit(edb_id, cve_id, description, platform, type)
		VALUES('44581', 'CVE-2021-44228', 'Log4j RCE exploit reference', 'multi', 'webapps')
	`)
	if err != nil {
		t.Fatalf("failed to insert exploit: %v", err)
	}

	if err := ReindexKnowledgeFTS(db); err != nil {
		t.Fatalf("failed to reindex FTS: %v", err)
	}

	count, err := FTSCount(db)
	if err != nil {
		t.Fatalf("failed to count FTS rows: %v", err)
	}

	if count < 2 {
		t.Fatalf("expected at least 2 FTS rows, got %d", count)
	}

	results, err := SemanticSearch(db, "log4j", 5)
	if err != nil {
		t.Fatalf("semantic search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected search results for log4j")
	}
}

func TestLookupVulnByID(t *testing.T) {
	db := newTestDB(t)

	_, err := db.Exec(`
		INSERT INTO exploit(edb_id, cve_id, description, platform, type)
		VALUES('44581', 'CVE-2021-44228', 'Log4j RCE exploit reference', 'multi', 'webapps')
	`)
	if err != nil {
		t.Fatalf("failed to insert exploit: %v", err)
	}

	entries, err := LookupVulnByID(db, "EDB-44581")
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("expected exact EDB lookup to return entries")
	}
}
