// Package analysis owns AI analysis sessions: conversation history, the
// schema context handed to the engine, and the run lifecycle (Plan 06).
package analysis

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/pgconv"
)

// ErrNotFound covers a missing session, dataset, or membership alike so callers
// return a uniform 404 and cannot probe other tenants.
var ErrNotFound = errors.New("analysis: not found")

// ErrNotReady maps to a 409 ANALYSIS_DATASET_NOT_READY: the dataset version is
// not ready to be analyzed.
var ErrNotReady = errors.New("analysis: dataset version is not ready")

// ErrInvalidTitle maps to a 422 validation failure.
var ErrInvalidTitle = errors.New("analysis: invalid title")

// ErrInvalidMessage maps to a 422: the submitted question is empty or too long.
var ErrInvalidMessage = errors.New("analysis: invalid message")

// ErrQuotaExceeded maps to a 429: the workspace has spent its daily run quota.
var ErrQuotaExceeded = errors.New("analysis: daily quota exceeded")

// Repository is the analysis data-access layer.
type Repository struct {
	db dbgen.DBTX
	q  *dbgen.Queries
}

// NewRepository wraps sqlc queries over any DB handle (pool or transaction).
func NewRepository(db dbgen.DBTX) *Repository { return &Repository{db: db, q: dbgen.New(db)} }

// CreateSession inserts a session row.
func (r *Repository) CreateSession(ctx context.Context, workspaceID, datasetID, userID uuid.UUID, title string) (dbgen.AnalysisSession, error) {
	return r.q.CreateAnalysisSession(ctx, dbgen.CreateAnalysisSessionParams{
		WorkspaceID: pgconv.UUID(workspaceID),
		DatasetID:   pgconv.UUID(datasetID),
		Title:       title,
		CreatedBy:   pgconv.UUID(userID),
	})
}

// GetSession loads a session the caller is a member of.
func (r *Repository) GetSession(ctx context.Context, sessionID, userID uuid.UUID) (dbgen.AnalysisSession, error) {
	s, err := r.q.GetAnalysisSession(ctx, dbgen.GetAnalysisSessionParams{
		ID:     pgconv.UUID(sessionID),
		UserID: pgconv.UUID(userID),
	})
	if err != nil {
		return dbgen.AnalysisSession{}, mapNotFound(err)
	}
	return s, nil
}

