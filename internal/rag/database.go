// internal/rag/database.go
package rag

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver with FTS5
)

const (
	dbFileName = "helix.db"
)

var schema = `
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT
);

CREATE TABLE IF NOT EXISTS cve (
    id                TEXT PRIMARY KEY,
    description       TEXT,
    cvss_score        REAL,
    published_date    TEXT,
    last_modified_date TEXT,
    raw_json          TEXT
);

CREATE TABLE IF NOT EXISTS kev (
    cve_id          TEXT PRIMARY KEY,
    title           TEXT,
    date_added      TEXT,
    required_action TEXT,
    due_date        TEXT,
    notes           TEXT
);

CREATE TABLE IF NOT EXISTS exploit (
    edb_id          TEXT PRIMARY KEY,
    cve_id          TEXT,
    description     TEXT,
    platform        TEXT,
    type            TEXT,
    date_published  TEXT,
    author          TEXT,
    raw_text        TEXT
);

CREATE TABLE IF NOT EXISTS mitre_technique (
    technique_id TEXT PRIMARY KEY,
    name         TEXT,
    description  TEXT,
    platform     TEXT,
    detection    TEXT,
    data_sources TEXT
);

-- Full-text search (standalone, no external content)
CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_fts USING fts5(
    source_type,
    source_id,
    title,
    description
);

CREATE TABLE IF NOT EXISTS embeddings (
    source_type TEXT,
    source_id   TEXT,
    model       TEXT,
    embedding   BLOB,
    PRIMARY KEY (source_type, source_id)
);
`

var triggers = []string{
	`CREATE TRIGGER IF NOT EXISTS cve_ai AFTER INSERT ON cve BEGIN
        INSERT INTO knowledge_fts(source_type, source_id, title, description)
        VALUES ('cve', new.id, new.id, new.description);
    END;`,
	`CREATE TRIGGER IF NOT EXISTS cve_ad AFTER DELETE ON cve BEGIN
        DELETE FROM knowledge_fts WHERE source_type='cve' AND source_id=old.id;
    END;`,
	`CREATE TRIGGER IF NOT EXISTS cve_au AFTER UPDATE ON cve BEGIN
        DELETE FROM knowledge_fts WHERE source_type='cve' AND source_id=old.id;
        INSERT INTO knowledge_fts(source_type, source_id, title, description)
        VALUES ('cve', new.id, new.id, new.description);
    END;`,
	`CREATE TRIGGER IF NOT EXISTS kev_ai AFTER INSERT ON kev BEGIN
        INSERT INTO knowledge_fts(source_type, source_id, title, description)
        VALUES ('kev', new.cve_id, new.title, new.notes);
    END;`,
	`CREATE TRIGGER IF NOT EXISTS kev_ad AFTER DELETE ON kev BEGIN
        DELETE FROM knowledge_fts WHERE source_type='kev' AND source_id=old.cve_id;
    END;`,
	`CREATE TRIGGER IF NOT EXISTS kev_au AFTER UPDATE ON kev BEGIN
        DELETE FROM knowledge_fts WHERE source_type='kev' AND source_id=old.cve_id;
        INSERT INTO knowledge_fts(source_type, source_id, title, description)
        VALUES ('kev', new.cve_id, new.title, new.notes);
    END;`,
	`CREATE TRIGGER IF NOT EXISTS exploit_ai AFTER INSERT ON exploit BEGIN
        INSERT INTO knowledge_fts(source_type, source_id, title, description)
        VALUES ('exploit', new.edb_id, new.edb_id, new.description);
    END;`,
	`CREATE TRIGGER IF NOT EXISTS exploit_ad AFTER DELETE ON exploit BEGIN
        DELETE FROM knowledge_fts WHERE source_type='exploit' AND source_id=old.edb_id;
    END;`,
	`CREATE TRIGGER IF NOT EXISTS exploit_au AFTER UPDATE ON exploit BEGIN
        DELETE FROM knowledge_fts WHERE source_type='exploit' AND source_id=old.edb_id;
        INSERT INTO knowledge_fts(source_type, source_id, title, description)
        VALUES ('exploit', new.edb_id, new.edb_id, new.description);
    END;`,
	`CREATE TRIGGER IF NOT EXISTS mitre_ai AFTER INSERT ON mitre_technique BEGIN
        INSERT INTO knowledge_fts(source_type, source_id, title, description)
        VALUES ('mitre', new.technique_id, new.name, new.description);
    END;`,
	`CREATE TRIGGER IF NOT EXISTS mitre_ad AFTER DELETE ON mitre_technique BEGIN
        DELETE FROM knowledge_fts WHERE source_type='mitre' AND source_id=old.technique_id;
    END;`,
	`CREATE TRIGGER IF NOT EXISTS mitre_au AFTER UPDATE ON mitre_technique BEGIN
        DELETE FROM knowledge_fts WHERE source_type='mitre' AND source_id=old.technique_id;
        INSERT INTO knowledge_fts(source_type, source_id, title, description)
        VALUES ('mitre', new.technique_id, new.name, new.description);
    END;`,
}

// schemaVersionKey is the meta key recording the applied schema version.
const schemaVersionKey = "schema_version"

// currentSchemaVersion is the newest schema version this build understands.
// Version 1 is the baseline created by `schema` above.
const currentSchemaVersion = 1

// schemaMigration is one ordered, transactional schema upgrade.
type schemaMigration struct {
	version int
	sql     string
}

