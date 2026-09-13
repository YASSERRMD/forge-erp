package procurement

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestMemoryStoreProcureChain(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	ym := "202609"

	// Contract price pins the line.
	if err := st.UpsertPrice(ctx, &SupplierPrice{EntityID: 1, ProductID: 2, OrgID: 9, UnitNet: 6000, Currency: "USD"}); err != nil {
		t.Fatal(err)
	}
	prices, _ := st.PricesFor(ctx, 1, 2, 9)
	line := documents.Line{ProductID: 2, Label: "Steel", Qty: 10, UnitNet: 6000, VATRateBps: 2000}
	if err := CheckContract(line, 9, prices); err != nil {
		t.Fatal(err)
	}

	ord := &Document{EntityID: 1, Type: documents.TypeSupplierOrder, OrgID: 9,
		Currency: "USD", RateToBase: 1000000, Lines: []documents.Line{line}}
	if err := st.CreateDoc(ctx, ord, ym); err != nil {
		t.Fatal(err)
	}
	if ord.Ref != "SORD-202609-0001" {
		t.Fatalf("ref = %q", ord.Ref)
	}
	// Above threshold without approver → validation gate on SetStatus.
	if _, err := st.SetStatus(ctx, ord.EntityID, ord.ID, Validated); err == nil {
		t.Fatal("unapproved large order validated")
	}
	approver := int64(1)
	if _, err := st.SetApproval(ctx, ord.EntityID, ord.ID, approver); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetStatus(ctx, ord.EntityID, ord.ID, Validated); err != nil {
		t.Fatal(err)
	}

	// Order → reception → validate; receive posts tested at handler level.
	rcv, err := Convert(*ord, documents.TypeReception)
	if err != nil {
		t.Fatal(err)
	}
	rcvDoc := &rcv
	if err := st.CreateDoc(ctx, rcvDoc, ym); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetStatus(ctx, rcvDoc.EntityID, rcvDoc.ID, Validated); err != nil {
		t.Fatal(err)
	}
	// Reception → supplier invoice → pay in full.
	sinv, err := Convert(rcv, documents.TypeSupplierInvoice)
	if err != nil {
		t.Fatal(err)
	}
	sinvDoc := &sinv
	if err := st.CreateDoc(ctx, sinvDoc, ym); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetStatus(ctx, sinvDoc.EntityID, sinvDoc.ID, Validated); err != nil {
		t.Fatal(err)
	}
	bal, _ := st.InvoiceBalance(ctx, sinvDoc.EntityID, sinvDoc.ID)
	if _, err := st.RecordPayment(ctx, &SupplierPayment{EntityID: 1, OrgID: 9, Amount: bal, Currency: "USD", Method: "transfer"}, []int64{sinvDoc.ID}, ym); err != nil {
		t.Fatal(err)
	}
	got, _ := st.DocByID(ctx, sinvDoc.EntityID, sinvDoc.ID)
	if got.Status != Paid {
		t.Fatalf("status = %d want paid", got.Status)
	}
}

func TestPGStoreProcureChain(t *testing.T) {
	ctx := context.Background()
	st := NewPGStore(pgtest.Pool(t))
	ym := "202609"
	po := &Document{EntityID: 1, Type: documents.TypeSupplierOrder, OrgID: 1,
		Currency: "USD", RateToBase: 1000000,
		Lines: []documents.Line{{ProductID: 1, Label: "W", Qty: 5, UnitNet: 300}}}
	if err := st.CreateDoc(ctx, po, ym); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.SetApproval(ctx, po.EntityID, po.ID, 1); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := st.SetStatus(ctx, po.EntityID, po.ID, Validated); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got, err := st.DocByID(ctx, po.EntityID, po.ID)
	if err != nil || got.Ref == "" {
		t.Fatalf("by id: %+v %v", got, err)
	}
}

func TestMemoryCrossTenantDoc(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	ym := "202609"
	ord := &Document{EntityID: 1, Type: documents.TypeSupplierOrder, OrgID: 9,
		Currency: "USD", RateToBase: 1000000,
		Lines: []documents.Line{{ProductID: 2, Label: "Steel", Qty: 1, UnitNet: 100}}}
	if err := st.CreateDoc(ctx, ord, ym); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DocByID(ctx, 2, ord.ID); err == nil {
		t.Error("cross-tenant DocByID succeeded")
	}
	if _, err := st.SetStatus(ctx, 2, ord.ID, Validated); err == nil {
		t.Error("cross-tenant SetStatus succeeded")
	}
	if _, err := st.SetApproval(ctx, 2, ord.ID, 1); err == nil {
		t.Error("cross-tenant SetApproval succeeded")
	}
	inv := &Document{EntityID: 1, Type: documents.TypeSupplierInvoice, OrgID: 9,
		Currency: "USD", RateToBase: 1000000,
		Lines: []documents.Line{{ProductID: 2, Label: "Steel", Qty: 1, UnitNet: 100}}}
	if err := st.CreateDoc(ctx, inv, ym); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InvoiceBalance(ctx, 2, inv.ID); err == nil {
		t.Error("cross-tenant InvoiceBalance succeeded")
	}
	if _, err := st.RecordPayment(ctx, &SupplierPayment{EntityID: 2, OrgID: 9, Amount: inv.Totals.Gross, Currency: "USD", Method: "transfer"}, []int64{inv.ID}, ym); err == nil {
		t.Error("cross-tenant RecordPayment succeeded")
	}
}
