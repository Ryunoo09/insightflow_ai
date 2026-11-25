package workspaces

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"

	"insightflow/api/internal/modules/users"
	"insightflow/api/internal/platform/database"
	"insightflow/api/internal/platform/dbgen"
)

func newTestService(t *testing.T) (*Service, *users.Repository) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := database.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("database.NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	q := dbgen.New(pool)
	return NewService(NewRepository(q), database.NewTxManager(pool)), users.NewRepository(q)
}

func activeUser(t *testing.T, repo *users.Repository) uuid.UUID {
	t.Helper()
	u, err := repo.Create(context.Background(), uuid.NewString()+"@example.com", "", "Member", "active")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return uuid.UUID(u.ID.Bytes)
}

func TestCreateEnrollsOwner(t *testing.T) {
	svc, usersRepo := newTestService(t)
	ctx := context.Background()
	owner := activeUser(t, usersRepo)

	ws, err := svc.Create(ctx, owner, "Acme Analytics")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ws.Role != "owner" {
		t.Fatalf("role = %q, want owner", ws.Role)
	}
	got, err := svc.Get(ctx, owner, ws.ID)
	if err != nil || got.ID != ws.ID {
		t.Fatalf("owner Get = %+v, %v", got, err)
	}
}

func TestCrossTenantAccessIsNotFound(t *testing.T) {
	svc, usersRepo := newTestService(t)
	ctx := context.Background()
	owner := activeUser(t, usersRepo)
	intruder := activeUser(t, usersRepo)

	ws, err := svc.Create(ctx, owner, "Private Workspace")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Get(ctx, intruder, ws.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("intruder Get err = %v, want ErrNotFound", err)
	}
	if _, err := svc.MemberRole(ctx, ws.ID, intruder); !errors.Is(err, ErrNotFound) {
		t.Fatalf("intruder MemberRole err = %v, want ErrNotFound", err)
	}
}

func TestCreateRejectsEmptyName(t *testing.T) {
	svc, usersRepo := newTestService(t)
	owner := activeUser(t, usersRepo)
	if _, err := svc.Create(context.Background(), owner, "   "); err == nil {
		t.Fatal("empty workspace name accepted")
	}
}
