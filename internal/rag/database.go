// internal/rag/database.go
package rag

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
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

// OpenDB opens or creates the SQLite database in the Helix config directory.
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
	db.SetMaxOpenConns(1) // SQLite works best with single writer
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	for _, t := range triggers {
		if _, err := db.Exec(t); err != nil {
			return fmt.Errorf("apply trigger: %w", err)
		}
	}
	return nil
}
