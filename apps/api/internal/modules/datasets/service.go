package datasets

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"insightflow/api/internal/platform/database"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/pgconv"
	"insightflow/api/internal/platform/storage"
)

// Limits are the per-workspace and per-format processing bounds.
type Limits struct {
	MaxDatasetsPerWorkspace int
	MaxRowsCSV              int64
	MaxRowsXLSX             int64
	MaxColumns              int
	PurgeGraceDays          int
}

// retentionVersions is how many dataset versions are kept after a reprocess
// succeeds; older versions are removed file-first (Plan 11, FR-DS-11).
const retentionVersions = 3

// Dataset is the API projection of a dataset with its current version.
type Dataset struct {
	ID               uuid.UUID
	WorkspaceID      uuid.UUID
	Name             string
	CurrentVersionNo int32
	Status           string
	FileFormat       string
	OriginalName     string
	RowCount         *int64
	ColumnCount      *int32
	ByteSize         *int64
	QualityScore     *float64
	ErrorCode        string
	ErrorMessage     string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ColumnResult is one column produced by the engine.
type ColumnResult struct {
	Ordinal       int32
	Name          string
	OriginalName  string
	LogicalType   string
	Role          string
	NullCount     int64
	DistinctCount int64
}

// ProcessResult carries the engine output persisted on success.
type ProcessResult struct {
	RowCount         int64
	ColumnCount      int32
	Columns          []ColumnResult
	ParquetSHA256    string
	ParquetSizeBytes int64
	Profile          json.RawMessage
	ProcessingReport json.RawMessage
}

// CreateInput is the validated upload metadata.
type CreateInput struct {
	DatasetID      uuid.UUID
	WorkspaceID    uuid.UUID
	UserID         uuid.UUID
	Name           string
	Format         string
	OriginalName   string
	SizeBytes      int64
	ChecksumSHA256 string
	SourcePath     string
}

// Notifier wakes idle job workers after a new job commits. It is optional: a
// nil Notifier leaves the worker on plain polling.
type Notifier interface {
	Signal(ctx context.Context) error
}

// Service implements dataset use cases.
type Service struct {
	repo     *Repository
	tx       *database.TxManager
	resolver *storage.Resolver
	limits   Limits
	notifier Notifier
}

// NewService builds the dataset service.
func NewService(repo *Repository, tx *database.TxManager, resolver *storage.Resolver, limits Limits, notifier Notifier) *Service {
	return &Service{repo: repo, tx: tx, resolver: resolver, limits: limits, notifier: notifier}
}

// Resolver exposes the storage resolver for the job worker.
func (s *Service) Resolver() *storage.Resolver { return s.resolver }

// MaxRowsForFormat returns the row ceiling applied to a run.
func (s *Service) MaxRowsForFormat(format string) int64 {
	if format == FormatXLSX {
		return s.limits.MaxRowsXLSX
	}
	return s.limits.MaxRowsCSV
}

// MaxColumns returns the column ceiling.
func (s *Service) MaxColumns() int { return s.limits.MaxColumns }

// QuotaExceeded reports whether the workspace already holds the dataset limit.
func (s *Service) QuotaExceeded(ctx context.Context, workspaceID uuid.UUID) (bool, error) {
	n, err := s.repo.CountForWorkspace(ctx, workspaceID)
	if err != nil {
		return false, err
	}
	return int(n) >= s.limits.MaxDatasetsPerWorkspace, nil
}

// Create registers the dataset, its first version, and the ingest job atomically.
func (s *Service) Create(ctx context.Context, in CreateInput) (Dataset, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Dataset{}, fmt.Errorf("datasets: name is required")
	}
	if len(name) > 200 {
		return Dataset{}, fmt.Errorf("datasets: name too long")
	}

	var out dbgen.Dataset
	err := s.tx.WithinTx(ctx, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		created, err := q.CreateDataset(ctx, dbgen.CreateDatasetParams{
			ID:          pgconv.UUID(in.DatasetID),
			WorkspaceID: pgconv.UUID(in.WorkspaceID),
			Name:        name,
			Description: "",
			CreatedBy:   pgconv.UUID(in.UserID),
		})
		if err != nil {
			return mapDatasetError(err)
		}
		version, err := q.CreateDatasetVersion(ctx, dbgen.CreateDatasetVersionParams{
			DatasetID:      created.ID,
			VersionNo:      1,
			FileFormat:     in.Format,
			OriginalName:   in.OriginalName,
			ByteSize:       pgtype.Int8{Int64: in.SizeBytes, Valid: true},
			ChecksumSha256: pgtype.Text{String: in.ChecksumSHA256, Valid: in.ChecksumSHA256 != ""},
			SourcePath:     in.SourcePath,
		})
		if err != nil {
			return err
		}
		if _, err := q.EnqueueJob(ctx, dbgen.EnqueueJobParams{
			WorkspaceID: created.WorkspaceID,
			VersionID:   version.ID,
			JobType:     "dataset_ingest",
		}); err != nil {
			return err
		}
		out = created
		return nil
	})
	if err != nil {
		return Dataset{}, err
	}

	// Best-effort: the job is already committed, so a failed signal only
	// delays processing until the worker's next poll.
	s.notify(ctx)

	return Dataset{
		ID:               uuid.UUID(out.ID.Bytes),
		WorkspaceID:      uuid.UUID(out.WorkspaceID.Bytes),
		Name:             out.Name,
		CurrentVersionNo: out.CurrentVersionNo,
		Status:           "queued",
		FileFormat:       in.Format,
		OriginalName:     in.OriginalName,
		ByteSize:         &in.SizeBytes,
		CreatedAt:        out.CreatedAt.Time,
		UpdatedAt:        out.UpdatedAt.Time,
	}, nil
}

