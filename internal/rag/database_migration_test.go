// internal/rag/database_migration_test.go
// Purpose: Verify schema versioning records the baseline on fresh databases.
package rag

import "testing"

// TestMigrateRecordsBaselineSchemaVersion ensures migrate() stamps v1.
//
// Args:
//   - t: test runner.
//
// Returns: none.
// Complexity: O(1) SQL statements.
func TestMigrateRecordsBaselineSchemaVersion(t *testing.T) {
	db := newTestDB(t)

	if v := SchemaVersion(db); v != currentSchemaVersion {
		t.Fatalf("expected schema version %d after migrate, got %d", currentSchemaVersion, v)
	}
}

// TestSchemaVersionNilDB ensures the accessor is nil-safe.
//
// Args:
//   - t: test runner.
//
// Returns: none.
// Complexity: O(1).
func TestSchemaVersionNilDB(t *testing.T) {
	if v := SchemaVersion(nil); v != 0 {
		t.Fatalf("expected 0 for nil db, got %d", v)
	}
}
