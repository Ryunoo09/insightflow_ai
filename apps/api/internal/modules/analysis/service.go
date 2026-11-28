package analysis

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/engineclient"
)

// defaultTitle is used when the client does not name the session.
const defaultTitle = "Analisis baru"

// maxTitleLength bounds the stored title.
const maxTitleLength = 200

// Session is the API projection of an analysis session.
type Session struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	DatasetID   uuid.UUID
	Title       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Message is one conversation turn. Assistant turns may carry the run artifacts
// so a reopened session renders the same chart, evidence, and plan.
type Message struct {
	ID         uuid.UUID
	Role       string
	Content    string
	CreatedAt  time.Time
	ResultJSON json.RawMessage
	ChartJSON  json.RawMessage
	PlanJSON   json.RawMessage
}

// SessionDetail is a session with its history and the compiled engine context.
type SessionDetail struct {
	Session  Session
	Messages []Message
	Context  AnalysisContext
}

// Service implements analysis session use cases.
type Service struct {
	repo *Repository
}

// NewService builds the analysis service.
func NewService(repo *Repository) *Service { return &Service{repo: repo} }

// RunStart is everything needed to open an engine run for one question.
type RunStart struct {
	RunID       uuid.UUID
	WorkspaceID uuid.UUID
	DatasetID   uuid.UUID
	VersionNo   int32
	Dataset     engineclient.AnalyzeDataset
	Messages    []engineclient.AnalyzeMessage
}

// CompleteRunInput is the persisted outcome of one successful run.
type CompleteRunInput struct {
	RunID            uuid.UUID
	ToolName         string
	ResultJSON       json.RawMessage
	ChartJSON        json.RawMessage
	PlanJSON         json.RawMessage
	PromptTokens     int
	CompletionTokens int
}

// StartRun records the user's question, opens a run row, and builds the engine
// payload. It is the single entry point used by the SSE handler.
func (s *Service) StartRun(ctx context.Context, sessionID, userID uuid.UUID, content string) (RunStart, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return RunStart{}, ErrInvalidMessage
	}
	session, err := s.repo.GetSession(ctx, sessionID, userID)
	if err != nil {
		return RunStart{}, err
	}
	version, err := s.repo.AnalyzableVersion(ctx, uuid.UUID(session.DatasetID.Bytes), uuid.UUID(session.WorkspaceID.Bytes), userID)
	if err != nil {
		return RunStart{}, err
	}
	if version.Status != "ready" {
		return RunStart{}, ErrNotReady
	}

	// History is read before the new message is stored so the planner does not
	// see the question twice.
	history, err := s.repo.RecentMessages(ctx, sessionID, contextMessageLimit)
	if err != nil {
		return RunStart{}, err
	}
	message, err := s.repo.CreateMessage(ctx, sessionID, "user", content, "")
	if err != nil {
		return RunStart{}, err
	}
	run, err := s.repo.CreateRun(ctx, sessionID, uuid.UUID(message.ID.Bytes))
	if err != nil {
		return RunStart{}, err
	}
	analysisCtx, err := s.buildContext(ctx, session, version, history)
	if err != nil {
		_ = s.repo.FailRun(ctx, uuid.UUID(run.ID.Bytes), "CONTEXT_BUILD_FAILED")
		return RunStart{}, err
	}

	dataset := engineclient.AnalyzeDataset{
		DatasetID:    analysisCtx.Dataset.DatasetID,
		VersionNo:    analysisCtx.Dataset.VersionNo,
		RowCount:     analysisCtx.Dataset.RowCount,
		ColumnCount:  analysisCtx.Dataset.ColumnCount,
		QualityScore: analysisCtx.Dataset.QualityScore,
		Columns:      make([]engineclient.AnalyzeColumn, 0, len(analysisCtx.Dataset.Columns)),
	}
	for _, column := range analysisCtx.Dataset.Columns {
		dataset.Columns = append(dataset.Columns, engineclient.AnalyzeColumn{
			Name:         column.Name,
			OriginalName: column.OriginalName,
			Type:         column.Type,
			Nullable:     column.Nullable,
		})
	}
	messages := make([]engineclient.AnalyzeMessage, 0, len(analysisCtx.Messages))
	for _, m := range analysisCtx.Messages {
		messages = append(messages, engineclient.AnalyzeMessage{Role: m.Role, Content: m.Content})
	}

	return RunStart{
		RunID:       uuid.UUID(run.ID.Bytes),
		WorkspaceID: uuid.UUID(session.WorkspaceID.Bytes),
		DatasetID:   uuid.UUID(session.DatasetID.Bytes),
		VersionNo:   version.VersionNo,
		Dataset:     dataset,
		Messages:    messages,
	}, nil
}

