// internal/rag/bootstrap.go
// Purpose: First-run, background knowledge-base bootstrap for fresh users.
//
// Guarantees:
//   - runs automatically once, in the background, after RAG Initialize(),
//   - is a no-op once a successful update has been recorded (meta key),
//   - skips gracefully (WITHOUT marking done) when offline, so the next
//     online session retries automatically,
//   - stays completely silent so the live prompt is never corrupted.
//
// Dependencies: stdlib + internal/utils (connectivity probe).
package rag

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"helix/internal/utils"
)

// ErrOffline is returned when a network-backed knowledge update is attempted
// without internet connectivity.
var ErrOffline = errors.New("offline: knowledge update requires internet connectivity")

// metaKnowledgeUpdated records the timestamp of the last successful update.
const metaKnowledgeUpdated = "knowledge_last_update"

// KnowledgeBootstrap populates the SQLite knowledge base on first use.
//
// Args: ctx – cancellation/timeout context; db – knowledge database handle.
// Returns: nil when already bootstrapped or on success; ErrOffline when the
// machine is offline (caller should retry later); other errors are surfaced
// for debug logging only.
// Complexity: O(network fetch time), bounded to 15 minutes.
func KnowledgeBootstrap(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database not initialized")
	}
	// Already bootstrapped on a previous session → nothing to do.
	if getMeta(db, metaKnowledgeUpdated) != "" {
		return nil
	}
	// INTERNET GATE: do not attempt API fetches while offline.
	if !utils.IsOnline(3 * time.Second) {
		return ErrOffline
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := UpdateAll(ctx, db); err != nil {
		return err
	}
	setMeta(db, metaKnowledgeUpdated, time.Now().UTC().Format(time.RFC3339))
	return nil
}

// KnowledgeLastUpdate returns the RFC3339 timestamp of the last successful
// knowledge update, or "" if it never happened.
func KnowledgeLastUpdate(db *sql.DB) string {
	if db == nil {
		return ""
	}
	return getMeta(db, metaKnowledgeUpdated)
}