// notify wakes idle workers after a job is committed. A missing adapter or a
// failed signal leaves the worker on polling.
func (s *Service) notify(ctx context.Context) {
	if s.notifier == nil {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.notifier.Signal(sctx); err != nil {
		slog.Warn("job wake signal failed; worker will poll", "error", err)
	}
}

// List returns one page of the workspace datasets visible to the caller.
func (s *Service) List(ctx context.Context, workspaceID, userID uuid.UUID, p ListParams) ([]Dataset, ListResult, error) {
	res, err := s.repo.ListPage(ctx, workspaceID, userID, p)
	if err != nil {
		return nil, res, err
	}
	out := make([]Dataset, 0, len(res.Items))
	for _, r := range res.Items {
		out = append(out, datasetFromSummary(
			r.ID, r.WorkspaceID, r.Name, r.CurrentVersionNo, r.CreatedAt, r.UpdatedAt,
			r.Status, r.FileFormat, r.OriginalName, r.RowCount, r.ColumnCount,
			r.ByteSize, r.QualityScore, r.ErrorCode, r.ErrorMessage,
		))
	}
	return out, res, nil
}

// Role returns the caller's role in a workspace.
func (s *Service) Role(ctx context.Context, workspaceID, userID uuid.UUID) (string, error) {
	return s.repo.MemberRole(ctx, workspaceID, userID)
}

// Rename changes a dataset name and returns the refreshed projection.
func (s *Service) Rename(ctx context.Context, datasetID, userID uuid.UUID, name string) (Dataset, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return Dataset{}, ErrInvalidName
	}
	if err := s.repo.Rename(ctx, datasetID, userID, name); err != nil {
		return Dataset{}, err
	}
	return s.Get(ctx, datasetID, userID)
}

// SoftDelete hides a dataset and fails its open jobs. Files are removed later
// by the purge janitor once the grace period elapses.
func (s *Service) SoftDelete(ctx context.Context, datasetID, userID uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		row, err := q.SoftDeleteDataset(ctx, dbgen.SoftDeleteDatasetParams{
			ID:     pgconv.UUID(datasetID),
			UserID: pgconv.UUID(userID),
			Days:   int32(s.limits.PurgeGraceDays),
		})
		if err != nil {
			return mapDatasetError(err)
		}
		return q.FailJobsForDataset(ctx, row.ID)
	})
}

