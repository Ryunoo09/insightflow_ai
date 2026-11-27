package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/gin-gonic/gin"

	"insightflow/api/internal/platform/engineclient"
)

// relayResult is what the relay observed, so the run can be finalized once.
type relayResult struct {
	promptTokens     int
	completionTokens int
	toolName         string
	resultJSON       json.RawMessage
	chartJSON        json.RawMessage
	failureCode      string
}

// relayStream forwards engine frames to the client verbatim and captures the
// payloads the API must persist. Every frame is flushed immediately: without
// the explicit Flush the browser buffers and the stage indicator never moves.
func relayStream(c *gin.Context, stream *engineclient.AnalyzeStream) relayResult {
	var out relayResult
	clientGone := c.Request.Context().Done()
	for {
		select {
		case <-clientGone:
			// The browser aborted: stop reading and let the deferred cleanup run.
			out.failureCode = "CLIENT_DISCONNECTED"
			return out
		default:
		}

		event, data, err := stream.Event()
		if err != nil {
			if !errors.Is(err, io.EOF) && out.failureCode == "" {
				out.failureCode = "ENGINE_STREAM_BROKEN"
			}
			return out
		}
		out.observe(event, data)
		if _, err := c.Writer.WriteString(frame(event, data)); err != nil {
			out.failureCode = "CLIENT_DISCONNECTED"
			return out
		}
		c.Writer.Flush()
		if event == "run_completed" || event == "run_failed" {
			return out
		}
	}
}

// observe captures persisted fields from the terminal frames only.
func (r *relayResult) observe(event string, data []byte) {
	switch event {
	case "run_failed":
		var payload struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(data, &payload) == nil && payload.Code != "" {
			r.failureCode = payload.Code
		}
	case "run_completed":
		var payload struct {
			Result struct {
				Tool string `json:"tool"`
			} `json:"result"`
			Chart json.RawMessage `json:"chart"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(data, &payload) == nil {
			r.toolName = payload.Result.Tool
			r.chartJSON = payload.Chart
			r.promptTokens = payload.Usage.PromptTokens
			r.completionTokens = payload.Usage.CompletionTokens
		}
		r.resultJSON = append(json.RawMessage(nil), data...)
	}
}

// frame renders one SSE frame. Data is already single-line JSON from the engine.
func frame(event string, data []byte) string {
	return "event: " + event + "\ndata: " + string(data) + "\n\n"
}

func engineErrorCode(err error) string {
	var engineErr *engineclient.Error
	if errors.As(err, &engineErr) && engineErr.Code != "" {
		return engineErr.Code
	}
	return "ANALYSIS_FAILED"
}

// detach returns a context for post-stream bookkeeping: the request context is
// already cancelled once the client disconnects.
func detach(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), bookkeepingTimeout)
}
