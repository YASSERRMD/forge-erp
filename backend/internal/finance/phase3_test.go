package finance

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// fakeLoader serves one canned validated invoice.
type fakeLoader struct {
	doc InvoiceDoc
	err error
}

func (f fakeLoader) LoadValidatedInvoice(_ context.Context, _ platform.DBTX, _ int64, _ int64) (InvoiceDoc, error) {
	if f.err != nil {
		return InvoiceDoc{}, f.err
	}
	return f.doc, nil
}

func testPoster(bindings *MemoryBindingStore) (*Poster, *MemoryStore) {
	st := NewMemoryStore()
	_ = st.CreateJournal(context.Background(), nil, &Journal{EntityID: 1, Code: "VEN", Label: "Sales"})
	_ = st.CreateAccount(context.Background(), nil, &Account{EntityID: 1, Code: "411", Label: "Clients", Type: "asset"})
	_ = st.CreateAccount(context.Background(), nil, &Account{EntityID: 1, Code: "701", Label: "Sales", Type: "revenue"})
	_ = st.CreateAccount(context.Background(), nil, &Account{EntityID: 1, Code: "445", Label: "VAT", Type: "liability"})
	accts, _ := st.Accounts(context.Background(), nil, 1)
	byCode := map[string]int64{}
	for _, a := range accts {
		byCode[a.Code] = a.ID
	}
	ctx := context.Background()
	_ = bindings.SetBinding(ctx, nil, &Binding{EntityID: 1, Kind: BindingPartner, Key: "ACME", AccountID: byCode["411"]})
	_ = bindings.SetBinding(ctx, nil, &Binding{EntityID: 1, Kind: BindingProduct, Key: "default", AccountID: byCode["701"]})
	_ = bindings.SetBinding(ctx, nil, &Binding{EntityID: 1, Kind: BindingVAT, Key: "2000", AccountID: byCode["445"]})
	doc := InvoiceDoc{ID: 7, Ref: "INV-202609-0001", Date: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC),
		OrgRef: "ACME", Net: 1000, VAT: 200, VATSlices: []VATSlice{{RateBps: 2000, VAT: 200}}}
	p := &Poster{Store: st, Postings: NewMemoryPostingStore(), Bindings: bindings,
		Loaders: map[string]InvoiceLoader{DocSalesInvoice: fakeLoader{doc: doc}}, Bus: platform.NewMemoryBus()}
	return p, st
}

func TestAutoPostBalancedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	p, st := testPoster(NewMemoryBindingStore())
	res, err := p.PostValidated(ctx, 1, DocSalesInvoice, 7)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if !res.Created || res.EntryID == 0 {
		t.Fatalf("result=%+v want created", res)
	}
	// Legs balance: DR 1200 control, CR 1000 revenue + 200 VAT.
	ents, _ := st.EntriesByJournal(ctx, nil, 1)
	if len(ents) != 1 {
		t.Fatalf("entries=%d want 1", len(ents))
	}
	var dr, cr int64
	for _, l := range ents[0].Lines {
		dr += l.Debit
		cr += l.Credit
	}
	if dr != 1200 || cr != 1200 {
		t.Fatalf("dr=%d cr=%d want 1200/1200", dr, cr)
	}
	// Replay returns the same entry without posting again.
	res2, err := p.PostValidated(ctx, 1, DocSalesInvoice, 7)
	if err != nil || res2.EntryID != res.EntryID || res2.Created {
		t.Fatalf("replay=%+v err=%v", res2, err)
	}
	ents, _ = st.EntriesByJournal(ctx, nil, 1)
	if len(ents) != 1 {
		t.Fatalf("replay posted again: %d entries", len(ents))
	}
	// Cross-entity replay is independent (no posting row there).
	if _, err := p.PostValidated(ctx, 2, DocSalesInvoice, 7); err == nil {
		t.Fatal("cross-entity post succeeded without bindings")
	}
}

func TestAutoPostFailClosed(t *testing.T) {
	ctx := context.Background()
	p, _ := testPoster(NewMemoryBindingStore())
	// Unknown family rejected.
	if _, err := p.PostValidated(ctx, 1, "delivery_note", 7); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("family err=%v want ErrValidation", err)
	}
	// Unvalidated invoice rejected by the loader.
	p.Loaders[DocSalesInvoice] = fakeLoader{err: errors.Join(errors.New("finance: only validated invoices post"), platform.ErrValidation)}
	if _, err := p.PostValidated(ctx, 1, DocSalesInvoice, 7); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("unvalidated err=%v want ErrValidation", err)
	}
	// Missing VAT binding names the key.
	b := NewMemoryBindingStore()
	ctx2 := context.Background()
	_ = b.SetBinding(ctx2, nil, &Binding{EntityID: 1, Kind: BindingPartner, Key: "ACME", AccountID: 1})
	_ = b.SetBinding(ctx2, nil, &Binding{EntityID: 1, Kind: BindingProduct, Key: "default", AccountID: 2})
	st2 := NewMemoryStore()
	_ = st2.CreateJournal(ctx2, nil, &Journal{EntityID: 1, Code: "VEN", Label: "Sales"})
	doc := InvoiceDoc{ID: 7, Ref: "INV-202609-0001",
		Date:   time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC),
		OrgRef: "ACME", Net: 1000, VAT: 200, VATSlices: []VATSlice{{RateBps: 2000, VAT: 200}}}
	p2 := &Poster{Store: st2, Postings: NewMemoryPostingStore(), Bindings: b,
		Loaders: map[string]InvoiceLoader{DocSalesInvoice: fakeLoader{doc: doc}}, Bus: platform.NewMemoryBus()}
	if _, err := p2.PostValidated(ctx, 1, DocSalesInvoice, 7); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("missing binding err=%v want ErrValidation", err)
	}
}

