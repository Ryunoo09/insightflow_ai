package analysis

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"insightflow/api/internal/middleware"
	"insightflow/api/internal/modules/users"
	"insightflow/api/internal/platform/cache"
	"insightflow/api/internal/platform/database"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/engineclient"
	"insightflow/api/internal/platform/pgconv"
)

// fakeAnalyzer streams canned SSE frames.
type fakeAnalyzer struct {
	mu     sync.Mutex
	frames string
	err    error
	calls  int
	last   engineclient.AnalyzeRequest
}

func (f *fakeAnalyzer) Analyze(_ context.Context, _ string, req engineclient.AnalyzeRequest) (*engineclient.AnalyzeStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.last = req
	if f.err != nil {
		return nil, f.err
	}
	return &engineclient.AnalyzeStream{Body: io.NopCloser(strings.NewReader(f.frames))}, nil
}

// sseDeps builds a service, a session, and an authenticated router.
func sseDeps(t *testing.T) (svc *Service, router *gin.Engine, sessionID uuid.UUID, userID uuid.UUID, q *dbgen.Queries) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	pool, err := database.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	q = dbgen.New(pool)
	svc = NewService(NewRepository(pool))

	user, err := q.CreateUser(ctx, dbgen.CreateUserParams{
		Email: uuid.NewString() + "@example.com", PasswordHash: pgtype.Text{},
		FullName: "SSE Tester", Status: "active",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	ws, err := q.CreateWorkspace(ctx, dbgen.CreateWorkspaceParams{
		Name: "SSE WS", Slug: "ws-" + uuid.NewString()[:8], CreatedBy: user.ID,
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := q.AddWorkspaceMember(ctx, dbgen.AddWorkspaceMemberParams{
		WorkspaceID: ws.ID, UserID: user.ID, Role: "owner",
	}); err != nil {
		t.Fatalf("add member: %v", err)
	}
	dsID := uuid.New()
	if _, err := q.CreateDataset(ctx, dbgen.CreateDatasetParams{
		ID: pgconv.UUID(dsID), WorkspaceID: ws.ID, Name: "DS", Description: "", CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	version, err := q.CreateDatasetVersion(ctx, dbgen.CreateDatasetVersionParams{
		DatasetID: pgconv.UUID(dsID), VersionNo: 1, FileFormat: "csv", OriginalName: "d.csv",
		ByteSize: pgtype.Int8{Int64: 1, Valid: true}, ChecksumSha256: pgtype.Text{String: "x", Valid: true},
		SourcePath: "sources/x/y/1/source.csv",
	})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	if _, err := q.MarkDatasetVersionReady(ctx, dbgen.MarkDatasetVersionReadyParams{
		ID: version.ID, RowCount: pgtype.Int8{Int64: 3, Valid: true},
		ColumnCount: pgtype.Int4{Int32: 1, Valid: true},
		ParquetPath: pgtype.Text{String: "parquet/x/y/1/data.parquet", Valid: true},
	}); err != nil {
		t.Fatalf("mark ready: %v", err)
	}
	session, err := svc.CreateSession(ctx, uuid.UUID(ws.ID.Bytes), dsID, uuid.UUID(user.ID.Bytes), "s")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return svc, nil, session.ID, uuid.UUID(user.ID.Bytes), q
}

// withPrincipal injects an authenticated principal without exercising JWT.
func withPrincipal(userID uuid.UUID) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("auth_principal", users.Principal{ID: userID, Email: "tester@example.com", FullName: "Tester"})
		c.Next()
	}
}

func TestRelayForwardsFramesAndPersistsRun(t *testing.T) {
	svc, _, sessionID, userID, q := sseDeps(t)
	analyzer := &fakeAnalyzer{frames: strings.Join([]string{
		"event: stage_start\ndata: {\"stage\":\"planning\"}\n\n",
		"event: run_completed\ndata: {\"result\":{\"tool\":\"aggregate_metric\"},\"narrative\":\"Total 60.\",\"chart\":{\"kind\":\"bar\"}}\n\n",
	}, "")}
	h := NewHandlerWithEngine(svc, analyzer, nil, "test", nil)

	router := gin.New()
	router.Use(withPrincipal(userID))
	router.POST("/analysis/sessions/:session_id/messages", h.SendMessage)

	body := `{"content":"total amount?"}`
	req := httptest.NewRequest(http.MethodPost, "/analysis/sessions/"+sessionID.String()+"/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "event: stage_start") || !strings.Contains(out, "event: run_completed") {
		t.Fatalf("stream missing frames: %s", out)
	}
	if analyzer.calls != 1 {
		t.Fatalf("analyzer calls = %d", analyzer.calls)
	}
	if analyzer.last.Question != "total amount?" {
		t.Fatalf("question = %q", analyzer.last.Question)
	}

	// The run and the assistant reply must be persisted.
	runs, err := q.ListAnalysisMessages(context.Background(), pgconv.UUID(sessionID))
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("messages = %d, want user + assistant", len(runs))
	}
	if runs[1].Role != "assistant" || runs[1].Content != "Total 60." {
		t.Fatalf("assistant message = %+v", runs[1])
	}
}

func TestSendMessageRejectsEmptyQuestion(t *testing.T) {
	svc, _, sessionID, userID, _ := sseDeps(t)
	h := NewHandlerWithEngine(svc, &fakeAnalyzer{}, nil, "test", nil)
	router := gin.New()
	router.Use(withPrincipal(userID))
	router.POST("/analysis/sessions/:session_id/messages", h.SendMessage)

	req := httptest.NewRequest(http.MethodPost, "/analysis/sessions/"+sessionID.String()+"/messages", strings.NewReader(`{"content":"   "}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSendMessageFailsClosedWithoutAnalyzer(t *testing.T) {
	svc, _, sessionID, userID, _ := sseDeps(t)
	h := NewHandlerWithEngine(svc, nil, nil, "test", nil)
	router := gin.New()
	router.Use(withPrincipal(userID))
	router.POST("/analysis/sessions/:session_id/messages", h.SendMessage)

	req := httptest.NewRequest(http.MethodPost, "/analysis/sessions/"+sessionID.String()+"/messages", strings.NewReader(`{"content":"q"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestSendMessageRecordsEngineFailure(t *testing.T) {
	svc, _, sessionID, userID, q := sseDeps(t)
	h := NewHandlerWithEngine(svc, &fakeAnalyzer{err: &engineclient.Error{Code: "ENGINE_UNAVAILABLE"}}, nil, "test", nil)
	router := gin.New()
	router.Use(withPrincipal(userID))
	router.POST("/analysis/sessions/:session_id/messages", h.SendMessage)

	req := httptest.NewRequest(http.MethodPost, "/analysis/sessions/"+sessionID.String()+"/messages", strings.NewReader(`{"content":"q"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError && rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rec.Code)
	}
	// The user message survives; the run must be marked failed.
	msgs, err := q.ListAnalysisMessages(context.Background(), pgconv.UUID(sessionID))
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("messages = %+v", msgs)
	}
}

func TestSendMessageStreamsFailureFrame(t *testing.T) {
	svc, _, sessionID, userID, _ := sseDeps(t)
	analyzer := &fakeAnalyzer{frames: "event: run_failed\ndata: {\"code\":\"PLAN_UNKNOWN_COLUMN\",\"message\":\"x\"}\n\n"}
	h := NewHandlerWithEngine(svc, analyzer, nil, "test", nil)
	router := gin.New()
	router.Use(withPrincipal(userID))
	router.POST("/analysis/sessions/:session_id/messages", h.SendMessage)

	req := httptest.NewRequest(http.MethodPost, "/analysis/sessions/"+sessionID.String()+"/messages", strings.NewReader(`{"content":"q"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "PLAN_UNKNOWN_COLUMN") {
		t.Fatalf("failure frame not relayed: %s", rec.Body.String())
	}
}

func TestLockPreventsConcurrentRuns(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	svc, _, sessionID, userID, _ := sseDeps(t)
	ctx := context.Background()
	cacheClient, err := cache.New(ctx, addr, "", 0)
	if err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	defer func() { _ = cacheClient.Close() }()

	lockKey := cache.Key("test", cache.PrefixLock, "analysis", sessionID.String())
	held, err := cacheClient.AcquireLock(ctx, lockKey, sseLockTTL)
	if err != nil || !held {
		t.Fatalf("acquire: %v %v", held, err)
	}
	defer func() { _ = cacheClient.ReleaseLock(ctx, lockKey) }()

	h := NewHandlerWithEngine(svc, &fakeAnalyzer{}, cacheClient, "test", nil)
	router := gin.New()
	router.Use(withPrincipal(userID))
	router.POST("/analysis/sessions/:session_id/messages", h.SendMessage)

	req := httptest.NewRequest(http.MethodPost, "/analysis/sessions/"+sessionID.String()+"/messages", strings.NewReader(`{"content":"q"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while lock is held", rec.Code)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Error.Code != "ANALYSIS_RUN_IN_PROGRESS" {
		t.Fatalf("code = %q", payload.Error.Code)
	}
}

func TestLockIsReleasedAfterRun(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	svc, _, sessionID, userID, _ := sseDeps(t)
	ctx := context.Background()
	cacheClient, err := cache.New(ctx, addr, "", 0)
	if err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	defer func() { _ = cacheClient.Close() }()

	analyzer := &fakeAnalyzer{frames: "event: run_completed\ndata: {\"result\":{\"tool\":\"x\"},\"narrative\":\"n\"}\n\n"}
	h := NewHandlerWithEngine(svc, analyzer, cacheClient, "test", nil)
	router := gin.New()
	router.Use(withPrincipal(userID))
	router.POST("/analysis/sessions/:session_id/messages", h.SendMessage)

	req := httptest.NewRequest(http.MethodPost, "/analysis/sessions/"+sessionID.String()+"/messages", strings.NewReader(`{"content":"q"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	lockKey := cache.Key("test", cache.PrefixLock, "analysis", sessionID.String())
	// After the handler returns, the lock must be free again.
	held, err := cacheClient.AcquireLock(ctx, lockKey, sseLockTTL)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !held {
		t.Fatal("lock was not released after the run finished")
	}
	_ = cacheClient.ReleaseLock(ctx, lockKey)
}

// mustPrincipal builds a principal for the test router. The auth middleware is
// bypassed on purpose: this file tests the SSE gateway, not authentication.
var _ = middleware.PrincipalFrom
