package analysis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"insightflow/api/internal/platform/cache"
	"insightflow/api/internal/platform/pgconv"
)

func TestQuotaExceededRejectsRunWithTooManyRequests(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	svc, _, sessionID, userID, q := sseDeps(t)
	ctx := context.Background()
	cacheClient, err := cache.New(ctx, addr, "", 0)
	if err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	defer func() { _ = cacheClient.Close() }()

	// Limit 0 disables enforcement, so use a limit already exhausted by a
	// manual increment on the same key the handler derives.
	env := "test-" + uuid.NewString()[:8]
	workspaceID, err := svc.SessionWorkspace(ctx, sessionID, userID)
	if err != nil {
		t.Fatalf("SessionWorkspace: %v", err)
	}
	key := cache.Key(env, cache.PrefixQuota, "analysis", workspaceID.String(), time.Now().UTC().Format("20060102"))
	if err := cacheClient.SetWithTTL(ctx, key, []byte("5"), time.Hour); err != nil {
		t.Fatalf("seed quota: %v", err)
	}

	h := NewHandlerWithEngine(svc, &fakeAnalyzer{}, cacheClient, env, NewDailyQuota(cacheClient, env, 5))
	router := gin.New()
	router.Use(withPrincipal(userID))
	router.POST("/analysis/sessions/:session_id/messages", h.SendMessage)

	req := httptest.NewRequest(http.MethodPost, "/analysis/sessions/"+sessionID.String()+"/messages", strings.NewReader(`{"content":"q"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Error.Code != "ANALYSIS_QUOTA_EXCEEDED" {
		t.Fatalf("code = %q", payload.Error.Code)
	}

	// No message and no run may be created for a rejected request.
	messages, err := q.ListAnalysisMessages(ctx, pgconv.UUID(sessionID))
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages = %d, want 0 for a rejected run", len(messages))
	}
}
