package finance

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestPGChartPackFR(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	st := NewPGStore(pool)
	rep, err := st.LoadChartPack(ctx, pool, 1, "fr") // lowercase accepted
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if rep.Pack != "FR" || rep.Accounts == 0 {
		t.Fatalf("report=%+v want FR with mirrored accounts", rep)
	}
	var packRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ferp_accounting_accounts
		WHERE entity_id=1 AND pack='FR'`).Scan(&packRows); err != nil || packRows < 100 {
		t.Fatalf("pack rows=%d err=%v want >=100", packRows, err)
	}
	if _, err := st.JournalByCode(ctx, pool, 1, "VEN"); err != nil {
		t.Fatalf("VEN journal: %v", err)
	}
	if _, err := st.JournalByCode(ctx, pool, 1, "ACH"); err != nil {
		t.Fatalf("ACH journal: %v", err)
	}
	// Reload is idempotent (conflicts do nothing, mirror adds zero).
	rep2, err := st.LoadChartPack(ctx, pool, 1, "FR")
	if err != nil || rep2.Accounts != 0 {
		t.Fatalf("reload=%+v err=%v want 0 new accounts", rep2, err)
	}
}

func TestPGPostingRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	st := NewPGStore(pool)
	j := &Journal{EntityID: 1, Code: "VEN", Label: "Sales"}
	if err := st.CreateJournal(ctx, pool, j); err != nil {
		t.Fatal(err)
	}
	a := &Account{EntityID: 1, Code: "411", Label: "Clients", Type: "asset"}
	b := &Account{EntityID: 1, Code: "701", Label: "Sales", Type: "revenue"}
	if err := st.CreateAccount(ctx, pool, a); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAccount(ctx, pool, b); err != nil {
		t.Fatal(err)
	}
	e := &Entry{EntityID: 1, JournalID: j.ID, Ref: "INV-PG", Date: time.Now().UTC(),
		Lines: []EntryLine{
			{AccountID: a.ID, Label: "dr", Debit: 100},
			{AccountID: b.ID, Label: "cr", Credit: 100},
		}}
	if err := st.PostEntry(ctx, pool, e); err != nil {
		t.Fatalf("post: %v", err)
	}
	p := &Posting{EntityID: 1, DocType: DocSalesInvoice, DocID: 77, EntryID: e.ID}
	if err := st.RecordPosting(ctx, pool, p); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := st.FindPosting(ctx, pool, 1, DocSalesInvoice, 77)
	if err != nil || got.EntryID != e.ID {
		t.Fatalf("find=%+v err=%v", got, err)
	}
	list, err := st.ListPostings(ctx, pool, 1, 50, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
}

func TestPGCloseLogRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	st := NewPGStore(pool)
	y := &FiscalYear{EntityID: 1, Label: "PG2026",
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)}
	if err := st.CreateFiscalYear(ctx, pool, y); err != nil {
		t.Fatal(err)
	}
	if err := st.LogClose(ctx, pool, &CloseLogEntry{EntityID: 1, YearID: y.ID,
		Action: CloseActionClose, Note: "pg"}); err != nil {
		t.Fatalf("log: %v", err)
	}
	trail, err := st.ListCloseLog(ctx, pool, 1, y.ID)
	if err != nil || len(trail) != 1 || trail[0].Action != CloseActionClose {
		t.Fatalf("trail=%+v err=%v", trail, err)
	}
}
