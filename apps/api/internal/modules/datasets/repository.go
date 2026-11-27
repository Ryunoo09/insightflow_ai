// Package datasets owns dataset upload, metadata, and processing results.
package datasets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/pgconv"
)

// ErrNotFound covers a missing dataset and missing membership alike so callers
// return a uniform 404 and cannot probe other tenants' datasets.
var ErrNotFound = errors.New("datasets: not found")

// ErrNameTaken maps to a 409 DATASET_NAME_TAKEN.
var ErrNameTaken = errors.New("datasets: name already taken in workspace")

// ErrBusy maps to a 409 DATASET_BUSY: a version is still queued or processing.
var ErrBusy = errors.New("datasets: a version is still processing")

// ErrNotReady maps to a 409 DATASET_NOT_READY: the version is not readable yet.
var ErrNotReady = errors.New("datasets: version is not ready")

// ErrInvalidName maps to a 422 validation failure.
var ErrInvalidName = errors.New("datasets: invalid name")

// Repository is the dataset data-access layer.
type Repository struct {
	db dbgen.DBTX
	q  *dbgen.Queries
}

// NewRepository wraps sqlc queries over any DB handle (pool or transaction).
func NewRepository(db dbgen.DBTX) *Repository { return &Repository{db: db, q: dbgen.New(db)} }

// CountForWorkspace counts existing datasets for the upload quota.
func (r *Repository) CountForWorkspace(ctx context.Context, workspaceID uuid.UUID) (int64, error) {
	return r.q.CountWorkspaceDatasets(ctx, pgconv.UUID(workspaceID))
}

// ListSummaries lists datasets with their current version.
func (r *Repository) ListSummaries(ctx context.Context, workspaceID, userID uuid.UUID) ([]dbgen.ListWorkspaceDatasetSummariesRow, error) {
	return r.q.ListWorkspaceDatasetSummaries(ctx, dbgen.ListWorkspaceDatasetSummariesParams{
		WorkspaceID: pgconv.UUID(workspaceID),
		UserID:      pgconv.UUID(userID),
	})
}

