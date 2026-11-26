package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"insightflow/api/internal/config"
)

func TestHealthz(t *testing.T) {
	handler := New(&config.Config{Env: "test"}, Deps{})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status field = %q, want %q", body["status"], "ok")
	}
}

func TestConfigLimitsIsPublicAndSecretFree(t *testing.T) {
	cfg := &config.Config{
		Env:                     "test",
		MaxUploadCSVBytes:       1024,
		MaxUploadXLSXBytes:      2048,
		MaxDatasetsPerWorkspace: 20,
		MaxDatasetRowsCSV:       1000,
		MaxDatasetRowsXLSX:      500,
		MaxDatasetColumns:       200,
		ProfileTimeoutSeconds:   30,
	}
	handler := New(cfg, Deps{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/limits", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["upload"]["csv_max_bytes"] != float64(1024) {
		t.Fatalf("csv_max_bytes = %v", body["upload"]["csv_max_bytes"])
	}
	if body["auth"]["google_enabled"] != false {
		t.Fatalf("google_enabled = %v, want false without credentials", body["auth"]["google_enabled"])
	}
	if _, leaked := body["auth"]["client_secret"]; leaked {
		t.Fatal("config/limits leaked a secret field")
	}
}
