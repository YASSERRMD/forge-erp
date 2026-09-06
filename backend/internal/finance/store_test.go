package finance

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStorePostAndTrial(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	rev, exp, vat := &Account{EntityID: 1, Code: "707000", Label: "Sales", Type: "revenue"},
		&Account{EntityID: 1, Code: "411000", Label: "Customers", Type: "asset"},
		&Account{EntityID: 1, Code: "445700", Label: "VAT", Type: "liability"}
	for _, a := range []*Account{rev, exp, vat} {
		if err := st.CreateAccount(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	j := &Journal{EntityID: 1, Code: "VEN", Label: "Sales journal"}
	if err := st.CreateJournal(ctx, j); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	e := &Entry{EntityID: 1, JournalID: j.ID, Ref: "VEN-1", Date: now, Memo: "sale",
		Lines: []EntryLine{
			{AccountID: exp.ID, Label: "customer", Debit: 1200},
			{AccountID: rev.ID, Label: "sale", Credit: 1000},
			{AccountID: vat.ID, Label: "vat", Credit: 200},
		}}
	if err := st.PostEntry(ctx, e); err != nil {
		t.Fatal(err)
	}
	// Unbalanced rejected.
	bad := &Entry{EntityID: 1, JournalID: j.ID, Ref: "VEN-2", Date: now,
		Lines: []EntryLine{{AccountID: exp.ID, Debit: 100}, {AccountID: rev.ID, Credit: 99}}}
	if err := st.PostEntry(ctx, bad); err == nil {
		t.Fatal("unbalanced posted")
	}
	// Locked year rejected.
	fy := &FiscalYear{EntityID: 1, Label: "lock", StartDate: now.Add(-time.Hour),
		EndDate: now.Add(time.Hour), Locked: true}
	if err := st.CreateFiscalYear(ctx, fy); err != nil {
		t.Fatal(err)
	}
	locked := &Entry{EntityID: 1, JournalID: j.ID, Ref: "VEN-3", Date: now,
		Lines: []EntryLine{{AccountID: exp.ID, Debit: 10}, {AccountID: rev.ID, Credit: 10}}}
	if err := st.PostEntry(ctx, locked); err == nil {
		t.Fatal("locked-year entry posted")
	}
	// Trial balance sums to zero.
	tb, err := st.TrialBalance(ctx, 1)
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
	ents, _ := st.EntriesByJournal(ctx, j.ID)
	if err := VerifyChain(ents); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryStoreBankReconcile(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	ba := &BankAccount{EntityID: 1, Code: "BNK1", Label: "Main"}
	if err := st.CreateBankAccount(ctx, ba); err != nil {
		t.Fatal(err)
	}
	tx := &BankTransaction{EntityID: 1, AccountID: ba.ID, Amount: 5000, Label: "transfer", ValueDate: time.Now().UTC()}
	if err := st.RecordTransaction(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := st.Reconcile(ctx, tx.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.Reconcile(ctx, tx.ID, time.Now().UTC()); err == nil {
		t.Fatal("double reconcile accepted")
	}
	bal, _ := st.AccountBalance(ctx, ba.ID)
	if bal != 5000 {
		t.Fatalf("balance = %d", bal)
	}
}
