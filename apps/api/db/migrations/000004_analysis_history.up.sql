-- Plan 06 history: link a completed run to its assistant message and persist the
-- accepted plan, so reopening a session renders the same answer (chart, evidence,
-- plan) instead of only the narrative text.

ALTER TABLE analysis_runs
    ADD COLUMN assistant_message_id uuid REFERENCES analysis_messages (id) ON DELETE SET NULL;

CREATE INDEX analysis_runs_assistant_idx ON analysis_runs (assistant_message_id);

ALTER TABLE analysis_results
    ADD COLUMN plan_json jsonb;

-- Backfill runs created before this migration: the assistant message is the first
-- assistant turn after the run's user message in the same session. The plan was
-- never stored, so only the result and chart can be restored for old runs.
UPDATE analysis_runs r
SET assistant_message_id = a.id
FROM (
    SELECT run.id AS run_id, (
        SELECT m.id
        FROM analysis_messages m
        JOIN analysis_messages u ON u.id = run.message_id
        WHERE m.session_id = run.session_id
          AND m.role = 'assistant'
          AND m.created_at > u.created_at
        ORDER BY m.created_at, m.id
        LIMIT 1
    ) AS id
    FROM analysis_runs run
    WHERE run.status = 'completed' AND run.message_id IS NOT NULL
) a
WHERE r.id = a.run_id AND a.id IS NOT NULL;