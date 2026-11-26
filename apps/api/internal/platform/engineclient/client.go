// Package engineclient is the Go side of the internal Go -> Python engine IPC.
// The engine is internal-only: every call carries X-Internal-Token and the
// request id so Engine-side logs can be correlated with the API access log.
package engineclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DatasetRef identifies a dataset version. Engines map it to storage keys via
// their own resolver; Go never sends filesystem paths.
type DatasetRef struct {
	WorkspaceID string `json:"workspace_id"`
	DatasetID   string `json:"dataset_id"`
	VersionNo   int32  `json:"version_no"`
}

// Limits are the per-run processing bounds enforced by the engine.
type Limits struct {
	MaxRows    int64 `json:"max_rows"`
	MaxColumns int   `json:"max_columns"`
}

// ProcessRequest is the payload for POST /v1/datasets/process.
type ProcessRequest struct {
	DatasetRef   DatasetRef      `json:"dataset_ref"`
	SourceFormat string          `json:"source_format"`
	SourceSHA256 string          `json:"source_sha256,omitempty"`
	Overrides    json.RawMessage `json:"overrides,omitempty"`
	Limits       Limits          `json:"limits"`
}

// ProcessColumn is one normalized column returned by the engine.
type ProcessColumn struct {
	Ordinal       int32  `json:"ordinal"`
	Name          string `json:"name"`
	OriginalName  string `json:"original_name"`
	LogicalType   string `json:"logical_type"`
	PhysicalType  string `json:"physical_type"`
	Role          string `json:"role"`
	NullCount     int64  `json:"null_count"`
	DistinctCount int64  `json:"distinct_count"`
}

// ProcessParquet describes the written Parquet artifact. The path is not taken
// from here: Go rebuilds it with its own resolver.
type ProcessParquet struct {
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// ProcessResponse is the engine success body.
type ProcessResponse struct {
	RowCount         int64           `json:"row_count"`
	ColumnCount      int32           `json:"column_count"`
	Columns          []ProcessColumn `json:"columns"`
	Parquet          ProcessParquet  `json:"parquet"`
	Profile          json.RawMessage `json:"profile"`
	ProcessingReport json.RawMessage `json:"processing_report"`
}

// Error is a structured engine failure. Retryable marks transient conditions
// (engine down, timeout) that the job worker may retry.
type Error struct {
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
}

func (e *Error) Error() string {
	return fmt.Sprintf("engine: %s (%d): %s", e.Code, e.StatusCode, e.Message)
}

// Retryable reports whether err should be retried by the job worker.
func Retryable(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Retryable
}

// Client calls the internal engine.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New builds a client. timeout bounds a single process call (PRD 23.5: 300s).
func New(baseURL, token string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: timeout},
	}
}

type errorEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

// Process runs one ingestion job on the engine.
func (c *Client) Process(ctx context.Context, requestID string, req ProcessRequest) (ProcessResponse, error) {
	var out ProcessResponse
	body, err := json.Marshal(req)
	if err != nil {
		return out, fmt.Errorf("engine: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/datasets/process", bytes.NewReader(body))
	if err != nil {
		return out, fmt.Errorf("engine: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Internal-Token", c.token)
	if requestID != "" {
		httpReq.Header.Set("X-Request-ID", requestID)
	}

	res, err := c.http.Do(httpReq)
	if err != nil {
		// Transport failure or timeout: transient by definition.
		return out, &Error{Code: "ENGINE_UNAVAILABLE", Message: "mesin pemrosesan tidak dapat dihubungi", Retryable: true}
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return out, decodeError(res)
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("engine: decode response: %w", err)
	}
	return out, nil
}

func decodeError(res *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	var env errorEnvelope
	if err := json.Unmarshal(raw, &env); err == nil && env.Error.Code != "" {
		return &Error{StatusCode: res.StatusCode, Code: env.Error.Code, Message: env.Error.Message, Retryable: env.Error.Retryable}
	}
	return &Error{StatusCode: res.StatusCode, Code: "ENGINE_ERROR", Message: "pemrosesan gagal", Retryable: res.StatusCode >= 500}
}
