// internal/rag/bootstrap.go
package rag

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"helix/internal/utils"
)

var ErrOffline = errors.New("offline: knowledge update requires internet connectivity")

const metaKnowledgeUpdated = "knowledge_last_update"

func KnowledgeBootstrap(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database not initialized")
	}
	if getMeta(db, metaKnowledgeUpdated) != "" {
		return nil
	}
	if !utils.IsOnline(3 * time.Second) {
		return ErrOffline
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	// FIX: Pass `false` for interactive (background bootstraps never prompt).
	if err := UpdateAll(ctx, db, false); err != nil {
		return err
	}
	setMeta(db, metaKnowledgeUpdated, time.Now().UTC().Format(time.RFC3339))
	return nil
}

// KnowledgeLastUpdate returns the last successful knowledge update timestamp.
//
// Args:
//   - db: open knowledge database handle.
//
// Returns:
//   - RFC3339 timestamp or empty string.
//
// Complexity: O(1) SQL query bounded by timeout.
func KnowledgeLastUpdate(db *sql.DB) string {
	if db == nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var last string
	err := db.QueryRowContext(
		ctx,
		`SELECT value FROM meta WHERE key = ?`,
		metaKnowledgeUpdated,
	).Scan(&last)

	if err != nil {
		return ""
	}

	return last
}
