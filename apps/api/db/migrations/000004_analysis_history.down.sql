ALTER TABLE analysis_results DROP COLUMN plan_json;

DROP INDEX analysis_runs_assistant_idx;

ALTER TABLE analysis_runs DROP COLUMN assistant_message_id;