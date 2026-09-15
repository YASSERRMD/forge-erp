package hr

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/trigger"
)

// Memory path (nil Pool): no transaction and no outbox exist, so Pay keeps
// the legacy direct publish — same subject, delivered synchronously.
func TestPayPublishesDirectOnMemoryPath(t *testing.T) {
	ctx := context.Background()
	mstore := NewMemoryStore()
	fstore := finance.NewMemoryStore()
	for _, a := range []finance.Account{
		{EntityID: 1, Code: "625000", Label: "Travel", Type: "expense"},
		{EntityID: 1, Code: "512000", Label: "Bank", Type: "asset"},
	} {
		a := a
		if err := fstore.CreateAccount(ctx, nil, &a); err != nil {
			t.Fatalf("account: %v", err)
		}
	}
	j := &finance.Journal{EntityID: 1, Code: "ACH", Label: "Purchases"}
	if err := fstore.CreateJournal(ctx, nil, j); err != nil {
		t.Fatalf("journal: %v", err)
	}
	rep := &ExpenseReport{EntityID: 1, Ref: "EXP-PAY-MEM", UserLogin: "ada"}
	if err := mstore.CreateExpense(ctx, nil, rep); err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := mstore.AddExpenseLine(ctx, nil, &ExpenseLine{EntityID: 1, ReportID: rep.ID,
		Date: now, Label: "hotel", Amount: 3000}); err != nil {
		t.Fatalf("line: %v", err)
	}
	upd, err := mstore.SetExpenseStatus(ctx, nil, 1, rep.ID, ExpenseSubmitted, rep.RowVersion)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	upd, err = mstore.SetExpenseStatus(ctx, nil, 1, rep.ID, ExpenseApproved, upd.RowVersion)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	accts, _ := fstore.Accounts(ctx, nil, 1)
	byCode := map[string]int64{}
	for _, a := range accts {
		byCode[a.Code] = a.ID
	}

	bus := platform.NewMemoryBus()
	var got []platform.Event
	unsub, err := trigger.Subscribe(nil, bus, "EXPENSE_PAID", func(_ context.Context, e platform.Event) {
		got = append(got, e)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer unsub()

	svc := NewService(nil, mstore, fstore, bus)
	paid, err := svc.Pay(ctx, PayCmd{EntityID: 1, ReportID: rep.ID, JournalID: j.ID,
		ExpenseAccount: byCode["625000"], BankAccount: byCode["512000"], RowVersion: upd.RowVersion})
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	if paid.Status != ExpensePaid {
		t.Fatalf("status=%d want paid", paid.Status)
	}
	if len(got) != 1 {
		t.Fatalf("bus got=%d want 1", len(got))
	}
	if got[0].Subject != "forgeerp.hr.expense.paid.v1" || got[0].EntityID != 1 || got[0].ID != rep.ID {
		t.Fatalf("event=%+v want subject compat", got[0])
	}
}

// Pooled path (PG-gated): Pay stages EXPENSE_PAID in ferp_outbox inside the
// payout transaction and publishes nothing directly; the relay delivers the
// legacy subject exactly once.
func TestPGPayEmitsOutboxRelayDelivers(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	fstore := finance.NewPGStore(pool)
	for _, a := range []finance.Account{
		{EntityID: 1, Code: "625000", Label: "Travel", Type: "expense"},
		{EntityID: 1, Code: "512000", Label: "Bank", Type: "asset"},
	} {
		a := a
		if err := fstore.CreateAccount(ctx, pool, &a); err != nil {
			t.Fatalf("account: %v", err)
		}
	}
	j := &finance.Journal{EntityID: 1, Code: "ACH", Label: "Purchases"}
	if err := fstore.CreateJournal(ctx, pool, j); err != nil {
		t.Fatalf("journal: %v", err)
	}
	hstore := NewPGStore(pool)
	rep := &ExpenseReport{EntityID: 1, Ref: "EXP-PAY-PG", UserLogin: "ada"}
	if err := hstore.CreateExpense(ctx, pool, rep); err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := hstore.AddExpenseLine(ctx, pool, &ExpenseLine{EntityID: 1, ReportID: rep.ID,
		Date: now, Label: "hotel", Amount: 3000}); err != nil {
		t.Fatalf("line: %v", err)
	}
	upd, err := hstore.SetExpenseStatus(ctx, pool, 1, rep.ID, ExpenseSubmitted, rep.RowVersion)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	upd, err = hstore.SetExpenseStatus(ctx, pool, 1, rep.ID, ExpenseApproved, upd.RowVersion)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	accts, err := fstore.Accounts(ctx, pool, 1)
	if err != nil {
		t.Fatalf("accounts: %v", err)
	}
	byCode := map[string]int64{}
	for _, a := range accts {
		byCode[a.Code] = a.ID
	}

	bus := platform.NewMemoryBus()
	var got []platform.Event
	unsub, err := trigger.Subscribe(nil, bus, "EXPENSE_PAID", func(_ context.Context, e platform.Event) {
		got = append(got, e)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer unsub()

	svc := NewService(pool, hstore, fstore, bus)
	paid, err := svc.Pay(ctx, PayCmd{EntityID: 1, ReportID: rep.ID, JournalID: j.ID,
		ExpenseAccount: byCode["625000"], BankAccount: byCode["512000"], RowVersion: upd.RowVersion})
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	if paid.Status != ExpensePaid {
		t.Fatalf("status=%d want paid", paid.Status)
	}
	// No direct publish: delivery waits for the relay.
	if len(got) != 0 {
		t.Fatalf("pay published directly (got=%d); want outbox", len(got))
	}
	var pending int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM ferp_outbox
		WHERE delivered_at IS NULL AND subject='forgeerp.hr.expense.paid.v1'
		AND entity_id=1 AND object_id=$1`, rep.ID).Scan(&pending); err != nil {
		t.Fatalf("outbox count: %v", err)
	}
	if pending != 1 {
		t.Fatalf("pending=%d want 1", pending)
	}

	relay := &trigger.Relay{Bus: bus}
	n, err := relay.RunOnce(ctx, pool)
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if n < 1 || len(got) < 1 {
		t.Fatalf("delivered=%d bus=%d want >=1", n, len(got))
	}
	last := got[len(got)-1]
	if last.Subject != "forgeerp.hr.expense.paid.v1" || last.Entity != "expense" ||
		last.EntityID != 1 || last.ID != rep.ID {
		t.Fatalf("event=%+v want subject compat", last)
	}
	before := len(got)
	n, err = relay.RunOnce(ctx, pool)
	if err != nil {
		t.Fatalf("relay rerun: %v", err)
	}
	if len(got) != before {
		t.Fatalf("rerun redelivered (bus=%d want %d)", len(got), before)
	}
	_ = n
}
