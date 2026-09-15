package reporting

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
	"github.com/go-chi/chi/v5"
)

type comptaFixture struct {
	ledger *finance.MemoryStore
	bankID int64
	sales  int64
	purch  int64
}

func setupCompta(t *testing.T) comptaFixture {
	t.Helper()
	ctx := context.Background()
	ledger := finance.NewMemoryStore()
	bank := &finance.Account{EntityID: 1, Code: "512000", Label: "Bank", Type: "asset"}
	rev := &finance.Account{EntityID: 1, Code: "707000", Label: "Sales", Type: "revenue"}
	exp := &finance.Account{EntityID: 1, Code: "607000", Label: "Purchases", Type: "expense"}
	for _, a := range []*finance.Account{bank, rev, exp} {
		if err := ledger.CreateAccount(ctx, nil, a); err != nil {
			t.Fatalf("account: %v", err)
		}
	}
	j := &finance.Journal{EntityID: 1, Code: "VEN", Label: "Sales"}
	if err := ledger.CreateJournal(ctx, nil, j); err != nil {
		t.Fatalf("journal: %v", err)
	}
	mk := func(ref string, date time.Time, lines []finance.EntryLine) {
		e := &finance.Entry{EntityID: 1, JournalID: j.ID, Ref: ref, Date: date, Lines: lines}
		if err := ledger.PostEntry(ctx, nil, e); err != nil {
			t.Fatalf("post %s: %v", ref, err)
		}
	}
	mk("E1", time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC), []finance.EntryLine{
		{AccountID: bank.ID, Label: "cust", Debit: 10000},
		{AccountID: rev.ID, Label: "sale", Credit: 10000},
	})
	mk("E2", time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC), []finance.EntryLine{
		{AccountID: exp.ID, Label: "stock", Debit: 4000},
		{AccountID: bank.ID, Label: "paid", Credit: 4000},
	})
	return comptaFixture{ledger: ledger, bankID: bank.ID, sales: rev.ID, purch: exp.ID}
}

func TestAccountDrillDown(t *testing.T) {
	ctx := context.Background()
	f := setupCompta(t)

	drill, err := AccountDrillDown(ctx, nil, 1, f.sales, f.ledger)
	if err != nil {
		t.Fatalf("drill sales: %v", err)
	}
	if drill.Debit != 0 || drill.Credit != 10000 || drill.Balance != 10000 || len(drill.Lines) != 1 {
		t.Fatalf("sales drill=%+v", drill)
	}
	if drill.Lines[0].Ref != "E1" || drill.Lines[0].JournalCode != "VEN" {
		t.Fatalf("sales line=%+v", drill.Lines[0])
	}

	drill, err = AccountDrillDown(ctx, nil, 1, f.bankID, f.ledger)
	if err != nil {
		t.Fatalf("drill bank: %v", err)
	}
	if drill.Debit != 10000 || drill.Credit != 4000 || drill.Balance != 6000 || len(drill.Lines) != 2 {
		t.Fatalf("bank drill=%+v", drill)
	}
	if !drill.Lines[0].Date.Before(drill.Lines[1].Date) {
		t.Fatalf("drill lines not date-ordered: %+v", drill.Lines)
	}

	if _, err := AccountDrillDown(ctx, nil, 1, 9999, f.ledger); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("unknown account err=%v want ErrNotFound", err)
	}
	if _, err := AccountDrillDown(ctx, nil, 1, 0, f.ledger); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("zero account err=%v want ErrValidation", err)
	}
	// Cross-entity: entity 2 has no chart, so the account is not found there.
	if _, err := AccountDrillDown(ctx, nil, 2, f.sales, f.ledger); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("entity 2 err=%v want ErrNotFound", err)
	}
}

func TestGeneralLedgerCSVGolden(t *testing.T) {
	ctx := context.Background()
	f := setupCompta(t)
	rows, err := GeneralLedger(ctx, nil, 1, f.ledger)
	if err != nil {
		t.Fatalf("gl: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("rows=%+v want 4", rows)
	}
	var sb strings.Builder
	if err := WriteGeneralLedgerCSV(&sb, rows); err != nil {
		t.Fatalf("csv: %v", err)
	}
	want := "date,journal,entry_ref,account,account_label,line_label,debit_cents,credit_cents\n" +
		"2026-01-15,VEN,E1,512000,Bank,cust,10000,0\n" +
		"2026-01-15,VEN,E1,707000,Sales,sale,0,10000\n" +
		"2026-03-10,VEN,E2,512000,Bank,paid,0,4000\n" +
		"2026-03-10,VEN,E2,607000,Purchases,stock,4000,0\n"
	if sb.String() != want {
		t.Fatalf("GL CSV mismatch:\n got %q\nwant %q", sb.String(), want)
	}
}

func TestPreviewClose(t *testing.T) {
	ctx := context.Background()
	f := setupCompta(t)
	prev, err := PreviewClose(ctx, nil, 1, f.ledger)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if prev.Revenue != 10000 || prev.Expense != 4000 || prev.Net != 6000 {
		t.Fatalf("prev=%+v", prev)
	}
	if prev.ResultAccount != CloseResultAccount || !prev.Balanced || len(prev.Legs) != 4 {
		t.Fatalf("prev=%+v", prev)
	}
	var dr, cr int64
	for _, l := range prev.Legs {
		if (l.Debit == 0) == (l.Credit == 0) {
			t.Fatalf("leg needs exactly one side: %+v", l)
		}
		dr += l.Debit
		cr += l.Credit
	}
	if dr != cr || dr != 14000 {
		t.Fatalf("legs dr=%d cr=%d want 14000/14000", dr, cr)
	}
}

func TestBuildPNLPeriod(t *testing.T) {
	ctx := context.Background()
	f := setupCompta(t)
	jan, err := BuildPNLPeriod(ctx, nil, 1, f.ledger,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 31, 23, 59, 59, 0, time.UTC))
	if err != nil {
		t.Fatalf("jan pnl: %v", err)
	}
	if jan.Revenue != 10000 || jan.Expense != 0 || jan.Net != 10000 {
		t.Fatalf("jan=%+v", jan)
	}
	full, err := BuildPNLPeriod(ctx, nil, 1, f.ledger,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC))
	if err != nil {
		t.Fatalf("full pnl: %v", err)
	}
	if full.Revenue != 10000 || full.Expense != 4000 || full.Net != 6000 {
		t.Fatalf("full=%+v", full)
	}
	// Zero bounds fall back to all postings.
	all, err := BuildPNLPeriod(ctx, nil, 1, f.ledger, time.Time{}, time.Time{})
	if err != nil || all.Net != 6000 {
		t.Fatalf("fallback=%+v err=%v", all, err)
	}
	bad, err := BuildPNLPeriod(ctx, nil, 1, f.ledger,
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("from-after-to err=%v (%+v)", err, bad)
	}
	if _, err := BuildPNLPeriod(ctx, nil, 1, f.ledger,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Time{}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("single bound err=%v", err)
	}
}

