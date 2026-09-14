package platform

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX abstracts the query surface shared by *pgxpool.Pool and pgx.Tx.
// Repository methods take DBTX so service.go can run multi-store
// orchestration inside one platform.Tx transaction (Phase 0 task 4).
// Handlers pass their pool-backed handle; services pass the tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	_ DBTX = (*pgxpool.Pool)(nil)
	_ DBTX = (pgx.Tx)(nil)
)

// JoinTx returns db itself when it already is a transaction; otherwise it
// begins one on pool. Call finish exactly once: finish(nil) commits an owned
// tx (no-op for a joined tx); finish(err) with non-nil err rolls an owned tx
// back (no-op for a joined tx — its owner rolls back). This lets repository
// methods stay atomic for direct handler calls while joining the service
// transaction when one is in flight (Phase 0 task 4).
func JoinTx(ctx context.Context, pool *pgxpool.Pool, db DBTX) (tx pgx.Tx, finish func(error) error, err error) {
	if t, ok := db.(pgx.Tx); ok {
		return t, func(error) error { return nil }, nil
	}
	t, err := pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	return t, func(e error) error {
		if e != nil {
			_ = t.Rollback(ctx)
			return e
		}
		return t.Commit(ctx)
	}, nil
}
