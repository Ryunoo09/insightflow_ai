// Package workspaces owns tenant workspaces and membership checks.
package workspaces

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/pgconv"
)

// ErrNotFound covers both a missing workspace and missing membership so callers
// can answer with a uniform 404 and avoid revealing other tenants' resources.
var ErrNotFound = errors.New("workspaces: not found")

// Repository is the workspace data-access layer.
type Repository struct {
	q *dbgen.Queries
}

// NewRepository wraps sqlc queries.
func NewRepository(q *dbgen.Queries) *Repository { return &Repository{q: q} }

// Create inserts a workspace.
func (r *Repository) Create(ctx context.Context, name, slug string, createdBy uuid.UUID) (dbgen.Workspace, error) {
	return r.q.CreateWorkspace(ctx, dbgen.CreateWorkspaceParams{
		Name:      name,
		Slug:      slug,
		CreatedBy: pgconv.UUID(createdBy),
	})
}

// AddMember adds a member with the given role.
func (r *Repository) AddMember(ctx context.Context, workspaceID, userID uuid.UUID, role string) error {
	_, err := r.q.AddWorkspaceMember(ctx, dbgen.AddWorkspaceMemberParams{
		WorkspaceID: pgconv.UUID(workspaceID),
		UserID:      pgconv.UUID(userID),
		Role:        role,
	})
	return err
}

// GetWithMember loads a workspace only when userID is a member.
func (r *Repository) GetWithMember(ctx context.Context, workspaceID, userID uuid.UUID) (dbgen.GetWorkspaceWithMemberRow, error) {
	row, err := r.q.GetWorkspaceWithMember(ctx, dbgen.GetWorkspaceWithMemberParams{
		ID:     pgconv.UUID(workspaceID),
		UserID: pgconv.UUID(userID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dbgen.GetWorkspaceWithMemberRow{}, ErrNotFound
		}
		return dbgen.GetWorkspaceWithMemberRow{}, err
	}
	return row, nil
}

// ListForUser lists every workspace the user belongs to.
func (r *Repository) ListForUser(ctx context.Context, userID uuid.UUID) ([]dbgen.ListUserWorkspacesRow, error) {
	return r.q.ListUserWorkspaces(ctx, pgconv.UUID(userID))
}