// migrations holds future ordered schema upgrades. The registry is empty at
// v1 by design: the framework records the baseline now so that every future
// schema change can ship as an appended, transactional migration entry.
var migrations = []schemaMigration{}

// OpenDB opens or creates the SQLite database in the Helix config directory.
//
// Args:
//   - homeDir: user home directory.
//
// Returns:
//   - open database handle or error.
//
// Complexity: O(schema migration time).
func OpenDB(homeDir string) (*sql.DB, error) {
	dir := filepath.Join(homeDir, ".helix")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	dbPath := filepath.Join(dir, dbFileName)

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(5 * time.Minute)

	// Prevent SQLite-level lock waits from becoming indefinite.
	pragmaCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(pragmaCtx, "PRAGMA busy_timeout = 3000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}

	if _, err := db.ExecContext(pragmaCtx, "PRAGMA synchronous = NORMAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set synchronous mode: %w", err)
	}

	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

// migrate applies the base schema, triggers, and ordered schema migrations.
//
// Args:
//   - db: open knowledge database handle.
//
// Returns: error if any schema statement fails.
// Complexity: O(number of schema statements + pending migrations).
func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	for _, t := range triggers {
		if _, err := db.Exec(t); err != nil {
			return fmt.Errorf("apply trigger: %w", err)
		}
	}
	return applySchemaMigrations(db)
}

// applySchemaMigrations runs ordered schema upgrades tracked in meta and
// stamps the baseline version onto fresh or pre-versioning databases.
//
// Args:
//   - db: open knowledge database handle.
//
// Returns: error if a migration statement fails (rolled back cleanly).
// Complexity: O(number of pending migrations).
func applySchemaMigrations(db *sql.DB) error {
	current := 0
	if v := getMeta(db, schemaVersionKey); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			current = n
		}
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d failed: %w", m.version, err)
		}
		if _, err := tx.Exec(
			"INSERT OR REPLACE INTO meta(key, value) VALUES(?,?)",
			schemaVersionKey, strconv.Itoa(m.version),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
		current = m.version
	}

	// Fresh or pre-versioning databases: stamp the baseline version.
	if current < currentSchemaVersion {
		setMeta(db, schemaVersionKey, strconv.Itoa(currentSchemaVersion))
	}
	return nil
}

// SchemaVersion returns the applied schema version (0 when unknown).
//
// Args:
//   - db: open knowledge database handle (nil-safe).
//
// Returns:
//   - int schema version.
//
// Complexity: O(1).
func SchemaVersion(db *sql.DB) int {
	if db == nil {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var value string
	err := db.QueryRowContext(
		ctx,
		`SELECT value FROM meta WHERE key = ?`,
		schemaVersionKey,
	).Scan(&value)

	if err != nil {
		return 0
	}

	if n, err := strconv.Atoi(value); err == nil {
		return n
	}

	return 0
}

// FTSCount returns the number of rows in the FTS index.
func FTSCount(db *sql.DB) (int, error) {
	var count int

	err := db.QueryRow(`SELECT COUNT(*) FROM knowledge_fts`).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// ReindexKnowledgeFTS explicitly rebuilds the FTS index from source tables.
// This is required when triggers did not fire or when repairing an old database.
// The optional progress callback receives (current, total) row counters after
// each source table is reinserted, driving a determinate UI progress bar.
//
// Args:
//   - db: open knowledge database handle.
//   - progress: optional callback (may be nil).
//
// Returns: error if any SQL statement fails.
// Complexity: O(number of FTS rows).
func ReindexKnowledgeFTS(db *sql.DB, progress func(current, total int)) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Row counts per source table drive the determinate progress bar.
	var cveCount, kevCount, exploitCount, mitreCount int
	_ = tx.QueryRow(`SELECT COUNT(*) FROM cve`).Scan(&cveCount)
	_ = tx.QueryRow(`SELECT COUNT(*) FROM kev`).Scan(&kevCount)
	_ = tx.QueryRow(`SELECT COUNT(*) FROM exploit`).Scan(&exploitCount)
	_ = tx.QueryRow(`SELECT COUNT(*) FROM mitre_technique`).Scan(&mitreCount)

	total := cveCount + kevCount + exploitCount + mitreCount
	current := 0
	if progress != nil {
		progress(current, total)
	}

	if _, err := tx.Exec(`DELETE FROM knowledge_fts`); err != nil {
		return err
	}

	if _, err := tx.Exec(`
INSERT INTO knowledge_fts(source_type, source_id, title, description)
SELECT 'cve', id, id, description FROM cve
`); err != nil {
		return err
	}
	current += cveCount
	if progress != nil {
		progress(current, total)
	}

	if _, err := tx.Exec(`
INSERT INTO knowledge_fts(source_type, source_id, title, description)
SELECT 'kev', cve_id, title, notes FROM kev
`); err != nil {
		return err
	}
	current += kevCount
	if progress != nil {
		progress(current, total)
	}

	if _, err := tx.Exec(`
INSERT INTO knowledge_fts(source_type, source_id, title, description)
SELECT 'exploit', edb_id, edb_id, description FROM exploit
`); err != nil {
		return err
	}
	current += exploitCount
	if progress != nil {
		progress(current, total)
	}

	if _, err := tx.Exec(`
INSERT INTO knowledge_fts(source_type, source_id, title, description)
SELECT 'mitre', technique_id, name, description FROM mitre_technique
`); err != nil {
		return err
	}
	current += mitreCount
	if progress != nil {
		progress(current, total)
	}

	return tx.Commit()
}
