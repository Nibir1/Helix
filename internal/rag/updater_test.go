// internal/rag/updater_test.go
package rag

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUpdateNVDSuccess(t *testing.T) {
	db := newTestDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		_, _ = w.Write([]byte(`{
			"totalResults": 1,
			"vulnerabilities": [
				{
					"cve": {
						"id": "CVE-2023-0001",
						"published": "2023-01-01T00:00:00.000",
						"lastModified": "2023-01-02T00:00:00.000",
						"descriptions": [
							{
								"lang": "en",
								"value": "Test CVE"
							}
						],
						"metrics": {
							"cvssMetricV31": [
								{
									"cvssData": {
										"baseScore": 9.8
									}
								}
							]
						}
					}
				}
			]
		}`))
	}))
	defer server.Close()

	oldURL := nvdBaseURL
	oldAttempts := nvdRetryAttempts
	oldDelay := nvdRetryBaseDelay

	nvdBaseURL = server.URL
	nvdRetryAttempts = 1
	nvdRetryBaseDelay = time.Millisecond

	t.Cleanup(func() {
		nvdBaseURL = oldURL
		nvdRetryAttempts = oldAttempts
		nvdRetryBaseDelay = oldDelay
	})

	if err := updateNVD(context.Background(), db); err != nil {
		t.Fatalf("updateNVD failed: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cve`).Scan(&count); err != nil {
		t.Fatalf("failed to count CVE rows: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected 1 CVE row, got %d", count)
	}
}

func TestUpdateNVD404(t *testing.T) {
	db := newTestDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	defer server.Close()

	oldURL := nvdBaseURL
	oldAttempts := nvdRetryAttempts
	oldDelay := nvdRetryBaseDelay

	nvdBaseURL = server.URL
	nvdRetryAttempts = 1
	nvdRetryBaseDelay = time.Millisecond

	t.Cleanup(func() {
		nvdBaseURL = oldURL
		nvdRetryAttempts = oldAttempts
		nvdRetryBaseDelay = oldDelay
	})

	err := updateNVD(context.Background(), db)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}

	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got: %v", err)
	}
}
