package trigger

// Real-SQL coverage for Emit + Relay against PostgreSQL (runs in CI where
// TEST_DATABASE_URL is set; skipped otherwise).

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestPGEmitRelayExactlyOnce(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	bus := platform.NewMemoryBus()
	var got []platform.Event
	unsub, err := Subscribe(nil, bus, "EXPENSE_PAID", func(_ context.Context, e platform.Event) {
		got = append(got, e)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer unsub()

	def, err := ByName("EXPENSE_PAID")
	if err != nil {
		t.Fatalf("ByName: %v", err)
	}
	// Emit inside the caller's transaction, then commit.
	err = platform.Tx(ctx, pool, func(tx pgx.Tx) error {
		return Emit(ctx, tx, def, 1, map[string]any{
			"id": int64(42), "ref": "EXP-9", "user_login": "ada", "total_minor": int64(3000),
		})
	})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	// Kill simulation: committed, but the relay never ran — nothing delivered.
	if len(got) != 0 {
		t.Fatalf("emit published directly (got=%d)", len(got))
	}
	var pending int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM ferp_outbox
		WHERE delivered_at IS NULL AND subject='forgeerp.hr.expense.paid.v1'`).Scan(&pending); err != nil {
		t.Fatalf("pending count: %v", err)
	}
	if pending != 1 {
		t.Fatalf("pending=%d want 1", pending)
	}

	relay := &Relay{Bus: bus}
	n, err := relay.RunOnce(ctx, pool)
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if n != 1 || len(got) != 1 {
		t.Fatalf("delivered=%d bus=%d want 1/1", n, len(got))
	}
	e := got[0]
	if e.Subject != "forgeerp.hr.expense.paid.v1" || e.Entity != "expense" || e.EntityID != 1 || e.ID != 42 {
		t.Fatalf("routing=%+v want expense/1/42", e)
	}

	n, err = relay.RunOnce(ctx, pool)
	if err != nil {
		t.Fatalf("relay rerun: %v", err)
	}
	if n != 0 || len(got) != 1 {
		t.Fatalf("rerun delivered=%d bus=%d want 0/1", n, len(got))
	}
}
