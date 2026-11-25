package workspaces

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"insightflow/api/internal/platform/database"
	"insightflow/api/internal/platform/dbgen"
	"insightflow/api/internal/platform/pgconv"
)

// Workspace is the API projection of a workspace.
type Workspace struct {
	ID   uuid.UUID
	Name string
	Slug string
	Role string
}

// Service implements workspace use cases.
type Service struct {
	repo *Repository
	tx   *database.TxManager
}

// NewService builds the workspace service.
func NewService(repo *Repository, tx *database.TxManager) *Service {
	return &Service{repo: repo, tx: tx}
}

// Create makes a workspace and enrolls the creator as owner atomically.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, name string) (Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return Workspace{}, fmt.Errorf("workspaces: name must be 1-100 characters")
	}
	var ws dbgen.Workspace
	err := s.tx.WithinTx(ctx, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		created, err := q.CreateWorkspace(ctx, dbgen.CreateWorkspaceParams{
			Name:      name,
			Slug:      "ws-" + uuid.NewString()[:8],
			CreatedBy: pgconv.UUID(userID),
		})
		if err != nil {
			return err
		}
		if _, err := q.AddWorkspaceMember(ctx, dbgen.AddWorkspaceMemberParams{
			WorkspaceID: created.ID,
			UserID:      pgconv.UUID(userID),
			Role:        "owner",
		}); err != nil {
			return err
		}
		ws = created
		return nil
	})
	if err != nil {
		return Workspace{}, err
	}
	return Workspace{ID: uuid.UUID(ws.ID.Bytes), Name: ws.Name, Slug: ws.Slug, Role: "owner"}, nil
}

// List returns the caller's workspaces.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Workspace, error) {
	rows, err := s.repo.ListForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Workspace, 0, len(rows))
	for _, r := range rows {
		out = append(out, Workspace{
			ID:   uuid.UUID(r.ID.Bytes),
			Name: r.Name,
			Slug: r.Slug,
			Role: r.MemberRole,
		})
	}
	return out, nil
}

// Get returns a workspace the caller is a member of.
func (s *Service) Get(ctx context.Context, userID, workspaceID uuid.UUID) (Workspace, error) {
	row, err := s.repo.GetWithMember(ctx, workspaceID, userID)
	if err != nil {
		return Workspace{}, err
	}
	return Workspace{ID: uuid.UUID(row.ID.Bytes), Name: row.Name, Slug: row.Slug, Role: row.MemberRole}, nil
}

// MemberRole implements the membership lookup used by the workspace middleware.
func (s *Service) MemberRole(ctx context.Context, workspaceID, userID uuid.UUID) (string, error) {
	row, err := s.repo.GetWithMember(ctx, workspaceID, userID)
	if err != nil {
		return "", err
	}
	return row.MemberRole, nil
}