// CompleteRun stores the assistant reply and the run outcome atomically.
func (s *Service) CompleteRun(ctx context.Context, in CompleteRunInput) error {
	var assistantContent string
	if len(in.ResultJSON) > 0 {
		var payload struct {
			Narrative string `json:"narrative"`
		}
		_ = json.Unmarshal(in.ResultJSON, &payload)
		assistantContent = payload.Narrative
	}
	run, err := s.repo.GetRun(ctx, in.RunID)
	if err != nil {
		return err
	}
	sessionID := uuid.UUID(run.SessionID.Bytes)
	if assistantContent != "" {
		message, err := s.repo.CreateMessage(ctx, sessionID, "assistant", assistantContent, "")
		if err != nil {
			return err
		}
		// Link the run to its assistant message so history can find the artifacts.
		if err := s.repo.SetAssistantMessage(ctx, in.RunID, uuid.UUID(message.ID.Bytes)); err != nil {
			return err
		}
	}
	if err := s.repo.InsertResult(ctx, in.RunID, in.ToolName, in.ResultJSON, in.ChartJSON, in.PlanJSON); err != nil {
		return err
	}
	return s.repo.CompleteRun(ctx, in.RunID, in.PromptTokens, in.CompletionTokens)
}

// FailRun marks a run failed with a stable code.
func (s *Service) FailRun(ctx context.Context, runID uuid.UUID, code string) error {
	return s.repo.FailRun(ctx, runID, code)
}

// SessionWorkspace resolves the workspace of a session the caller may read. It
// exists so quota can be enforced before a run row or a provider call is made.
func (s *Service) SessionWorkspace(ctx context.Context, sessionID, userID uuid.UUID) (uuid.UUID, error) {
	row, err := s.repo.GetSession(ctx, sessionID, userID)
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.UUID(row.WorkspaceID.Bytes), nil
}

// CreateSession opens a conversation about a dataset the caller can see.
func (s *Service) CreateSession(ctx context.Context, workspaceID, datasetID, userID uuid.UUID, title string) (Session, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = defaultTitle
	}
	if len(title) > maxTitleLength {
		return Session{}, ErrInvalidTitle
	}
	// Membership and dataset existence are resolved in one query, so a foreign
	// workspace id cannot be used to create a session on someone else's dataset.
	if _, err := s.repo.AnalyzableVersion(ctx, datasetID, workspaceID, userID); err != nil {
		return Session{}, err
	}
	row, err := s.repo.CreateSession(ctx, workspaceID, datasetID, userID, title)
	if err != nil {
		return Session{}, err
	}
	return toSession(row), nil
}

// ListSessions returns one page of sessions for a workspace.
func (s *Service) ListSessions(ctx context.Context, workspaceID, userID uuid.UUID, page, perPage int) ([]Session, int64, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	rows, total, err := s.repo.ListSessions(ctx, workspaceID, userID, int32(perPage), int32((page-1)*perPage))
	if err != nil {
		return nil, 0, err
	}
	out := make([]Session, 0, len(rows))
	for _, row := range rows {
		out = append(out, toSession(row))
	}
	return out, total, nil
}

// GetSession returns a session with its history and compiled context.
func (s *Service) GetSession(ctx context.Context, sessionID, userID uuid.UUID) (SessionDetail, error) {
	row, err := s.repo.GetSession(ctx, sessionID, userID)
	if err != nil {
		return SessionDetail{}, err
	}
	messages, err := s.repo.Messages(ctx, sessionID)
	if err != nil {
		return SessionDetail{}, err
	}
	turns, err := s.repo.Turns(ctx, sessionID)
	if err != nil {
		return SessionDetail{}, err
	}
	version, err := s.repo.AnalyzableVersion(ctx, uuid.UUID(row.DatasetID.Bytes), uuid.UUID(row.WorkspaceID.Bytes), userID)
	if err != nil {
		return SessionDetail{}, err
	}
	analysisCtx, err := s.buildContext(ctx, row, version, messages)
	if err != nil {
		return SessionDetail{}, err
	}
	return SessionDetail{Session: toSession(row), Messages: toTurns(turns), Context: analysisCtx}, nil
}

// buildContext loads column metadata and the profile for the session's current
// dataset version, then compiles the engine payload.
func (s *Service) buildContext(ctx context.Context, session dbgen.AnalysisSession, version dbgen.GetAnalyzableDatasetVersionRow, messages []dbgen.AnalysisMessage) (AnalysisContext, error) {
	if version.Status != "ready" {
		return AnalysisContext{}, ErrNotReady
	}
	versionID := uuid.UUID(version.VersionID.Bytes)
	columns, err := s.repo.Columns(ctx, versionID)
	if err != nil {
		return AnalysisContext{}, err
	}
	profile, err := s.repo.Profile(ctx, versionID)
	if err != nil {
		return AnalysisContext{}, err
	}
	return BuildContext(
		uuid.UUID(version.DatasetID.Bytes).String(),
		version.VersionNo,
		version.RowCount.Int64,
		version.ColumnCount.Int32,
		columns,
		profile,
		messages,
	)
}

func toSession(row dbgen.AnalysisSession) Session {
	return Session{
		ID:          uuid.UUID(row.ID.Bytes),
		WorkspaceID: uuid.UUID(row.WorkspaceID.Bytes),
		DatasetID:   uuid.UUID(row.DatasetID.Bytes),
		Title:       row.Title,
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
	}
}

func toTurns(rows []dbgen.ListAnalysisSessionTurnsRow) []Message {
	out := make([]Message, 0, len(rows))
	for _, row := range rows {
		out = append(out, Message{
			ID:         uuid.UUID(row.ID.Bytes),
			Role:       row.Role,
			Content:    row.Content,
			CreatedAt:  row.CreatedAt.Time,
			ResultJSON: row.ResultJson,
			ChartJSON:  row.ChartSpecJson,
			PlanJSON:   row.PlanJson,
		})
	}
	return out
}