func TestCloseReopenTrail(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	log := NewMemoryCloseLogStore()
	svc := &Service{Store: st, CloseLog: log}
	_ = st.CreateJournal(ctx, nil, &Journal{EntityID: 1, Code: "OD", Label: "Misc"})
	_ = st.CreateAccount(ctx, nil, &Account{EntityID: 1, Code: "512", Label: "Bank", Type: "asset"})
	_ = st.CreateAccount(ctx, nil, &Account{EntityID: 1, Code: "701", Label: "Sales", Type: "revenue"})
	_ = st.CreateAccount(ctx, nil, &Account{EntityID: 1, Code: "120", Label: "Result", Type: "equity"})
	accts, _ := st.Accounts(ctx, nil, 1)
	byCode := map[string]int64{}
	for _, a := range accts {
		byCode[a.Code] = a.ID
	}
	// A P&L balance to carry forward (empty trial has nothing to close).
	if err := st.PostEntry(ctx, nil, &Entry{EntityID: 1, JournalID: 1, Ref: "SALE-1",
		Date: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		Lines: []EntryLine{
			{AccountID: byCode["512"], Label: "cash", Debit: 5000},
			{AccountID: byCode["701"], Label: "sales", Credit: 5000},
		}}); err != nil {
		t.Fatal(err)
	}
	y := &FiscalYear{EntityID: 1, Label: "2026",
		StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)}
	if err := st.CreateFiscalYear(ctx, nil, y); err != nil {
		t.Fatal(err)
	}
	out, err := svc.CloseYear(ctx, nil, CloseCmd{EntityID: 1, YearID: y.ID,
		JournalID: 1, RetainedAccountID: accts[0].ID, Actor: int64ptr(9), Note: "year end"})
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	trail, err := log.ListCloseLog(ctx, nil, 1, y.ID)
	if err != nil || len(trail) != 1 || trail[0].Action != CloseActionClose {
		t.Fatalf("trail=%+v err=%v", trail, err)
	}
	if trail[0].EntryID == nil || *trail[0].EntryID != out.ID {
		t.Fatalf("close entry link=%+v want %d", trail[0], out.ID)
	}
	// Double close conflicts; reopen trails; second reopen is 422.
	if _, err := svc.CloseYear(ctx, nil, CloseCmd{EntityID: 1, YearID: y.ID,
		JournalID: 1, RetainedAccountID: accts[0].ID}); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("double close err=%v want ErrConflict", err)
	}
	if err := svc.ReopenYear(ctx, nil, ReopenCmd{EntityID: 1, YearID: y.ID, Note: "correction"}); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := svc.ReopenYear(ctx, nil, ReopenCmd{EntityID: 1, YearID: y.ID}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("double reopen err=%v want ErrValidation", err)
	}
	trail, _ = log.ListCloseLog(ctx, nil, 1, y.ID)
	if len(trail) != 2 || trail[1].Action != CloseActionReopen {
		t.Fatalf("trail=%+v want close+reopen", trail)
	}
}

func int64ptr(v int64) *int64 { return &v }

func TestFECGolden(t *testing.T) {
	entries := []Entry{{
		ID: 41, EntityID: 1, JournalID: 7, Ref: "INV-202609-0001",
		Date: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC), Memo: "auto-post",
		Lines: []EntryLine{
			{AccountID: 1, Label: "contrôle ACME", Debit: 1200},
			{AccountID: 2, Label: "revenu\tfacturé", Credit: 1000},
		},
	}}
	journals := map[int64]Journal{7: {ID: 7, Code: "VEN", Label: "Ventes"}}
	accounts := map[int64]Account{
		1: {ID: 1, Code: "411000", Label: "Clients"},
		2: {ID: 2, Code: "701000", Label: "Ventes de produits"},
	}
	var buf bytes.Buffer
	if err := WriteFEC(&buf, BuildFECRows(entries, journals, accounts)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\r\n"), "\r\n")
	if len(lines) != 3 {
		t.Fatalf("lines=%d want header+2:\n%s", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "JournalCode\tJournalLib\tEcritureNum") {
		t.Fatalf("header=%q", lines[0])
	}
	// Labels unaccented, tabs stripped; amounts comma-formatted.
	if !strings.Contains(lines[1], "controle ACME") || !strings.Contains(lines[1], "411000") {
		t.Fatalf("leg1=%q", lines[1])
	}
	if !strings.Contains(lines[2], "revenu facture") || !strings.Contains(lines[2], "20260917") {
		t.Fatalf("leg2=%q", lines[2])
	}
	for _, want := range []string{"        12,00", "        10,00"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("amount %q missing:\n%s", want, buf.String())
		}
	}
}

func TestChartPackCodes(t *testing.T) {
	for _, code := range []string{"FR", "DE", "US"} {
		if _, ok := ChartPacks[code]; !ok {
			t.Fatalf("pack %s missing", code)
		}
	}
	st := NewPGStore(nil)
	if _, err := st.LoadChartPack(context.Background(), nil, 1, "XX"); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("unknown pack err=%v want ErrValidation", err)
	}
}
