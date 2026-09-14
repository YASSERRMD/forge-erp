package platform

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Row-level security tenant wiring (Phase 0 task 3 remainder).
//
// Migration 0024 defines fail-closed tenant policies (entity_id matched
// against app.entity_id, deny when unset) on every entity-owned table.
// Enforcement (ENABLE + FORCE) follows with Phase 1 service completion;
// until then these helpers prepare the paths that already run in
// transactions so they are correct on day one.

// SetEntityID pins the tenant for the current transaction. It must run as
// the first statement inside the transaction: SET LOCAL is transaction
// scoped and has no effect outside one.
func SetEntityID(ctx context.Context, db DBTX, entityID int64) error {
	_, err := db.Exec(ctx, fmt.Sprintf("SET LOCAL app.entity_id = '%d'", entityID))
	return err
}

// TxEntity runs fn inside a transaction with app.entity_id set first, so
// row-level security confines every statement to the entity (rolls back on
// error or panic, like Tx).
func TxEntity(ctx context.Context, pool *pgxpool.Pool, entityID int64, fn func(pgx.Tx) error) error {
	return Tx(ctx, pool, func(tx pgx.Tx) error {
		if err := SetEntityID(ctx, tx, entityID); err != nil {
			return err
		}
		return fn(tx)
	})
}
