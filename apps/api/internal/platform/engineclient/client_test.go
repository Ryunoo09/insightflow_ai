package engineclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQueryRowsPostsExplorerRequest(t *testing.T) {
	var gotPath, gotToken, gotRequestID string
	var gotBody QueryRowsRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Internal-Token")
		gotRequestID = r.Header.Get("X-Request-ID")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"columns":[{"name":"a","type":"number"}],"rows":[{"a":1}],"total":7}`))
	}))
	defer server.Close()

	client := New(server.URL, "secret", time.Second)
	out, err := client.QueryRows(context.Background(), "req-1", QueryRowsRequest{
		DatasetRef: DatasetRef{WorkspaceID: "ws", DatasetID: "ds", VersionNo: 2},
		Offset:     0,
		Limit:      100,
		Search:     "jakarta",
	})
	if err != nil {
		t.Fatalf("QueryRows: %v", err)
	}

	if gotPath != "/v1/datasets/rows/query" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotToken != "secret" || gotRequestID != "req-1" {
		t.Fatalf("headers = token %q, request id %q", gotToken, gotRequestID)
	}
	if gotBody.DatasetRef.VersionNo != 2 || gotBody.Search != "jakarta" {
		t.Fatalf("body = %+v", gotBody)
	}
	if len(out.Columns) != 1 || out.Total != 7 {
		t.Fatalf("response = %+v", out)
	}
}

func TestQueryRowsMapsEngineError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":"EXPLORER_INVALID_COLUMN","message":"bad column","retryable":false}}`))
	}))
	defer server.Close()

	client := New(server.URL, "secret", time.Second)
	_, err := client.QueryRows(context.Background(), "", QueryRowsRequest{Limit: 100})

	var engineErr *Error
	if !errors.As(err, &engineErr) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if engineErr.Code != "EXPLORER_INVALID_COLUMN" || engineErr.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("error = %+v", engineErr)
	}
}
