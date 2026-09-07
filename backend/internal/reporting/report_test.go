package reporting

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

func TestBuildPNL(t *testing.T) {
	ctx := context.Background()
	ledger := finance.NewMemoryStore()
	for _, a := range []finance.Account{
		{EntityID: 1, Code: "512000", Label: "Bank", Type: "asset"},
		{EntityID: 1, Code: "707000", Label: "Sales", Type: "revenue"},
		{EntityID: 1, Code: "607000", Label: "Purchases", Type: "expense"},
	} {
		a := a
		if err := ledger.CreateAccount(ctx, &a); err != nil {
			t.Fatalf("account: %v", err)
		}
	}
	j := &finance.Journal{EntityID: 1, Code: "VEN", Label: "Sales"}
	if err := ledger.CreateJournal(ctx, j); err != nil {
		t.Fatalf("journal: %v", err)
	}
	if err := ledger.CreateFiscalYear(ctx, &finance.FiscalYear{EntityID: 1, Label: "FY"}); err != nil {
		t.Fatalf("fy: %v", err)
	}
	accts, _ := ledger.Accounts(ctx, 1)
	byCode := map[string]int64{}
	for _, a := range accts {
		byCode[a.Code] = a.ID
	}
	if len(byCode) != 3 {
		t.Fatalf("accounts=%d want 3", len(byCode))
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, e := range []*finance.Entry{
		{EntityID: 1, JournalID: j.ID, Ref: "E1", Date: now, Lines: []finance.EntryLine{
			{AccountID: byCode["512000"], Debit: 10000},
			{AccountID: byCode["707000"], Credit: 10000},
		}},
		{EntityID: 1, JournalID: j.ID, Ref: "E2", Date: now, Lines: []finance.EntryLine{
			{AccountID: byCode["607000"], Debit: 4000},
			{AccountID: byCode["512000"], Credit: 4000},
		}},
	} {
		if err := ledger.PostEntry(ctx, e); err != nil {
			t.Fatalf("post: %v", err)
		}
	}
	pnl, err := BuildPNL(ctx, 1, ledger)
	if err != nil {
		t.Fatalf("pnl: %v", err)
	}
	if pnl.Revenue != 10000 || pnl.Expense != 4000 || pnl.Net != 6000 {
		t.Fatalf("pnl=%+v want revenue 10000 expense 4000 net 6000", pnl)
	}
}

func TestReceivablesAndValuation(t *testing.T) {
	ctx := context.Background()
	billing := sales.NewMemoryStore()
	d := &sales.Document{EntityID: 1, Type: documents.TypeInvoice, OrgID: 7,
		Currency: "USD", RateToBase: 1000000,
		Lines: []documents.Line{{ProductID: 1, Label: "x", Qty: 1, UnitNet: 1200, VATRateBps: 2000}}}
	if err := billing.CreateDoc(ctx, d, "202609"); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	if _, err := billing.SetStatus(ctx, d.ID, sales.InvoiceValidated); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got, total, err := Receivables(ctx, 1, billing)
	if err != nil {
		t.Fatalf("receivables: %v", err)
	}
	if len(got) != 1 || total != got[0].Balance || total <= 0 {
		t.Fatalf("receivables=%+v total=%d", got, total)
	}

	stock := catalog.NewMemoryStore()
	p := &catalog.Product{EntityID: 1, SKU: "W-1", Name: "W", Type: catalog.ProductGoods,
		Status: catalog.ProductActive, StockTracked: true}
	if err := stock.CreateProduct(ctx, p); err != nil {
		t.Fatalf("product: %v", err)
	}
	mv := &catalog.StockMovement{EntityID: 1, ProductID: p.ID, WarehouseID: 1,
		Qty: 5, UnitCost: 200, Reason: catalog.ReasonReceipt, Ref: "OPEN"}
	if _, err := stock.AppendMovement(ctx, mv, false); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	lines, vtotal, err := StockValuation(ctx, 1, 1, stock)
	if err != nil {
		t.Fatalf("valuation: %v", err)
	}
	if len(lines) != 1 || vtotal != 1000 || lines[0].PMP != 200 {
		t.Fatalf("valuation=%+v total=%d", lines, vtotal)
	}
}
