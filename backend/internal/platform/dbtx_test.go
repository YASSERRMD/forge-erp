package platform_test

import (
	"context"
	"errors"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

// TestJoinTx proves the Phase 0 task 4 join semantics: an owned transaction
// commits/rolls back via finish, while a joined (already-a-tx) handle is a
// no-op for finish and sees the owner's uncommitted writes.
func TestJoinTx(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS ferp_jointx_probe (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, v INT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM ferp_jointx_probe`) })
	count := func(db platform.DBTX) int {
		var n int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM ferp_jointx_probe`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Owned: finish(nil) commits.
	tx, finish, err := platform.JoinTx(ctx, pool, pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ferp_jointx_probe (v) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
	if n := count(pool); n != 1 {
		t.Fatalf("owned commit: rows = %d want 1", n)
	}

	// Owned: finish(err) rolls back.
	tx, finish, err = platform.JoinTx(ctx, pool, pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ferp_jointx_probe (v) VALUES (2)`); err != nil {
		t.Fatal(err)
	}
	if err := finish(errors.New("boom")); err == nil {
		t.Fatal("owned rollback: expected original error")
	}
	if n := count(pool); n != 1 {
		t.Fatalf("owned rollback: rows = %d want 1", n)
	}

	// Joined: finish is a no-op; writes stay under the owner's control and
	// the joined handle sees the owner's uncommitted rows.
	outer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outer.Rollback(ctx) }()
	jtx, jfinish, err := platform.JoinTx(ctx, pool, outer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outer.Exec(ctx, `INSERT INTO ferp_jointx_probe (v) VALUES (4)`); err != nil {
		t.Fatal(err)
	}
	if n := count(jtx); n != 2 {
		t.Fatalf("joined visibility: rows = %d want 2", n)
	}
	if err := jfinish(nil); err != nil {
		t.Fatal(err)
	}
	if n := count(pool); n != 1 {
		t.Fatalf("joined no-op finish: rows = %d want 1", n)
	}
	if err := outer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(pool); n != 2 {
		t.Fatalf("outer commit: rows = %d want 2", n)
	}
}
