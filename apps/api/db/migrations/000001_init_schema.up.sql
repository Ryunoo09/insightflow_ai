-- InsightFlow AI initial schema (MVP metadata store).
-- 18 tables: identity, workspace, dataset, job, analysis, dashboard, governance.

-- ---------- Identity ----------

CREATE TABLE users (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email              text NOT NULL,
    password_hash      text,
    full_name          text NOT NULL DEFAULT '',
    status             text NOT NULL DEFAULT 'pending_verification',
    tokens_valid_after timestamptz NOT NULL DEFAULT now(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_status_check CHECK (status IN ('pending_verification', 'active', 'disabled'))
);

CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));

CREATE TABLE refresh_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash text NOT NULL,
    family_id  uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX refresh_tokens_hash_key ON refresh_tokens (token_hash);
CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id);

CREATE TABLE user_identities (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider         text NOT NULL,
    provider_subject text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_identities_provider_check CHECK (provider IN ('google'))
);

CREATE UNIQUE INDEX user_identities_provider_subject_key ON user_identities (provider, provider_subject);
CREATE INDEX user_identities_user_idx ON user_identities (user_id);

-- ---------- Workspace ----------

CREATE TABLE workspaces (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    slug       text NOT NULL,
    created_by uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX workspaces_slug_key ON workspaces (slug);

CREATE TABLE workspace_members (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role         text NOT NULL DEFAULT 'owner',
    joined_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT workspace_members_role_check CHECK (role IN ('owner', 'editor', 'viewer'))
);

CREATE UNIQUE INDEX workspace_members_ws_user_key ON workspace_members (workspace_id, user_id);
CREATE INDEX workspace_members_user_idx ON workspace_members (user_id);

-- ---------- Dataset ----------

CREATE TABLE datasets (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name               text NOT NULL,
    description        text NOT NULL DEFAULT '',
    current_version_no integer NOT NULL DEFAULT 1,
    created_by         uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX datasets_workspace_idx ON datasets (workspace_id);

CREATE TABLE dataset_versions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id      uuid NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    version_no      integer NOT NULL,
    status          text NOT NULL DEFAULT 'queued',
    file_format     text NOT NULL,
    original_name   text NOT NULL DEFAULT '',
    row_count       bigint,
    column_count    integer,
    byte_size       bigint,
    checksum_sha256 text,
    source_path     text NOT NULL,
    parquet_path    text,
    error_message   text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT dataset_versions_status_check CHECK (status IN ('queued', 'processing', 'ready', 'failed')),
    CONSTRAINT dataset_versions_format_check CHECK (file_format IN ('csv', 'xlsx'))
);

CREATE UNIQUE INDEX dataset_versions_dataset_version_key ON dataset_versions (dataset_id, version_no);
CREATE INDEX dataset_versions_status_idx ON dataset_versions (status);

CREATE TABLE dataset_columns (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    version_id    uuid NOT NULL REFERENCES dataset_versions (id) ON DELETE CASCADE,
    column_name   text NOT NULL,
    original_name text NOT NULL,
    inferred_type text NOT NULL,
    is_nullable   boolean NOT NULL DEFAULT true,
    column_order  integer NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX dataset_columns_version_name_key ON dataset_columns (version_id, column_name);

CREATE TABLE dataset_profiles (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    version_id    uuid NOT NULL REFERENCES dataset_versions (id) ON DELETE CASCADE,
    profile_json  jsonb NOT NULL,
    quality_score numeric(5, 2),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX dataset_profiles_version_key ON dataset_profiles (version_id);

-- ---------- Job queue ----------

CREATE TABLE processing_jobs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    version_id    uuid NOT NULL REFERENCES dataset_versions (id) ON DELETE CASCADE,
    job_type      text NOT NULL,
    status        text NOT NULL DEFAULT 'queued',
    attempts      integer NOT NULL DEFAULT 0,
    max_attempts  integer NOT NULL DEFAULT 3,
    error_message text,
    locked_at     timestamptz,
    finished_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT processing_jobs_status_check CHECK (status IN ('queued', 'processing', 'completed', 'failed'))
);

CREATE INDEX processing_jobs_claim_idx ON processing_jobs (created_at) WHERE status = 'queued';

-- ---------- Analysis ----------

CREATE TABLE analysis_sessions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    dataset_id   uuid NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    title        text NOT NULL DEFAULT '',
    created_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX analysis_sessions_workspace_idx ON analysis_sessions (workspace_id);
CREATE INDEX analysis_sessions_dataset_idx ON analysis_sessions (dataset_id);

CREATE TABLE analysis_messages (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id        uuid NOT NULL REFERENCES analysis_sessions (id) ON DELETE CASCADE,
    role              text NOT NULL,
    content           text NOT NULL,
    client_message_id text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT analysis_messages_role_check CHECK (role IN ('user', 'assistant', 'system'))
);

CREATE INDEX analysis_messages_session_idx ON analysis_messages (session_id, created_at);

CREATE TABLE analysis_runs (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id        uuid NOT NULL REFERENCES analysis_sessions (id) ON DELETE CASCADE,
    message_id        uuid REFERENCES analysis_messages (id) ON DELETE SET NULL,
    status            text NOT NULL DEFAULT 'running',
    error_code        text,
    prompt_tokens     integer NOT NULL DEFAULT 0,
    completion_tokens integer NOT NULL DEFAULT 0,
    started_at        timestamptz NOT NULL DEFAULT now(),
    finished_at       timestamptz,
    CONSTRAINT analysis_runs_status_check CHECK (status IN ('running', 'completed', 'failed'))
);

CREATE INDEX analysis_runs_session_idx ON analysis_runs (session_id, started_at);

CREATE TABLE analysis_results (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id          uuid NOT NULL REFERENCES analysis_runs (id) ON DELETE CASCADE,
    tool_name       text,
    result_json     jsonb,
    chart_spec_json jsonb,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX analysis_results_run_idx ON analysis_results (run_id);

-- ---------- Dashboard ----------

CREATE TABLE dashboards (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    title        text NOT NULL,
    description  text NOT NULL DEFAULT '',
    layout_json  jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_by   uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX dashboards_workspace_idx ON dashboards (workspace_id);

CREATE TABLE dashboard_widgets (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    dashboard_id    uuid NOT NULL REFERENCES dashboards (id) ON DELETE CASCADE,
    title           text NOT NULL,
    widget_type     text NOT NULL,
    query_spec_json jsonb NOT NULL,
    chart_spec_json jsonb NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX dashboard_widgets_dashboard_idx ON dashboard_widgets (dashboard_id);

-- ---------- Governance ----------

CREATE TABLE usage_records (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      uuid REFERENCES users (id) ON DELETE SET NULL,
    metric_type  text NOT NULL,
    amount       bigint NOT NULL DEFAULT 0,
    recorded_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT usage_records_metric_check CHECK (metric_type IN ('llm_tokens', 'analysis_runs', 'storage_bytes'))
);

CREATE INDEX usage_records_ws_metric_time_idx ON usage_records (workspace_id, metric_type, recorded_at);

CREATE TABLE audit_logs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  uuid REFERENCES workspaces (id) ON DELETE SET NULL,
    user_id       uuid REFERENCES users (id) ON DELETE SET NULL,
    action        text NOT NULL,
    resource_type text,
    resource_id   uuid,
    details       jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip_address    text,
    user_agent    text,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_logs_workspace_time_idx ON audit_logs (workspace_id, created_at DESC);
CREATE INDEX audit_logs_user_time_idx ON audit_logs (user_id, created_at DESC);

-- audit_logs is append-only: block UPDATE and DELETE at the database level.
CREATE FUNCTION audit_logs_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_logs_no_update
    BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_append_only();
