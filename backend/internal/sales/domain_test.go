package sales

import (
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
)

func sampleDoc() Document {
	return Document{EntityID: 1, Type: documents.TypeProposal, Status: ProposalDraft,
		OrgID: 3, Currency: "USD", RateToBase: 1000000,
		Lines: []documents.Line{{ProductID: 1, Label: "Widget", Qty: 2, UnitNet: 500, VATRateBps: 2000}}}
}

func TestDocumentValidate(t *testing.T) {
	if err := sampleDoc().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := sampleDoc()
	bad.Lines = nil
	if err := bad.Validate(); err == nil {
		t.Error("lineless doc accepted")
	}
	bad = sampleDoc()
	bad.Type = "teleport"
	if err := bad.Validate(); err == nil {
		t.Error("unknown type accepted")
	}
}

func TestMoveTo(t *testing.T) {
	d := sampleDoc()
	if err := d.MoveTo(ProposalValidated); err != nil {
		t.Fatal(err)
	}
	if err := d.MoveTo(ProposalBilled); err == nil {
		t.Error("skip transition accepted")
	}
}

func TestConvertChain(t *testing.T) {
	prop := sampleDoc()
	prop.ID = 11
	ord, err := Convert(prop, documents.TypeOrder)
	if err != nil {
		t.Fatal(err)
	}
	if ord.SourceID != 11 || ord.SourceType != documents.TypeProposal || ord.Status != 0 {
		t.Fatalf("lineage: %+v", ord)
	}
	if ord.Totals.Gross != 1200 { // 2×500=1000 net +200 VAT
		t.Fatalf("totals: %+v", ord.Totals)
	}
	if _, err := Convert(prop, documents.TypeInvoice); err == nil {
		t.Error("proposal→invoice skip accepted")
	}
	inv, err := Convert(Document{EntityID: 1, Type: documents.TypeOrder, ID: 5,
		OrgID: 1, Currency: "USD", Lines: prop.Lines}, documents.TypeInvoice)
	if err != nil || inv.SourceID != 5 {
		t.Fatalf("order→invoice: %+v %v", inv, err)
	}
}

func TestAllocate(t *testing.T) {
	a, left, err := Allocate(1000, 400)
	if err != nil || a != 400 || left != 0 {
		t.Fatalf("partial: %d %d %v", a, left, err)
	}
	if _, _, err := Allocate(1000, 1001); err == nil {
		t.Fatal("overpayment accepted")
	}
	applied, rest, err := AllocateAcross([]int64{500, 700, 300}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if applied[0] != 500 || applied[1] != 500 || applied[2] != 0 || rest != 0 {
		t.Fatalf("across: %v rest=%d", applied, rest)
	}
	applied, rest, _ = AllocateAcross([]int64{500}, 900)
	if applied[0] != 500 || rest != 400 {
		t.Fatalf("remainder: %v rest=%d", applied, rest)
	}
}
