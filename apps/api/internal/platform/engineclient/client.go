// Package engineclient is the Go side of the internal Go -> Python engine IPC.
// The engine is internal-only: every call carries X-Internal-Token and the
// request id so Engine-side logs can be correlated with the API access log.
package engineclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// QueryRowsRequest is the payload for POST /v1/datasets/rows/query (Plan 05).
type QueryRowsRequest struct {
	DatasetRef DatasetRef       `json:"dataset_ref"`
	Offset     int              `json:"offset"`
	Limit      int              `json:"limit"`
	Sort       []ExplorerSort   `json:"sort,omitempty"`
	Filters    []ExplorerFilter `json:"filters,omitempty"`
	Search     string           `json:"search,omitempty"`
}

// ExplorerSort orders one explorer column.
type ExplorerSort struct {
	Column    string `json:"column"`
	Direction string `json:"direction"`
}

// ExplorerFilter restricts one explorer column. Value stays opaque: the engine
// validates the column and binds the value as a statement parameter.
type ExplorerFilter struct {
	Column   string `json:"column"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}

// ExplorerColumn is one column of the parquet schema. OriginalName is filled by
// the Go gateway from dataset_columns so the UI can show the uploaded header.
type ExplorerColumn struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	OriginalName string `json:"original_name,omitempty"`
}

// QueryRowsResponse is the engine explorer body.
type QueryRowsResponse struct {
	Columns []ExplorerColumn `json:"columns"`
	Rows    []map[string]any `json:"rows"`
	Total   int64            `json:"total"`
}

// AnalyzeColumn is one column of the dataset context sent to the planner.
type AnalyzeColumn struct {
	Name         string `json:"name"`
	OriginalName string `json:"original_name,omitempty"`
	Type         string `json:"type"`
	Nullable     bool   `json:"nullable"`
}

// AnalyzeDataset is the schema and profile summary for one version.
type AnalyzeDataset struct {
	DatasetID    string          `json:"dataset_id"`
	VersionNo    int32           `json:"version_no"`
	RowCount     int64           `json:"row_count"`
	ColumnCount  int32           `json:"column_count"`
	QualityScore *float64        `json:"quality_score,omitempty"`
	Columns      []AnalyzeColumn `json:"columns"`
}

// AnalyzeMessage is one prior conversation turn.
type AnalyzeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AnalyzeRequest is the payload for POST /v1/ai/analyze.
type AnalyzeRequest struct {
	DatasetRef DatasetRef       `json:"dataset_ref"`
	Question   string           `json:"question"`
	Dataset    AnalyzeDataset   `json:"dataset"`
	Messages   []AnalyzeMessage `json:"messages"`
}

// AnalyzeStream is an open SSE stream from the engine. The caller must Close it.
type AnalyzeStream struct {
	Body   io.ReadCloser
	reader *bufio.Reader
}

// Close releases the underlying response body.
func (s *AnalyzeStream) Close() error {
	if s == nil || s.Body == nil {
		return nil
	}
	return s.Body.Close()
}

// Event reads the next SSE frame. It returns io.EOF when the stream ends.
func (s *AnalyzeStream) Event() (name string, data []byte, err error) {
	if s.reader == nil {
		s.reader = bufio.NewReader(s.Body)
	}
	reader := s.reader
	var event string
	var payload strings.Builder
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			if errors.Is(readErr, io.EOF) && (event != "" || payload.Len() > 0) {
				return event, []byte(payload.String()), nil
			}
			return "", nil, readErr
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if event != "" || payload.Len() > 0 {
				return event, []byte(payload.String()), nil
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			payload.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
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
	if err := c.post(ctx, requestID, "/v1/datasets/process", req, &out); err != nil {
		return ProcessResponse{}, err
	}
	return out, nil
}

// QueryRows runs one explorer page on the engine.
func (c *Client) QueryRows(ctx context.Context, requestID string, req QueryRowsRequest) (QueryRowsResponse, error) {
	var out QueryRowsResponse
	if err := c.post(ctx, requestID, "/v1/datasets/rows/query", req, &out); err != nil {
		return QueryRowsResponse{}, err
	}
	return out, nil
}

// Analyze opens a streaming analysis run on the engine. The response body is
// returned unbuffered so the caller can relay frames as they arrive.
func (c *Client) Analyze(ctx context.Context, requestID string, req AnalyzeRequest) (*AnalyzeStream, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("engine: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/ai/analyze", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("engine: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("X-Internal-Token", c.token)
	if requestID != "" {
		httpReq.Header.Set("X-Request-ID", requestID)
	}
	// The stream outlives the default client timeout, so a dedicated client is
	// used with no total timeout: the caller's context is the real bound.
	streamClient := &http.Client{}
	res, err := streamClient.Do(httpReq)
	if err != nil {
		return nil, &Error{Code: "ENGINE_UNAVAILABLE", Message: "mesin analisis tidak dapat dihubungi", Retryable: true}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		defer func() { _ = res.Body.Close() }()
		return nil, decodeError(res)
	}
	return &AnalyzeStream{Body: res.Body}, nil
}

func (c *Client) post(ctx context.Context, requestID, path string, req, out any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("engine: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("engine: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Internal-Token", c.token)
	if requestID != "" {
		httpReq.Header.Set("X-Request-ID", requestID)
	}

	res, err := c.http.Do(httpReq)
	if err != nil {
		// Transport failure or timeout: transient by definition.
		return &Error{Code: "ENGINE_UNAVAILABLE", Message: "mesin pemrosesan tidak dapat dihubungi", Retryable: true}
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return decodeError(res)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("engine: decode response: %w", err)
	}
	return nil
}

func decodeError(res *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	var env errorEnvelope
	if err := json.Unmarshal(raw, &env); err == nil && env.Error.Code != "" {
		return &Error{StatusCode: res.StatusCode, Code: env.Error.Code, Message: env.Error.Message, Retryable: env.Error.Retryable}
	}
	return &Error{StatusCode: res.StatusCode, Code: "ENGINE_ERROR", Message: "pemrosesan gagal", Retryable: res.StatusCode >= 500}
}
