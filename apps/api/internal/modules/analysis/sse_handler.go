package analysis

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"insightflow/api/internal/middleware"
	"insightflow/api/internal/platform/cache"
	"insightflow/api/internal/platform/engineclient"
	"insightflow/api/internal/platform/httpx"
)

const (
	// sseLockTTL bounds how long one session may hold the run lock. It exceeds
	// the engine's 30s LLM timeout plus tool execution, so a live run never
	// loses it before the stream ends.
	sseLockTTL = 3 * time.Minute
	// bookkeepingTimeout bounds the post-stream DB write. The client is gone by
	// then, so this cannot rely on the request context.
	bookkeepingTimeout = 10 * time.Second
)

// Analyzer opens an analysis stream on the engine.
type Analyzer interface {
	Analyze(ctx context.Context, requestID string, req engineclient.AnalyzeRequest) (*engineclient.AnalyzeStream, error)
}

// SendMessage handles POST /analysis/sessions/:session_id/messages as an SSE
// stream: it enforces quota, persists the question, relays engine frames, and
// records the run outcome.
func (h *Handler) SendMessage(c *gin.Context) {
	principal, ok := middleware.PrincipalFrom(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	sessionID, err := uuid.Parse(c.Param("session_id"))
	if err != nil {
		writeNotFound(c)
		return
	}

	var req struct {
		Content string `json:"content" binding:"required,max=2000"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteBindError(c, err)
		return
	}
	if h.analyzer == nil {
		httpx.WriteError(c, httpx.Err(http.StatusServiceUnavailable, "ANALYSIS_UNAVAILABLE", "Layanan analisis belum tersedia."))
		return
	}

	ctx := c.Request.Context()
	// The lock is taken before any write so two concurrent submits cannot both
	// create a run for the same session.
	lockKey := ""
	if h.cache != nil {
		lockKey = cache.Key(h.env, cache.PrefixLock, "analysis", sessionID.String())
		acquired, lockErr := h.cache.AcquireLock(ctx, lockKey, sseLockTTL)
		if lockErr != nil {
			// Redis is soft state: a lock failure must not block analysis.
			lockKey = ""
		} else if !acquired {
			httpx.WriteError(c, httpx.Err(http.StatusConflict, "ANALYSIS_RUN_IN_PROGRESS", "Analisis lain masih berjalan pada sesi ini."))
			return
		}
	}
	if lockKey != "" {
		defer func() {
			releaseCtx, cancel := detach(context.Background())
			defer cancel()
			_ = h.cache.ReleaseLock(releaseCtx, lockKey)
		}()
	}

	// Quota is checked before the engine is called: a rejected run must not
	// spend a provider credit, and the counter is the only hard bound on cost.
	if h.quota.Enabled() {
		workspaceID, err := h.svc.SessionWorkspace(ctx, sessionID, principal.ID)
		if err != nil {
			writeAnalysisError(c, err)
			return
		}
		allowed, quotaErr := h.quota.ConsumeDaily(ctx, workspaceID, time.Now())
		if quotaErr == nil && !allowed {
			writeAnalysisError(c, quotaError(h.quota.limit))
			return
		}
	}

	run, err := h.svc.StartRun(ctx, sessionID, principal.ID, req.Content)
	if err != nil {
		writeAnalysisError(c, err)
		return
	}

	stream, err := h.analyzer.Analyze(ctx, httpx.RequestID(c), engineclient.AnalyzeRequest{
		DatasetRef: engineclient.DatasetRef{
			WorkspaceID: run.WorkspaceID.String(),
			DatasetID:   run.DatasetID.String(),
			VersionNo:   run.VersionNo,
		},
		Question: req.Content,
		Dataset:  run.Dataset,
		Messages: run.Messages,
	})
	if err != nil {
		failCtx, cancel := detach(ctx)
		defer cancel()
		_ = h.svc.FailRun(failCtx, run.RunID, engineErrorCode(err))
		writeAnalysisError(c, err)
		return
	}
	defer func() { _ = stream.Close() }()

	writeSSEHeaders(c)
	c.Writer.Flush()

	relay := relayStream(c, stream)
	finishRun(c, h, run.RunID, relay)
}

// finishRun records the outcome. Errors here cannot be reported to the client
// (the stream is already closed), so they are only logged.
func finishRun(c *gin.Context, h *Handler, runID uuid.UUID, relay relayResult) {
	ctx, cancel := detach(c.Request.Context())
	defer cancel()
	if relay.failureCode != "" {
		_ = h.svc.FailRun(ctx, runID, relay.failureCode)
		return
	}
	_ = h.svc.CompleteRun(ctx, CompleteRunInput{
		RunID:            runID,
		ToolName:         relay.toolName,
		ResultJSON:       relay.resultJSON,
		ChartJSON:        relay.chartJSON,
		PromptTokens:     relay.promptTokens,
		CompletionTokens: relay.completionTokens,
	})
}

func writeSSEHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	// Defensive: a reverse proxy must not buffer the stream.
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
}
