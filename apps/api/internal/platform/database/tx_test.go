package database

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := NewPool(context.Background(), url)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestWithinTxCommits(t *testing.T) {
	pool := testPool(t)
	mgr := NewTxManager(pool)
	ctx := context.Background()

	err := mgr.WithinTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "CREATE TEMP TABLE tx_probe (v int) ON COMMIT DROP"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "INSERT INTO tx_probe (v) VALUES (1)")
		return err
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}
}

func TestWithinTxRollsBack(t *testing.T) {
	pool := testPool(t)
	mgr := NewTxManager(pool)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, "CREATE TABLE IF NOT EXISTS tx_rollback_probe (v int)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP TABLE IF EXISTS tx_rollback_probe")
	})
	if _, err := pool.Exec(ctx, "TRUNCATE tx_rollback_probe"); err != nil {
		t.Fatal(err)
	}

	sentinel := errors.New("boom")
	err := mgr.WithinTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO tx_rollback_probe (v) VALUES (42)"); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithinTx err = %v, want sentinel", err)
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM tx_rollback_probe").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rollback left %d rows, want 0", count)
	}
}