func TestProductMarginsAllProducts(t *testing.T) {
	ctx := context.Background()
	billing := sales.NewMemoryStore()
	stock := catalog.NewMemoryStore()
	var pids []int64
	for _, sku := range []string{"M-1", "M-2"} {
		p := &catalog.Product{EntityID: 1, SKU: sku, Name: sku, Type: catalog.ProductGoods,
			Status: catalog.ProductActive, StockTracked: true}
		if err := stock.CreateProduct(ctx, nil, p); err != nil {
			t.Fatalf("product: %v", err)
		}
		pids = append(pids, p.ID)
	}
	d := &sales.Document{EntityID: 1, Type: documents.TypeInvoice, OrgID: 7,
		Currency: "USD", RateToBase: 1000000,
		Lines: []documents.Line{
			{ProductID: pids[0], Label: "a", Qty: 1, UnitNet: 1000, VATRateBps: 0},
			{ProductID: pids[1], Label: "b", Qty: 2, UnitNet: 500, VATRateBps: 0},
		}}
	if err := billing.CreateDoc(ctx, nil, d, "202609"); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	if _, err := billing.SetStatus(ctx, nil, d.EntityID, d.ID, sales.InvoiceValidated); err != nil {
		t.Fatalf("validate: %v", err)
	}
	lines, err := ProductMargins(ctx, nil, 1, billing, stock)
	if err != nil {
		t.Fatalf("margins: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("margins=%+v want one row per product", lines)
	}
}

func passMiddleware(module, entity, action string) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler { return h }
}

func serveReporting(t *testing.T, d Deps, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	Routes(r, d, passMiddleware)
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(platform.ContextWithEntity(req.Context(), 1))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestReportingHTTPFiles(t *testing.T) {
	billing, purchasing, orgs, stock := collectFixture(t)
	f := setupCompta(t)
	d := Deps{Ledger: f.ledger, Billing: billing, Purchases: purchasing, Stock: stock, Orgs: orgs}

	rec := serveReporting(t, d, "GET", "/reports/intra-eu.deb?home=FR")
	if rec.Code != http.StatusOK {
		t.Fatalf("deb code=%d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("deb content-type=%q", ct)
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "deb") {
		t.Fatalf("deb disposition=%q", rec.Header().Get("Content-Disposition"))
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "H") || strings.Count(body, "\n") != 4 { // header + 3 lines
		t.Fatalf("deb body=%q", body)
	}

	rec = serveReporting(t, d, "GET", "/reports/intra-eu.csv?home=FR&flow=goods")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("csv code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if c := strings.Count(strings.TrimSpace(rec.Body.String()), "\n"); c != 2 { // header + 2 goods rows
		t.Fatalf("csv body=%q", rec.Body.String())
	}

	rec = serveReporting(t, d, "GET", "/reports/general-ledger.csv")
	if rec.Code != http.StatusOK {
		t.Fatalf("gl code=%d", rec.Code)
	}
	if !strings.HasPrefix(rec.Body.String(), "date,journal,entry_ref") {
		t.Fatalf("gl body=%q", rec.Body.String())
	}

	rec = serveReporting(t, d, "GET", "/reports/close-preview")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"net":6000`) {
		t.Fatalf("close code=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = serveReporting(t, d, "GET", "/reports/pnl?from=2026-01-01&to=2026-01-31")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"revenue":10000`) {
		t.Fatalf("pnl code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = serveReporting(t, d, "GET", "/reports/pnl?from=nope&to=2026-01-31")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad pnl code=%d", rec.Code)
	}
}

func TestReportingHTTPLedgerAccount(t *testing.T) {
	_, _, orgs, _ := collectFixture(t)
	f := setupCompta(t)
	d := Deps{Ledger: f.ledger, Orgs: orgs}

	// Memory-store ids are sequence-assigned: bank=1, sales=2, purchases=3.
	rec := serveReporting(t, d, "GET", "/reports/ledger/account?account_id=1")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"balance":6000`) {
		t.Fatalf("drill code=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := serveReporting(t, d, "GET", "/reports/ledger/account"); got.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing id code=%d", got.Code)
	}
	if got := serveReporting(t, d, "GET", "/reports/ledger/account?account_id=999999"); got.Code != http.StatusNotFound {
		t.Fatalf("unknown id code=%d body=%s", got.Code, got.Body.String())
	}
}
