package trigger

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Emit → kill-simulation (commit happened, relay never ran) → relay →
// delivered exactly once; a second run delivers nothing.
func TestEmitRelayExactlyOnce(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	bus := platform.NewMemoryBus()
	var got []platform.Event
	unsub, err := Subscribe(nil, bus, "EXPENSE_PAID", func(_ context.Context, e platform.Event) {
		got = append(got, e)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer unsub()

	ev := ExpensePaid{Entity: 1, ExpenseID: 42, Ref: "EXP-9", UserLogin: "ada", Total: 3000}
	if err := EmitEvent(ctx, db, ev); err != nil {
		t.Fatalf("emit: %v", err)
	}

	// Kill simulation: the process died after commit, before any publish.
	if len(got) != 0 {
		t.Fatalf("emit published directly (got=%d)", len(got))
	}
	if n := db.pending(); n != 1 {
		t.Fatalf("pending=%d want 1", n)
	}

	relay := &Relay{Bus: bus}
	n, err := relay.RunOnce(ctx, db)
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if n != 1 {
		t.Fatalf("delivered=%d want 1", n)
	}
	if len(got) != 1 {
		t.Fatalf("bus got=%d want 1", len(got))
	}
	e := got[0]
	if e.Subject != "forgeerp.hr.expense.paid.v1" {
		t.Errorf("subject=%q want legacy compat", e.Subject)
	}
	if e.Entity != "expense" || e.EntityID != 1 || e.ID != 42 {
		t.Errorf("routing=%+v want expense/1/42", e)
	}
	if e.Payload["ref"] != "EXP-9" || e.Payload["total_minor"] != float64(3000) {
		t.Errorf("payload=%v want ref + total (float64 after JSON round-trip)", e.Payload)
	}

	n, err = relay.RunOnce(ctx, db)
	if err != nil {
		t.Fatalf("relay rerun: %v", err)
	}
	if n != 0 || len(got) != 1 {
		t.Fatalf("rerun delivered=%d bus=%d want 0/1", n, len(got))
	}
}

func TestRelayEmptyAndBatching(t *testing.T) {
	ctx := context.Background()
	db := newFakeDB()
	bus := platform.NewMemoryBus()
	relay := &Relay{Bus: bus, Batch: 2}

	n, err := relay.RunOnce(ctx, db)
	if err != nil || n != 0 {
		t.Fatalf("empty run n=%d err=%v", n, err)
	}
	def, _ := ByName("BILL_VALIDATE")
	for i := int64(1); i <= 3; i++ {
		if err := Emit(ctx, db, def, 1, map[string]any{"id": i}); err != nil {
			t.Fatalf("emit %d: %v", i, err)
		}
	}
	var count int
	bus.Subscribe(def.Subject, func(context.Context, platform.Event) { count++ })
	n, err = relay.RunOnce(ctx, db)
	if err != nil || n != 2 || count != 2 {
		t.Fatalf("batch run n=%d count=%d err=%v want 2/2", n, count, err)
	}
	n, err = relay.RunOnce(ctx, db)
	if err != nil || n != 1 || count != 3 {
		t.Fatalf("drain run n=%d count=%d err=%v want 1/3", n, count, err)
	}
}

func TestRelayNeedsBusAndDB(t *testing.T) {
	ctx := context.Background()
	if _, err := (&Relay{}).RunOnce(ctx, newFakeDB()); err == nil {
		t.Fatal("nil bus accepted")
	}
	if _, err := (&Relay{Bus: platform.NewMemoryBus()}).RunOnce(ctx, nil); err == nil {
		t.Fatal("nil db accepted")
	}
}
