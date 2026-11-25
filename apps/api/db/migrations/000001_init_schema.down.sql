-- Reverse of 000001_init_schema.up.sql

DROP TRIGGER IF EXISTS audit_logs_no_update ON audit_logs;
DROP FUNCTION IF EXISTS audit_logs_append_only();

DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS usage_records;
DROP TABLE IF EXISTS dashboard_widgets;
DROP TABLE IF EXISTS dashboards;
DROP TABLE IF EXISTS analysis_results;
DROP TABLE IF EXISTS analysis_runs;
DROP TABLE IF EXISTS analysis_messages;
DROP TABLE IF EXISTS analysis_sessions;
DROP TABLE IF EXISTS processing_jobs;
DROP TABLE IF EXISTS dataset_profiles;
DROP TABLE IF EXISTS dataset_columns;
DROP TABLE IF EXISTS dataset_versions;
DROP TABLE IF EXISTS datasets;
DROP TABLE IF EXISTS workspace_members;
DROP TABLE IF EXISTS workspaces;
DROP TABLE IF EXISTS user_identities;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS users;
