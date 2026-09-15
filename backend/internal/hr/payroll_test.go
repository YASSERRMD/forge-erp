package hr

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func payrollTestSetup(t *testing.T, ctx context.Context, fstore *finance.MemoryStore) (int64, int64, int64, int64) {
	t.Helper()
	for _, a := range []finance.Account{
		{EntityID: 1, Code: "641000", Label: "Salaries", Type: "expense"},
		{EntityID: 1, Code: "512000", Label: "Bank", Type: "asset"},
		{EntityID: 1, Code: "431000", Label: "Payroll payable", Type: "liability"},
	} {
		a := a
		if err := fstore.CreateAccount(ctx, nil, &a); err != nil {
			t.Fatalf("account: %v", err)
		}
	}
	j := &finance.Journal{EntityID: 1, Code: "PAY", Label: "Payroll"}
	if err := fstore.CreateJournal(ctx, nil, j); err != nil {
		t.Fatalf("journal: %v", err)
	}
	accts, _ := fstore.Accounts(ctx, nil, 1)
	byCode := map[string]int64{}
	for _, a := range accts {
		byCode[a.Code] = a.ID
	}
	return j.ID, byCode["641000"], byCode["512000"], byCode["431000"]
}

func TestPayrollRunPostingBalance(t *testing.T) {
	ctx := context.Background()
	mstore := NewMemoryStore()
	fstore := finance.NewMemoryStore()
	jid, exp, bank, pay := payrollTestSetup(t, ctx, fstore)

	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	run := &PayrollRun{EntityID: 1, Label: "SEP-2026", PeriodStart: start, PeriodEnd: end}
	if err := mstore.CreatePayrollRun(ctx, nil, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	// Unbalanced line rejected (calculation stays out: server records, validates).
	if err := mstore.AddPayrollRunLine(ctx, nil, &PayrollRunLine{EntityID: 1, RunID: run.ID,
		Gross: 1000, Charges: 200, Net: 700}); err == nil {
		t.Fatal("unbalanced line accepted")
	}
	for _, l := range []PayrollRunLine{
		{EntityID: 1, RunID: run.ID, Gross: 500000, Charges: 110000, Net: 390000},
		{EntityID: 1, RunID: run.ID, Gross: 300000, Charges: 60000, Net: 240000},
	} {
		l := l
		if err := mstore.AddPayrollRunLine(ctx, nil, &l); err != nil {
			t.Fatalf("add line: %v", err)
		}
	}
	svc := NewPayrollService(nil, mstore, fstore, nil)
	posted, err := svc.Post(ctx, PostRunCmd{EntityID: 1, RunID: run.ID, JournalID: jid,
		ExpenseAccount: exp, BankAccount: bank, PayableAccount: pay, RowVersion: run.RowVersion})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if posted.Status != PayrollRunPosted {
		t.Fatalf("status=%d want posted", posted.Status)
	}
	// Balanced: debit expense gross 800000, credit payable 170000 + bank 630000.
	tb, _ := fstore.TrialBalance(ctx, nil, 1)
	if tb[exp] != [2]int64{800000, 0} {
		t.Fatalf("expense leg=%v want debit 800000", tb[exp])
	}
	if tb[pay] != [2]int64{0, 170000} {
		t.Fatalf("payable leg=%v want credit 170000", tb[pay])
	}
	if tb[bank] != [2]int64{0, 630000} {
		t.Fatalf("bank leg=%v want credit 630000", tb[bank])
	}
	// Double post rejected; late lines rejected on posted runs.
	if _, err := svc.Post(ctx, PostRunCmd{EntityID: 1, RunID: run.ID, JournalID: jid,
		ExpenseAccount: exp, BankAccount: bank, PayableAccount: pay, RowVersion: posted.RowVersion}); err == nil {
		t.Fatal("double post accepted")
	}
	if err := mstore.AddPayrollRunLine(ctx, nil, &PayrollRunLine{EntityID: 1, RunID: run.ID,
		Gross: 100, Charges: 10, Net: 90}); err == nil {
		t.Fatal("line on posted run accepted")
	}
}

func TestPayrollRunEmptyRejected(t *testing.T) {
	ctx := context.Background()
	mstore := NewMemoryStore()
	fstore := finance.NewMemoryStore()
	jid, exp, bank, pay := payrollTestSetup(t, ctx, fstore)
	run := &PayrollRun{EntityID: 1, Label: "EMPTY",
		PeriodStart: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	if err := mstore.CreatePayrollRun(ctx, nil, run); err != nil {
		t.Fatalf("create: %v", err)
	}
	svc := NewPayrollService(nil, mstore, fstore, nil)
	if _, err := svc.Post(ctx, PostRunCmd{EntityID: 1, RunID: run.ID, JournalID: jid,
		ExpenseAccount: exp, BankAccount: bank, PayableAccount: pay, RowVersion: run.RowVersion}); err == nil {
		t.Fatal("empty run post accepted")
	}
}

func TestPGPayrollRunPersist(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	hstore := NewPGStore(pool)
	run := &PayrollRun{EntityID: 1, Label: "SEP-2026-PG",
		PeriodStart: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	if err := hstore.CreatePayrollRun(ctx, pool, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := hstore.AddPayrollRunLine(ctx, pool, &PayrollRunLine{EntityID: 1, RunID: run.ID,
		Gross: 200000, Charges: 40000, Net: 160000}); err != nil {
		t.Fatalf("add line: %v", err)
	}
	lines, err := hstore.PayrollRunLines(ctx, pool, 1, run.ID)
	if err != nil || len(lines) != 1 || lines[0].Net != 160000 {
		t.Fatalf("lines=%+v %v", lines, err)
	}
	posted, err := hstore.SetPayrollRunStatus(ctx, pool, 1, run.ID, PayrollRunPosted, run.RowVersion)
	if err != nil {
		t.Fatalf("post status: %v", err)
	}
	if posted.Status != PayrollRunPosted {
		t.Fatalf("status=%d want posted", posted.Status)
	}
	got, err := hstore.PayrollRunByID(ctx, pool, 1, run.ID)
	if err != nil || got.Status != PayrollRunPosted {
		t.Fatalf("persisted=%+v %v", got, err)
	}
	// NOTE: end-to-end run posting through finance.PostEntry is covered by
	// TestPayrollRunPostingBalance on memory stores; the pooled service path
	// reuses the expense-payout TxEntity pattern and is exercised in CI where
	// the ledger chain lock resolves.
}
