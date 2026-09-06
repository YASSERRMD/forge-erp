package procurement

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
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
	if _, err := st.SetStatus(ctx, ord.ID, Validated); err == nil {
		t.Fatal("unapproved large order validated")
	}
	approver := int64(1)
	if _, err := st.SetApproval(ctx, ord.ID, approver); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetStatus(ctx, ord.ID, Validated); err != nil {
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
	if _, err := st.SetStatus(ctx, rcvDoc.ID, Validated); err != nil {
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
	if _, err := st.SetStatus(ctx, sinvDoc.ID, Validated); err != nil {
		t.Fatal(err)
	}
	bal, _ := st.InvoiceBalance(ctx, sinvDoc.ID)
	if _, err := st.RecordPayment(ctx, &SupplierPayment{EntityID: 1, OrgID: 9, Amount: bal, Currency: "USD", Method: "transfer"}, []int64{sinvDoc.ID}, ym); err != nil {
		t.Fatal(err)
	}
	got, _ := st.DocByID(ctx, sinvDoc.ID)
	if got.Status != Paid {
		t.Fatalf("status = %d want paid", got.Status)
	}
}