// ListSessions returns one page of the caller's sessions in a workspace.
func (r *Repository) ListSessions(ctx context.Context, workspaceID, userID uuid.UUID, limit, offset int32) ([]dbgen.AnalysisSession, int64, error) {
	rows, err := r.q.ListAnalysisSessions(ctx, dbgen.ListAnalysisSessionsParams{
		WorkspaceID: pgconv.UUID(workspaceID),
		UserID:      pgconv.UUID(userID),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := r.q.CountAnalysisSessions(ctx, dbgen.CountAnalysisSessionsParams{
		WorkspaceID: pgconv.UUID(workspaceID),
		UserID:      pgconv.UUID(userID),
	})
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// AnalyzableVersion resolves the dataset's current version for a member.
func (r *Repository) AnalyzableVersion(ctx context.Context, datasetID, workspaceID, userID uuid.UUID) (dbgen.GetAnalyzableDatasetVersionRow, error) {
	row, err := r.q.GetAnalyzableDatasetVersion(ctx, dbgen.GetAnalyzableDatasetVersionParams{
		ID:          pgconv.UUID(datasetID),
		UserID:      pgconv.UUID(userID),
		WorkspaceID: pgconv.UUID(workspaceID),
	})
	if err != nil {
		return dbgen.GetAnalyzableDatasetVersionRow{}, mapNotFound(err)
	}
	return row, nil
}

// Messages returns a session's full history in chronological order.
func (r *Repository) Messages(ctx context.Context, sessionID uuid.UUID) ([]dbgen.AnalysisMessage, error) {
	return r.q.ListAnalysisMessages(ctx, pgconv.UUID(sessionID))
}

// RecentMessages returns the newest limit messages, oldest-first.
func (r *Repository) RecentMessages(ctx context.Context, sessionID uuid.UUID, limit int32) ([]dbgen.AnalysisMessage, error) {
	return r.q.ListRecentAnalysisMessages(ctx, dbgen.ListRecentAnalysisMessagesParams{
		SessionID: pgconv.UUID(sessionID),
		Limit:     limit,
	})
}

// CreateMessage appends one message and bumps the session's updated_at.
func (r *Repository) CreateMessage(ctx context.Context, sessionID uuid.UUID, role, content, clientMessageID string) (dbgen.AnalysisMessage, error) {
	msg, err := r.q.CreateAnalysisMessage(ctx, dbgen.CreateAnalysisMessageParams{
		SessionID:       pgconv.UUID(sessionID),
		Role:            role,
		Content:         content,
		ClientMessageID: textOrNull(clientMessageID),
	})
	if err != nil {
		return dbgen.AnalysisMessage{}, err
	}
	if err := r.q.TouchAnalysisSession(ctx, pgconv.UUID(sessionID)); err != nil {
		return dbgen.AnalysisMessage{}, err
	}
	return msg, nil
}

// Columns lists the persisted column metadata for a version.
func (r *Repository) Columns(ctx context.Context, versionID uuid.UUID) ([]dbgen.DatasetColumn, error) {
	return r.q.ListDatasetColumns(ctx, pgconv.UUID(versionID))
}

// Profile loads the stored profile, or nil when the version has none.
func (r *Repository) Profile(ctx context.Context, versionID uuid.UUID) (*dbgen.DatasetProfile, error) {
	p, err := r.q.GetDatasetProfile(ctx, pgconv.UUID(versionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// CreateRun opens a running analysis run for one user message.
func (r *Repository) CreateRun(ctx context.Context, sessionID, messageID uuid.UUID) (dbgen.AnalysisRun, error) {
	return r.q.CreateAnalysisRun(ctx, dbgen.CreateAnalysisRunParams{
		SessionID: pgconv.UUID(sessionID),
		MessageID: pgconv.UUID(messageID),
	})
}

// GetRun loads one run by id.
func (r *Repository) GetRun(ctx context.Context, runID uuid.UUID) (dbgen.AnalysisRun, error) {
	run, err := r.q.GetAnalysisRun(ctx, pgconv.UUID(runID))
	if err != nil {
		return dbgen.AnalysisRun{}, mapNotFound(err)
	}
	return run, nil
}

// CompleteRun marks a run completed and records token usage.
func (r *Repository) CompleteRun(ctx context.Context, runID uuid.UUID, promptTokens, completionTokens int) error {
	_, err := r.q.CompleteAnalysisRun(ctx, dbgen.CompleteAnalysisRunParams{
		ID:               pgconv.UUID(runID),
		PromptTokens:     int32(promptTokens),
		CompletionTokens: int32(completionTokens),
	})
	return err
}

// FailRun marks a run failed with a stable error code.
func (r *Repository) FailRun(ctx context.Context, runID uuid.UUID, code string) error {
	_, err := r.q.FailAnalysisRun(ctx, dbgen.FailAnalysisRunParams{
		ID:        pgconv.UUID(runID),
		ErrorCode: textOrNull(code),
	})
	return err
}

// InsertResult stores the tool output and chart spec for a run.
func (r *Repository) InsertResult(ctx context.Context, runID uuid.UUID, toolName string, resultJSON, chartJSON []byte) error {
	_, err := r.q.InsertAnalysisResult(ctx, dbgen.InsertAnalysisResultParams{
		RunID:         pgconv.UUID(runID),
		ToolName:      textOrNull(toolName),
		ResultJson:    resultJSON,
		ChartSpecJson: chartJSON,
	})
	return err
}

func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func textOrNull(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
