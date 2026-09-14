package pos

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

// TestPGCheckoutAtomic proves the Phase 0 task 4 guarantee: invoice,
// payment, stock movement and till sale commit together, and an
// over-stock checkout leaves no partial invoice behind.
func TestPGCheckoutAtomic(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	cst := catalog.NewPGStore(pool)
	sst := sales.NewPGStore(pool)
	pst := NewPGStore(pool)
	svc := NewService(pool, pst, cst, sst, 0, nil)

	p := &catalog.Product{EntityID: 1, SKU: "PG-CKOUT", Name: "Widget",
		Type: catalog.ProductGoods, Unit: "unit", NetPrice: 1000, VATRateBps: 2000,
		Status: catalog.ProductActive, StockTracked: true}
	if err := cst.CreateProduct(ctx, pool, p); err != nil {
		t.Fatal(err)
	}
	w := &catalog.Warehouse{EntityID: 1, Code: "PGCK", Label: "Main", Status: 1}
	if err := cst.CreateWarehouse(ctx, pool, w); err != nil {
		t.Fatal(err)
	}
	if _, err := cst.AppendMovement(ctx, pool, &catalog.StockMovement{EntityID: 1,
		ProductID: p.ID, WarehouseID: w.ID, Qty: 10, UnitCost: 300,
		Reason: catalog.ReasonReceipt, Ref: "OPEN"}, false); err != nil {
		t.Fatal(err)
	}
	term := &Terminal{EntityID: 1, Code: "PGT", Label: "Till", WarehouseID: w.ID, Status: TerminalActive}
	if err := pst.CreateTerminal(ctx, pool, term); err != nil {
		t.Fatal(err)
	}
	se := &Session{EntityID: 1, TerminalID: term.ID, Cashier: "ada"}
	if err := pst.OpenSession(ctx, pool, se); err != nil {
		t.Fatal(err)
	}

	// 2 x 1000 net + 20% VAT = 2400 gross; tender 3000 → change 600.
	rec, err := svc.Checkout(ctx, CheckoutCmd{EntityID: 1, SessionID: se.ID, OrgID: 7,
		Lines: []SaleLine{{ProductID: p.ID, Qty: 2}}, Method: PayCash, Tendered: 3000})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if rec.TotalGross != 2400 || rec.Change != 600 || rec.InvoiceID == 0 {
		t.Fatalf("sale=%+v want gross 2400 change 600 + invoice", rec)
	}
	invoices := func() int {
		list, err := sst.ListDocs(ctx, pool, 1, documents.TypeInvoice, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	if n := invoices(); n != 1 {
		t.Fatalf("invoices = %d want 1", n)
	}
	lvl, err := cst.Level(ctx, pool, p.ID, w.ID)
	if err != nil || lvl.Qty != 8 {
		t.Fatalf("stock=%+v err=%v want qty 8", lvl, err)
	}

	// Over-stock checkout must fail with zero side effects: no new invoice,
	// stock untouched.
	if _, err := svc.Checkout(ctx, CheckoutCmd{EntityID: 1, SessionID: se.ID, OrgID: 7,
		Lines: []SaleLine{{ProductID: p.ID, Qty: 999}}, Method: PayCash, Tendered: 99999999}); err == nil {
		t.Fatal("over-stock checkout succeeded")
	}
	if n := invoices(); n != 1 {
		t.Fatalf("invoices after rollback = %d want 1", n)
	}
	lvl, err = cst.Level(ctx, pool, p.ID, w.ID)
	if err != nil || lvl.Qty != 8 {
		t.Fatalf("stock after rollback=%+v err=%v want qty 8", lvl, err)
	}
}
