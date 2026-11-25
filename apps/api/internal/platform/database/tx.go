// Package database provides PostgreSQL connection and transaction helpers.
package database

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TxManager runs work inside a single database transaction.
type TxManager struct {
	pool *pgxpool.Pool
}

// NewTxManager wraps a connection pool.
func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool}
}

// WithinTx runs fn in a transaction: commit on nil, rollback on error.
// The deferred rollback is a no-op after a successful commit, so panics and
// early returns never leave an open transaction.
func (m *TxManager) WithinTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