// Reprocess queues a new version from the latest version's immutable source.
func (s *Service) Reprocess(ctx context.Context, datasetID, userID uuid.UUID) (Dataset, error) {
	err := s.tx.WithinTx(ctx, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		ds, err := q.GetDatasetForUpdate(ctx, dbgen.GetDatasetForUpdateParams{
			ID:     pgconv.UUID(datasetID),
			UserID: pgconv.UUID(userID),
		})
		if err != nil {
			return mapDatasetError(err)
		}
		if ds.DeletedAt.Valid {
			return ErrNotFound
		}
		latest, err := q.GetLatestDatasetVersionForUpdate(ctx, pgconv.UUID(datasetID))
		if err != nil {
			return mapDatasetError(err)
		}
		if latest.Status == "queued" || latest.Status == "processing" {
			return ErrBusy
		}
		next := latest.VersionNo + 1
		version, err := q.CreateDatasetVersion(ctx, dbgen.CreateDatasetVersionParams{
			DatasetID:      pgconv.UUID(datasetID),
			VersionNo:      next,
			FileFormat:     latest.FileFormat,
			OriginalName:   latest.OriginalName,
			ByteSize:       latest.ByteSize,
			ChecksumSha256: latest.ChecksumSha256,
			SourcePath:     latest.SourcePath,
		})
		if err != nil {
			return err
		}
		if _, err := q.UpdateDatasetCurrentVersion(ctx, dbgen.UpdateDatasetCurrentVersionParams{
			ID:               pgconv.UUID(datasetID),
			CurrentVersionNo: next,
		}); err != nil {
			return err
		}
		if _, err := q.EnqueueJob(ctx, dbgen.EnqueueJobParams{
			WorkspaceID: ds.WorkspaceID,
			VersionID:   version.ID,
			JobType:     "dataset_ingest",
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Dataset{}, err
	}
	s.notify(ctx)
	return s.Get(ctx, datasetID, userID)
}

// ExplorerTarget is the dataset/version an explorer query runs against.
type ExplorerTarget struct {
	DatasetID   uuid.UUID
	WorkspaceID uuid.UUID
	VersionID   uuid.UUID
	VersionNo   int32
	Columns     map[string]struct{}
}

// ExplorerTarget resolves workspace access and the version to read. A zero
// versionNo selects the dataset's current version. Columns is the authoritative
// identifier allowlist for this version (Plan 05 SEC-PARAM-SQL).
func (s *Service) ExplorerTarget(ctx context.Context, datasetID, userID uuid.UUID, versionNo int32) (ExplorerTarget, error) {
	d, err := s.repo.GetSummary(ctx, datasetID, userID)
	if err != nil {
		return ExplorerTarget{}, err
	}
	want := d.CurrentVersionNo
	if versionNo > 0 {
		want = versionNo
	}
	v, err := s.repo.GetVersionByNo(ctx, datasetID, want)
	if err != nil {
		return ExplorerTarget{}, err
	}
	if v.Status != "ready" {
		return ExplorerTarget{}, ErrNotReady
	}
	columns, err := s.repo.Columns(ctx, uuid.UUID(v.ID.Bytes))
	if err != nil {
		return ExplorerTarget{}, err
	}
	names := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		names[column.ColumnName] = struct{}{}
	}
	return ExplorerTarget{
		DatasetID:   uuid.UUID(d.ID.Bytes),
		WorkspaceID: uuid.UUID(d.WorkspaceID.Bytes),
		VersionID:   uuid.UUID(v.ID.Bytes),
		VersionNo:   v.VersionNo,
		Columns:     names,
	}, nil
}

// Versions lists every version of a dataset the caller can access.
func (s *Service) Versions(ctx context.Context, datasetID, userID uuid.UUID) ([]dbgen.DatasetVersion, error) {
	if _, err := s.repo.GetSummary(ctx, datasetID, userID); err != nil {
		return nil, err
	}
	return s.repo.ListVersions(ctx, datasetID)
}

// Get returns one dataset the caller can access.
func (s *Service) Get(ctx context.Context, datasetID, userID uuid.UUID) (Dataset, error) {
	r, err := s.repo.GetSummary(ctx, datasetID, userID)
	if err != nil {
		return Dataset{}, err
	}
	return datasetFromSummary(
		r.ID, r.WorkspaceID, r.Name, r.CurrentVersionNo, r.CreatedAt, r.UpdatedAt,
		r.Status, r.FileFormat, r.OriginalName, r.RowCount, r.ColumnCount,
		r.ByteSize, r.QualityScore, r.ErrorCode, r.ErrorMessage,
	), nil
}

// VersionMeta is the dataset/version context a job worker needs.
type VersionMeta struct {
	DatasetID      uuid.UUID
	WorkspaceID    uuid.UUID
	VersionNo      int32
	FileFormat     string
	ChecksumSHA256 string
}

// VersionMeta loads the dataset and version context for a queued job.
func (s *Service) VersionMeta(ctx context.Context, versionID uuid.UUID) (VersionMeta, error) {
	v, err := s.repo.GetVersion(ctx, versionID)
	if err != nil {
		return VersionMeta{}, err
	}
	d, err := s.repo.GetDataset(ctx, uuid.UUID(v.DatasetID.Bytes))
	if err != nil {
		return VersionMeta{}, err
	}
	return VersionMeta{
		DatasetID:      uuid.UUID(v.DatasetID.Bytes),
		WorkspaceID:    uuid.UUID(d.WorkspaceID.Bytes),
		VersionNo:      v.VersionNo,
		FileFormat:     v.FileFormat,
		ChecksumSHA256: v.ChecksumSha256.String,
	}, nil
}

// MarkProcessing flags a version as processing (job worker).
func (s *Service) MarkProcessing(ctx context.Context, versionID uuid.UUID) error {
	_, err := s.repo.q.MarkDatasetVersionProcessing(ctx, pgconv.UUID(versionID))
	return err
}

// Complete persists a successful processing run (job worker).
func (s *Service) Complete(ctx context.Context, versionID uuid.UUID, parquetPath string, res ProcessResult) error {
	score := qualityScore(res.Profile)
	report := res.ProcessingReport
	if len(report) == 0 {
		report = []byte("{}")
	}
	return s.tx.WithinTx(ctx, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		ready, err := q.MarkDatasetVersionReady(ctx, dbgen.MarkDatasetVersionReadyParams{
			ID:               pgconv.UUID(versionID),
			RowCount:         pgtype.Int8{Int64: res.RowCount, Valid: true},
			ColumnCount:      pgtype.Int4{Int32: res.ColumnCount, Valid: true},
			ParquetPath:      pgtype.Text{String: parquetPath, Valid: parquetPath != ""},
			ParquetSha256:    pgtype.Text{String: res.ParquetSHA256, Valid: res.ParquetSHA256 != ""},
			ProcessingReport: report,
		})
		if err != nil {
			return err
		}
		for _, col := range res.Columns {
			if _, err := q.InsertDatasetColumn(ctx, dbgen.InsertDatasetColumnParams{
				VersionID:    pgconv.UUID(versionID),
				ColumnName:   col.Name,
				OriginalName: col.OriginalName,
				InferredType: col.LogicalType,
				IsNullable:   true,
				ColumnOrder:  col.Ordinal,
			}); err != nil {
				return err
			}
		}
		profile := res.Profile
		if len(profile) == 0 {
			profile = []byte("{}")
		}
		if _, err := q.UpsertDatasetProfile(ctx, dbgen.UpsertDatasetProfileParams{
			VersionID:    pgconv.UUID(versionID),
			ProfileJson:  profile,
			QualityScore: pgconv.Numeric(score),
		}); err != nil {
			return err
		}
		return enforceRetention(ctx, q, ready.DatasetID)
	})
}

