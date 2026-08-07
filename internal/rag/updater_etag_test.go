// internal/rag/updater_etag_test.go
// Purpose: Verify conditional-GET (ETag) caching for threat feed syncs.
package rag

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFetchConditional304 verifies If-None-Match round-trips and 304 handling.
//
// Args:
//   - t: test runner.
//
// Returns: none.
// Complexity: O(1) HTTP round trips.
func TestFetchConditional304(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(`{"vulnerabilities":[]}`))
	}))
	defer server.Close()

	client := server.Client()

	body, etag, changed, err := fetchConditional(context.Background(), client, server.URL, "")
	if err != nil {
		t.Fatalf("first fetch failed: %v", err)
	}
	if !changed || etag != `"v1"` || len(body) == 0 {
		t.Fatalf("expected changed payload with etag, got changed=%v etag=%q body=%d", changed, etag, len(body))
	}

	_, _, changed2, err2 := fetchConditional(context.Background(), client, server.URL, `"v1"`)
	if err2 != nil {
		t.Fatalf("conditional fetch failed: %v", err2)
	}
	if changed2 {
		t.Fatal("expected 304 to report unchanged payload")
	}
}

// TestUpdateKEVHonors304 verifies a 304 preserves existing rows and ETag.
//
// Args:
//   - t: test runner.
//
// Returns: none.
// Complexity: O(1) SQL statements + HTTP round trips.
func TestUpdateKEVHonors304(t *testing.T) {
	db := newTestDB(t)
	if _, err := db.Exec(`INSERT INTO kev(cve_id, title) VALUES('CVE-1','old')`); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	setMeta(db, metaETagKEV, `"k"`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"k"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(`{"vulnerabilities":[{"cveID":"CVE-2"}]}`))
	}))
	defer server.Close()

	oldURL := kevURL
	kevURL = server.URL
	t.Cleanup(func() { kevURL = oldURL })

	if err := updateKEV(context.Background(), db); err != nil {
		t.Fatalf("updateKEV with 304 failed: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM kev`).Scan(&count); err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected existing row preserved on 304, got %d rows", count)
	}
	if got := getMeta(db, metaETagKEV); got != `"k"` {
		t.Fatalf("expected ETag unchanged on 304, got %q", got)
	}
}

// TestUpdateKEVStoresETagOn200 verifies the ETag persists after a commit.
//
// Args:
//   - t: test runner.
//
// Returns: none.
// Complexity: O(1) SQL statements + HTTP round trips.
func TestUpdateKEVStoresETagOn200(t *testing.T) {
	db := newTestDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"k2"`)
		_, _ = w.Write([]byte(`{"vulnerabilities":[{"cveID":"CVE-9","vendorProject":"Vendor"}]}`))
	}))
	defer server.Close()

	oldURL := kevURL
	kevURL = server.URL
	t.Cleanup(func() { kevURL = oldURL })

	if err := updateKEV(context.Background(), db); err != nil {
		t.Fatalf("updateKEV with 200 failed: %v", err)
	}

	if got := getMeta(db, metaETagKEV); got != `"k2"` {
		t.Fatalf("expected stored ETag %q, got %q", `"k2"`, got)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM kev`).Scan(&count); err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 KEV row after sync, got %d", count)
	}
}
