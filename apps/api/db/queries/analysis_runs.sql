-- Analysis runs and results (Plan 06, TASK-602).
-- A run is one message exchange: it starts when the user submits a question and
-- ends when the engine stream finishes or fails.

-- name: CreateAnalysisRun :one
INSERT INTO analysis_runs (session_id, message_id, status)
VALUES ($1, $2, 'running')
RETURNING *;

-- name: CompleteAnalysisRun :one
UPDATE analysis_runs
SET status = 'completed', finished_at = now(),
    prompt_tokens = $2, completion_tokens = $3
WHERE id = $1
RETURNING *;

-- name: FailAnalysisRun :one
UPDATE analysis_runs
SET status = 'failed', finished_at = now(), error_code = $2
WHERE id = $1
RETURNING *;

-- name: InsertAnalysisResult :one
INSERT INTO analysis_results (run_id, tool_name, result_json, chart_spec_json, plan_json)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: SetAnalysisRunAssistant :exec
-- Links the run to the assistant message it produced, so history can find the
-- result/chart/plan for that turn.
UPDATE analysis_runs SET assistant_message_id = $2 WHERE id = $1;

-- name: ListAnalysisSessionTurns :many
-- Conversation turns with their run artifacts. A user message has no result; an
-- assistant message carries the result, chart, and plan written by its run.
SELECT m.id, m.role, m.content, m.created_at,
       res.result_json, res.chart_spec_json, res.plan_json
FROM analysis_messages m
LEFT JOIN analysis_runs r ON r.assistant_message_id = m.id
LEFT JOIN analysis_results res ON res.run_id = r.id
WHERE m.session_id = $1
ORDER BY m.created_at, m.id;

-- name: GetAnalysisRun :one
SELECT * FROM analysis_runs WHERE id = $1;

-- name: ListAnalysisResults :many
SELECT * FROM analysis_results WHERE run_id = $1 ORDER BY created_at;
