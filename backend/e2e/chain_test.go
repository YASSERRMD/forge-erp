// Package e2e scripts the end-to-end business chains against in-memory stores:
// quote-to-cash, procure-to-pay, and ledger posting with a balanced trial.
// It runs without external services (DB-gated coverage lives in CI with Postgres).
package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/partners"
	"github.com/YASSERRMD/forge-erp/backend/internal/procurement"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

func TestEndToEndChains(t *testing.T) {
	ctx := context.Background()
	ym, now := "202609", time.Now().UTC()
	pst, cst := partners.NewMemoryStore(), catalog.NewMemoryStore()
	sst, procst, fst := sales.NewMemoryStore(), procurement.NewMemoryStore(), finance.NewMemoryStore()

	// Master data.
	cust := &partners.Organization{EntityID: 1, Name: "Acme", IsCustomer: true, CustomerCode: "ACME"}
	supp := &partners.Organization{EntityID: 1, Name: "Globex", IsSupplier: true, SupplierCode: "GLOB"}
	if err := pst.CreateOrg(ctx, cust); err != nil {
		t.Fatal(err)
	}
	if err := pst.CreateOrg(ctx, supp); err != nil {
		t.Fatal(err)
	}
	prod := &catalog.Product{EntityID: 1, SKU: "W-1", Name: "Widget", NetPrice: 500,
		VATRateBps: 2000, Status: catalog.ProductActive, StockTracked: true}
	if err := cst.CreateProduct(ctx, prod); err != nil {
		t.Fatal(err)
	}
	wh := &catalog.Warehouse{EntityID: 1, Code: "MAIN", Label: "Main", Status: 1}
	if err := cst.CreateWarehouse(ctx, wh); err != nil {
		t.Fatal(err)
	}
	if _, err := cst.AppendMovement(ctx, &catalog.StockMovement{EntityID: 1,
		ProductID: prod.ID, WarehouseID: wh.ID, Qty: 100, UnitCost: 300,
		Reason: catalog.ReasonReceipt, Ref: "OPENING"}, false); err != nil {
		t.Fatal(err)
	}

	// Quote-to-cash.
	lines := []documents.Line{{ProductID: prod.ID, Label: "Widget", Qty: 2, UnitNet: 500, VATRateBps: 2000}}
	prop := &sales.Document{EntityID: 1, Type: documents.TypeProposal, OrgID: cust.ID,
		Currency: "USD", RateToBase: 1000000, Lines: lines}
	mustCreateSales(t, ctx, sst, prop, ym)
	mustStatusSales(t, ctx, sst, prop.ID, sales.ProposalValidated)
	mustStatusSales(t, ctx, sst, prop.ID, sales.ProposalSigned)
	ord := mustConvertSales(t, ctx, sst, *prop, documents.TypeOrder, ym)
	mustStatusSales(t, ctx, sst, ord.ID, sales.OrderValidated)
	shp := mustConvertSales(t, ctx, sst, *ord, documents.TypeShipment, ym)
	mustStatusSales(t, ctx, sst, shp.ID, sales.ShipmentValidated)
	if _, err := cst.AppendMovement(ctx, &catalog.StockMovement{EntityID: 1,
		ProductID: prod.ID, WarehouseID: wh.ID, Qty: -2,
		Reason: catalog.ReasonShipment, Ref: shp.Ref}, false); err != nil {
		t.Fatal(err)
	}
	mustStatusSales(t, ctx, sst, shp.ID, sales.ShipmentClosed)
	inv := mustConvertSales(t, ctx, sst, *shp, documents.TypeInvoice, ym)
	mustStatusSales(t, ctx, sst, inv.ID, sales.InvoiceValidated)
	if _, err := sst.RecordPayment(ctx, &sales.Payment{EntityID: 1, OrgID: cust.ID,
		Amount: inv.Totals.Gross, Currency: "USD", Method: "transfer", PaidAt: now},
		[]int64{inv.ID}, ym); err != nil {
		t.Fatal(err)
	}
	got, _ := sst.DocByID(ctx, inv.ID)
	if got.Status != sales.InvoicePaid {
		t.Fatalf("sales invoice status = %d", got.Status)
	}

	// Procure-to-pay.
	if err := procst.UpsertPrice(ctx, &procurement.SupplierPrice{EntityID: 1,
		ProductID: prod.ID, OrgID: supp.ID, UnitNet: 300, Currency: "USD"}); err != nil {
		t.Fatal(err)
	}
	po := &procurement.Document{EntityID: 1, Type: documents.TypeSupplierOrder, OrgID: supp.ID,
		Currency: "USD", RateToBase: 1000000,
		Lines: []documents.Line{{ProductID: prod.ID, Label: "Widget", Qty: 10, UnitNet: 300}}}
	if err := procst.CreateDoc(ctx, po, ym); err != nil {
		t.Fatal(err)
	}
	approver := int64(1)
	if _, err := procst.SetApproval(ctx, po.ID, approver); err != nil {
		t.Fatal(err)
	}
	if _, err := procst.SetStatus(ctx, po.ID, procurement.Validated); err != nil {
		t.Fatal(err)
	}
	rcvDoc, err := procurement.Convert(*po, documents.TypeReception)
	if err != nil {
		t.Fatal(err)
	}
	rcv := &rcvDoc
	if err := procst.CreateDoc(ctx, rcv, ym); err != nil {
		t.Fatal(err)
	}
	if _, err := procst.SetStatus(ctx, rcv.ID, procurement.Validated); err != nil {
		t.Fatal(err)
	}
	if _, err := cst.AppendMovement(ctx, &catalog.StockMovement{EntityID: 1,
		ProductID: prod.ID, WarehouseID: wh.ID, Qty: 10, UnitCost: 300,
		Reason: catalog.ReasonReceipt, Ref: rcv.Ref}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := procst.SetStatus(ctx, rcv.ID, procurement.Stage2); err != nil {
		t.Fatal(err)
	}
	sinvDoc, err := procurement.Convert(*rcv, documents.TypeSupplierInvoice)
	if err != nil {
		t.Fatal(err)
	}
	sinv := &sinvDoc
	if err := procst.CreateDoc(ctx, sinv, ym); err != nil {
		t.Fatal(err)
	}
	if _, err := procst.SetStatus(ctx, sinv.ID, procurement.Validated); err != nil {
		t.Fatal(err)
	}
	bal, _ := procst.InvoiceBalance(ctx, sinv.ID)
	if _, err := procst.RecordPayment(ctx, &procurement.SupplierPayment{EntityID: 1,
		OrgID: supp.ID, Amount: bal, Currency: "USD", Method: "transfer"},
		[]int64{sinv.ID}, ym); err != nil {
		t.Fatal(err)
	}

	// Stock integrity: 100 - 2 + 10 = 108.
	lvl, _ := cst.Level(ctx, prod.ID, wh.ID)
	if lvl.Qty != 108 {
		t.Fatalf("stock = %+v want qty 108", lvl)
	}

	// Ledger: post both invoices, trial must balance.
	custA := &finance.Account{EntityID: 1, Code: "411", Label: "C", Type: "asset"}
	revA := &finance.Account{EntityID: 1, Code: "707", Label: "R", Type: "revenue"}
	vatA := &finance.Account{EntityID: 1, Code: "4457", Label: "V", Type: "liability"}
	for _, a := range []*finance.Account{custA, revA, vatA} {
		if err := fst.CreateAccount(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	j := &finance.Journal{EntityID: 1, Code: "VEN", Label: "V"}
	if err := fst.CreateJournal(ctx, j); err != nil {
		t.Fatal(err)
	}
	if _, err := finance.PostInvoice(ctx, fst, 1, j.ID, inv.Ref, now,
		custA.ID, revA.ID, vatA.ID, inv.Totals.Net, inv.Totals.VAT, "auto", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := finance.PostInvoice(ctx, fst, 1, j.ID, sinv.Ref, now,
		custA.ID, revA.ID, vatA.ID, sinv.Totals.Net, sinv.Totals.VAT, "auto", nil); err != nil {
		t.Fatal(err)
	}
	tb, _ := fst.TrialBalance(ctx, 1)
	var dr, cr int64
	for _, s := range tb {
		dr += s[0]
		cr += s[1]
	}
	if dr != cr || dr == 0 {
		t.Fatalf("trial unbalanced: dr=%d cr=%d", dr, cr)
	}
	ents, _ := fst.EntriesByJournal(ctx, j.ID)
	if err := finance.VerifyChain(ents); err != nil {
		t.Fatal(err)
	}
}

func mustCreateSales(t *testing.T, ctx context.Context, s *sales.MemoryStore, d *sales.Document, ym string) {
	t.Helper()
	if err := s.CreateDoc(ctx, d, ym); err != nil {
		t.Fatal(err)
	}
}

func mustStatusSales(t *testing.T, ctx context.Context, s *sales.MemoryStore, id int64, to int16) {
	t.Helper()
	if _, err := s.SetStatus(ctx, id, to); err != nil {
		t.Fatal(err)
	}
}

func mustConvertSales(t *testing.T, ctx context.Context, s *sales.MemoryStore, src sales.Document, to documents.DocType, ym string) *sales.Document {
	t.Helper()
	next, err := sales.Convert(src, to)
	if err != nil {
		t.Fatal(err)
	}
	out := &next
	if err := s.CreateDoc(ctx, out, ym); err != nil {
		t.Fatal(err)
	}
	return out
}