// GetSummary loads one dataset summary, scoped to a member of its workspace.
func (r *Repository) GetSummary(ctx context.Context, datasetID, userID uuid.UUID) (dbgen.GetDatasetSummaryRow, error) {
	row, err := r.q.GetDatasetSummary(ctx, dbgen.GetDatasetSummaryParams{
		ID:     pgconv.UUID(datasetID),
		UserID: pgconv.UUID(userID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dbgen.GetDatasetSummaryRow{}, ErrNotFound
		}
		return dbgen.GetDatasetSummaryRow{}, err
	}
	return row, nil
}

// GetVersion loads a dataset version.
func (r *Repository) GetVersion(ctx context.Context, versionID uuid.UUID) (dbgen.DatasetVersion, error) {
	v, err := r.q.GetDatasetVersion(ctx, pgconv.UUID(versionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dbgen.DatasetVersion{}, ErrNotFound
		}
		return dbgen.DatasetVersion{}, err
	}
	return v, nil
}

// GetDataset loads a dataset by id.
func (r *Repository) GetDataset(ctx context.Context, datasetID uuid.UUID) (dbgen.Dataset, error) {
	d, err := r.q.GetDatasetByID(ctx, pgconv.UUID(datasetID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dbgen.Dataset{}, ErrNotFound
		}
		return dbgen.Dataset{}, err
	}
	return d, nil
}

// GetVersionByNo loads one version of a dataset.
func (r *Repository) GetVersionByNo(ctx context.Context, datasetID uuid.UUID, versionNo int32) (dbgen.DatasetVersion, error) {
	v, err := r.q.GetDatasetVersionByNo(ctx, dbgen.GetDatasetVersionByNoParams{
		DatasetID: pgconv.UUID(datasetID),
		VersionNo: versionNo,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dbgen.DatasetVersion{}, ErrNotFound
		}
		return dbgen.DatasetVersion{}, err
	}
	return v, nil
}

// MemberRole returns the caller's role in a workspace, or ErrNotFound.
func (r *Repository) MemberRole(ctx context.Context, workspaceID, userID uuid.UUID) (string, error) {
	row, err := r.q.GetWorkspaceWithMember(ctx, dbgen.GetWorkspaceWithMemberParams{
		ID:     pgconv.UUID(workspaceID),
		UserID: pgconv.UUID(userID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return row.MemberRole, nil
}

// Rename changes a dataset name, scoped to a member of its workspace.
func (r *Repository) Rename(ctx context.Context, datasetID, userID uuid.UUID, name string) error {
	_, err := r.q.RenameDataset(ctx, dbgen.RenameDatasetParams{
		ID:     pgconv.UUID(datasetID),
		UserID: pgconv.UUID(userID),
		Name:   name,
	})
	return mapDatasetError(err)
}

// SoftDelete flags a dataset deleted and schedules its purge.
func (r *Repository) SoftDelete(ctx context.Context, datasetID, userID uuid.UUID, graceDays int) (dbgen.Dataset, error) {
	row, err := r.q.SoftDeleteDataset(ctx, dbgen.SoftDeleteDatasetParams{
		ID:     pgconv.UUID(datasetID),
		UserID: pgconv.UUID(userID),
		Days:   int32(graceDays),
	})
	if err != nil {
		return dbgen.Dataset{}, mapDatasetError(err)
	}
	return row, nil
}

// ListPurgeDue returns soft-deleted datasets past their purge deadline.
func (r *Repository) ListPurgeDue(ctx context.Context, limit int32) ([]dbgen.Dataset, error) {
	return r.q.ListPurgeDue(ctx, limit)
}

// Purge removes a dataset row; versions and profile rows cascade.
func (r *Repository) Purge(ctx context.Context, datasetID uuid.UUID) error {
	return r.q.PurgeDataset(ctx, pgconv.UUID(datasetID))
}

// ListVersions returns every version of a dataset.
func (r *Repository) ListVersions(ctx context.Context, datasetID uuid.UUID) ([]dbgen.DatasetVersion, error) {
	return r.q.ListDatasetVersions(ctx, pgconv.UUID(datasetID))
}

// Columns lists the column metadata persisted for a version.
func (r *Repository) Columns(ctx context.Context, versionID uuid.UUID) ([]dbgen.DatasetColumn, error) {
	return r.q.ListDatasetColumns(ctx, pgconv.UUID(versionID))
}

// ListVersionsBeyondRetention returns versions older than the newest keep.
func (r *Repository) ListVersionsBeyondRetention(ctx context.Context, datasetID uuid.UUID, keep int32) ([]dbgen.DatasetVersion, error) {
	return r.q.ListDatasetVersionsBeyondRetention(ctx, dbgen.ListDatasetVersionsBeyondRetentionParams{
		DatasetID: pgconv.UUID(datasetID),
		Offset:    keep,
	})
}

// DeleteVersion removes one version row; its columns/profile cascade.
func (r *Repository) DeleteVersion(ctx context.Context, versionID uuid.UUID) error {
	return r.q.DeleteDatasetVersion(ctx, pgconv.UUID(versionID))
}

// ListParams is the validated server-side list query.
type ListParams struct {
	Page    int
	PerPage int
	Query   string
	Status  string
	Sort    string
	Order   string
}

// ListResult is one page of dataset summaries.
type ListResult struct {
	Items   []dbgen.ListWorkspaceDatasetSummariesRow
	Total   int64
	Page    int
	PerPage int
}

// sortColumns is the allowlist of ORDER BY targets. A user string is never
// interpolated into SQL directly.
var sortColumns = map[string]string{
	"name":       "d.name",
	"created_at": "d.created_at",
	"updated_at": "d.updated_at",
	"row_count":  "v.row_count",
	"byte_size":  "v.byte_size",
	"status":     "v.status",
}

// ListPage runs the paginated/searchable/sortable list query. Filters are
// parameterized; only the allowlisted sort column and asc/desc are interpolated.
func (r *Repository) ListPage(ctx context.Context, workspaceID, userID uuid.UUID, f ListParams) (ListResult, error) {
	col, ok := sortColumns[f.Sort]
	if !ok {
		col = "d.updated_at"
	}
	order := "DESC"
	if strings.EqualFold(f.Order, "asc") {
		order = "ASC"
	}

	args := []any{pgconv.UUID(workspaceID), pgconv.UUID(userID)}
	where := "WHERE d.workspace_id = $1 AND m.user_id = $2 AND d.deleted_at IS NULL"
	if f.Query != "" {
		args = append(args, "%"+escapeLike(f.Query)+"%")
		where += fmt.Sprintf(" AND d.name ILIKE $%d ESCAPE '\\'", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where += fmt.Sprintf(" AND v.status = $%d", len(args))
	}

	const from = "FROM datasets d " +
		"JOIN workspace_members m ON m.workspace_id = d.workspace_id " +
		"JOIN dataset_versions v ON v.dataset_id = d.id AND v.version_no = d.current_version_no "

	var total int64
	if err := r.db.QueryRow(ctx, "SELECT count(*) "+from+where, args...).Scan(&total); err != nil {
		return ListResult{}, err
	}

	page := f.Page
	if page < 1 {
		page = 1
	}
	perPage := f.PerPage
	if perPage < 1 {
		perPage = 20
	}
	dataArgs := append(append([]any{}, args...), int32(perPage), int32((page-1)*perPage))
	sql := "SELECT d.id, d.workspace_id, d.name, d.description, d.current_version_no, " +
		"d.created_at, d.updated_at, " +
		"v.id AS version_id, v.version_no, v.status, v.file_format, v.original_name, " +
		"v.row_count, v.column_count, v.byte_size, v.parquet_path, " +
		"v.error_code, v.error_message, v.updated_at AS version_updated_at, " +
		"p.quality_score " + from +
		"LEFT JOIN dataset_profiles p ON p.version_id = v.id " + where +
		fmt.Sprintf(" ORDER BY %s %s NULLS LAST, d.id", col, order) +
		fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)

	rows, err := r.db.Query(ctx, sql, dataArgs...)
	if err != nil {
		return ListResult{}, err
	}
	defer rows.Close()
	var items []dbgen.ListWorkspaceDatasetSummariesRow
	for rows.Next() {
		var i dbgen.ListWorkspaceDatasetSummariesRow
		if err := rows.Scan(
			&i.ID, &i.WorkspaceID, &i.Name, &i.Description, &i.CurrentVersionNo,
			&i.CreatedAt, &i.UpdatedAt, &i.VersionID, &i.VersionNo, &i.Status,
			&i.FileFormat, &i.OriginalName, &i.RowCount, &i.ColumnCount, &i.ByteSize,
			&i.ParquetPath, &i.ErrorCode, &i.ErrorMessage, &i.VersionUpdatedAt, &i.QualityScore,
		); err != nil {
			return ListResult{}, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, err
	}
	return ListResult{Items: items, Total: total, Page: page, PerPage: perPage}, nil
}

// escapeLike neutralizes LIKE wildcards so a literal % or _ is matched as text.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func mapDatasetError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "datasets_workspace_name_key" {
		return ErrNameTaken
	}
	return err
}