// Fail records a permanent processing failure (job worker).
func (s *Service) Fail(ctx context.Context, versionID uuid.UUID, code, message string) error {
	_, err := s.repo.q.MarkDatasetVersionFailed(ctx, dbgen.MarkDatasetVersionFailedParams{
		ID:           pgconv.UUID(versionID),
		ErrorCode:    pgtype.Text{String: code, Valid: code != ""},
		ErrorMessage: pgtype.Text{String: truncate(message, 500), Valid: message != ""},
	})
	return err
}

func datasetFromSummary(
	id, workspaceID pgtype.UUID, name string, versionNo int32,
	createdAt, updatedAt pgtype.Timestamptz,
	status, format, originalName string, rowCount pgtype.Int8, columnCount pgtype.Int4,
	byteSize pgtype.Int8, quality pgtype.Numeric, errorCode, errorMessage pgtype.Text,
) Dataset {
	return Dataset{
		ID:               uuid.UUID(id.Bytes),
		WorkspaceID:      uuid.UUID(workspaceID.Bytes),
		Name:             name,
		CurrentVersionNo: versionNo,
		Status:           status,
		FileFormat:       format,
		OriginalName:     originalName,
		RowCount:         optionalInt8(rowCount),
		ColumnCount:      optionalInt4(columnCount),
		ByteSize:         optionalInt8(byteSize),
		QualityScore:     optionalNumeric(quality),
		ErrorCode:        errorCode.String,
		ErrorMessage:     errorMessage.String,
		CreatedAt:        createdAt.Time,
		UpdatedAt:        updatedAt.Time,
	}
}

func qualityScore(profile json.RawMessage) float64 {
	var parsed struct {
		QualityScore float64 `json:"quality_score"`
	}
	if len(profile) > 0 {
		_ = json.Unmarshal(profile, &parsed)
	}
	return parsed.QualityScore
}

func optionalInt8(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func optionalInt4(v pgtype.Int4) *int32 {
	if !v.Valid {
		return nil
	}
	return &v.Int32
}

func optionalNumeric(v pgtype.Numeric) *float64 {
	if !v.Valid {
		return nil
	}
	f, err := v.Float64Value()
	if err != nil || !f.Valid {
		return nil
	}
	return &f.Float64
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
