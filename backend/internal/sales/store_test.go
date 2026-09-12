package sales

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestMemoryStoreChain(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	ym := "202609"

	prop := &Document{EntityID: 1, Type: documents.TypeProposal, OrgID: 7,
		Currency: "USD", RateToBase: 1000000,
		Lines: []documents.Line{{ProductID: 1, Label: "Widget", Qty: 2, UnitNet: 500, VATRateBps: 2000}}}
	if err := st.CreateDoc(ctx, prop, ym); err != nil {
		t.Fatal(err)
	}
	if prop.Ref != "PROP-202609-0001" {
		t.Fatalf("ref = %q", prop.Ref)
	}
	if _, err := st.SetStatus(ctx, prop.ID, ProposalValidated); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetStatus(ctx, prop.ID, ProposalBilled); err == nil {
		t.Fatal("skip transition accepted")
	}

	// Convert down the chain to an invoice.
	ord, err := Convert(*prop, documents.TypeOrder)
	if err != nil {
		t.Fatal(err)
	}
	ordDoc := &ord
	if err := st.CreateDoc(ctx, ordDoc, ym); err != nil {
		t.Fatal(err)
	}
	inv, err := Convert(*ordDoc, documents.TypeInvoice)
	if err != nil {
		t.Fatal(err)
	}
	invDoc := &inv
	if err := st.CreateDoc(ctx, invDoc, ym); err != nil {
		t.Fatal(err)
	}
	if invDoc.SourceType != documents.TypeOrder || invDoc.SourceID != ordDoc.ID {
		t.Fatalf("lineage: %+v", invDoc)
	}

	// Partial then full payment flips invoice status.
	pay := &Payment{EntityID: 1, OrgID: 7, Amount: 400, Currency: "USD", Method: "transfer", PaidAt: time.Now().UTC()}
	if _, err := st.RecordPayment(ctx, pay, []int64{invDoc.ID}, ym); err != nil {
		t.Fatal(err)
	}
	got, _ := st.DocByID(ctx, invDoc.ID)
	if got.Status != InvoicePartPaid {
		t.Fatalf("status = %d want part-paid", got.Status)
	}
	bal, _ := st.InvoiceBalance(ctx, invDoc.ID)
	if bal != 800 {
		t.Fatalf("balance = %d want 800", bal)
	}
	pay2 := &Payment{EntityID: 1, OrgID: 7, Amount: 800, Currency: "USD", Method: "transfer", PaidAt: time.Now().UTC()}
	if _, err := st.RecordPayment(ctx, pay2, []int64{invDoc.ID}, ym); err != nil {
		t.Fatal(err)
	}
	got, _ = st.DocByID(ctx, invDoc.ID)
	if got.Status != InvoicePaid {
		t.Fatalf("status = %d want paid", got.Status)
	}
	// Settled invoice rejects further payment.
	pay3 := &Payment{EntityID: 1, OrgID: 7, Amount: 10, Currency: "USD", PaidAt: time.Now().UTC()}
	if _, err := st.RecordPayment(ctx, pay3, []int64{invDoc.ID}, ym); err == nil {
		t.Fatal("payment on settled invoice accepted")
	}
}

func TestPGStoreChain(t *testing.T) {
	ctx := context.Background()
	st := NewPGStore(pgtest.Pool(t))
	ym := "202609"
	d := &Document{EntityID: 1, Type: documents.TypeInvoice, OrgID: 1, Currency: "USD",
		RateToBase: 1000000,
		Lines: []documents.Line{{ProductID: 1, Label: "W", Qty: 1, UnitNet: 1000, VATRateBps: 2000}}}
	if err := st.CreateDoc(ctx, d, ym); err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.Ref == "" || d.Totals.Gross != 1200 {
		t.Fatalf("doc=%+v", d)
	}
	if _, err := st.SetStatus(ctx, d.ID, InvoiceValidated); err != nil {
		t.Fatalf("validate: %v", err)
	}
	pay := &Payment{EntityID: 1, OrgID: 1, Amount: 1200, Currency: "USD",
		Method: "transfer", PaidAt: time.Now().UTC()}
	if _, err := st.RecordPayment(ctx, pay, []int64{d.ID}, ym); err != nil {
		t.Fatalf("pay: %v", err)
	}
	bal, err := st.InvoiceBalance(ctx, d.ID)
	if err != nil || bal != 0 {
		t.Fatalf("balance=%d err=%v", bal, err)
	}
}
