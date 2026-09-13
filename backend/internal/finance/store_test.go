package finance

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestMemoryStorePostAndTrial(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	rev, exp, vat := &Account{EntityID: 1, Code: "707000", Label: "Sales", Type: "revenue"},
		&Account{EntityID: 1, Code: "411000", Label: "Customers", Type: "asset"},
		&Account{EntityID: 1, Code: "445700", Label: "VAT", Type: "liability"}
	for _, a := range []*Account{rev, exp, vat} {
		if err := st.CreateAccount(ctx, nil, a); err != nil {
			t.Fatal(err)
		}
	}
	j := &Journal{EntityID: 1, Code: "VEN", Label: "Sales journal"}
	if err := st.CreateJournal(ctx, nil, j); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	e := &Entry{EntityID: 1, JournalID: j.ID, Ref: "VEN-1", Date: now, Memo: "sale",
		Lines: []EntryLine{
			{AccountID: exp.ID, Label: "customer", Debit: 1200},
			{AccountID: rev.ID, Label: "sale", Credit: 1000},
			{AccountID: vat.ID, Label: "vat", Credit: 200},
		}}
	if err := st.PostEntry(ctx, nil, e); err != nil {
		t.Fatal(err)
	}
	// Unbalanced rejected.
	bad := &Entry{EntityID: 1, JournalID: j.ID, Ref: "VEN-2", Date: now,
		Lines: []EntryLine{{AccountID: exp.ID, Debit: 100}, {AccountID: rev.ID, Credit: 99}}}
	if err := st.PostEntry(ctx, nil, bad); err == nil {
		t.Fatal("unbalanced posted")
	}
	// Locked year rejected.
	fy := &FiscalYear{EntityID: 1, Label: "lock", StartDate: now.Add(-time.Hour),
		EndDate: now.Add(time.Hour), Locked: true}
	if err := st.CreateFiscalYear(ctx, nil, fy); err != nil {
		t.Fatal(err)
	}
	locked := &Entry{EntityID: 1, JournalID: j.ID, Ref: "VEN-3", Date: now,
		Lines: []EntryLine{{AccountID: exp.ID, Debit: 10}, {AccountID: rev.ID, Credit: 10}}}
	if err := st.PostEntry(ctx, nil, locked); err == nil {
		t.Fatal("locked-year entry posted")
	}
	// Trial balance sums to zero.
	tb, err := st.TrialBalance(ctx, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	var dr, cr int64
	for _, sums := range tb {
		dr += sums[0]
		cr += sums[1]
	}
	if dr != cr || dr != 1200 {
		t.Fatalf("trial: dr=%d cr=%d", dr, cr)
	}
	// Chain verifies.
	ents, _ := st.EntriesByJournal(ctx, nil, j.ID)
	if err := VerifyChain(ents); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryStoreBankReconcile(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	ba := &BankAccount{EntityID: 1, Code: "BNK1", Label: "Main"}
	if err := st.CreateBankAccount(ctx, nil, ba); err != nil {
		t.Fatal(err)
	}
	tx := &BankTransaction{EntityID: 1, AccountID: ba.ID, Amount: 5000, Label: "transfer", ValueDate: time.Now().UTC()}
	if err := st.RecordTransaction(ctx, nil, tx); err != nil {
		t.Fatal(err)
	}
	if err := st.Reconcile(ctx, nil, 2, tx.ID, time.Now().UTC()); err == nil {
		t.Fatal("cross-tenant reconcile accepted")
	}
	if err := st.Reconcile(ctx, nil, 1, tx.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.Reconcile(ctx, nil, 1, tx.ID, time.Now().UTC()); err == nil {
		t.Fatal("double reconcile accepted")
	}
	bal, _ := st.AccountBalance(ctx, nil, ba.ID)
	if bal != 5000 {
		t.Fatalf("balance = %d", bal)
	}
}

func TestPGStorePostAndTrial(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	rev := &Account{EntityID: 1, Code: "707000", Label: "Sales", Type: "revenue"}
	bank := &Account{EntityID: 1, Code: "512000", Label: "Bank", Type: "asset"}
	for _, a := range []*Account{rev, bank} {
		if err := st.CreateAccount(ctx, pool, a); err != nil {
			t.Fatalf("account: %v", err)
		}
	}
	j := &Journal{EntityID: 1, Code: "VEN", Label: "Sales"}
	if err := st.CreateJournal(ctx, pool, j); err != nil {
		t.Fatalf("journal: %v", err)
	}
	e := &Entry{EntityID: 1, JournalID: j.ID, Ref: "PG-1", Date: time.Now().UTC(),
		Lines: []EntryLine{
			{AccountID: bank.ID, Debit: 1200},
			{AccountID: rev.ID, Credit: 1200},
		}}
	if err := st.PostEntry(ctx, pool, e); err != nil {
		t.Fatalf("post: %v", err)
	}
	tb, err := st.TrialBalance(ctx, pool, 1)
	if err != nil {
		t.Fatalf("trial: %v", err)
	}
	var dr, cr int64
	for _, s := range tb {
		dr += s[0]
		cr += s[1]
	}
	if dr != cr || dr != 1200 {
		t.Fatalf("trial dr=%d cr=%d", dr, cr)
	}
}
