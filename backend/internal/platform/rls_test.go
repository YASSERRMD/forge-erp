package platform_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

// recordDBTX is a no-op DBTX capturing the last Exec statement.
type recordDBTX struct{ last string }

func (f *recordDBTX) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.last = sql
	return pgconn.CommandTag{}, nil
}

func (f *recordDBTX) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, nil
}

func (f *recordDBTX) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return nil
}

// TestSetEntityIDStatement locks the exact SET LOCAL text the RLS policies
// match on (Phase 0 task 3).
func TestSetEntityIDStatement(t *testing.T) {
	f := &recordDBTX{}
	if err := platform.SetEntityID(context.Background(), f, 7); err != nil {
		t.Fatal(err)
	}
	if f.last != "SET LOCAL app.entity_id = '7'" {
		t.Fatalf("statement = %q", f.last)
	}
}

// TestTxEntitySetsTenant proves the tenant is visible inside the transaction
// (runs against PostgreSQL in CI, skipped locally without TEST_DATABASE_URL).
func TestTxEntitySetsTenant(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	err := platform.TxEntity(ctx, pool, 9, func(tx pgx.Tx) error {
		var got string
		if err := tx.QueryRow(ctx, `SELECT current_setting('app.entity_id', true)`).Scan(&got); err != nil {
			return err
		}
		if strings.TrimSpace(got) != "9" {
			t.Fatalf("app.entity_id = %q want 9", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
