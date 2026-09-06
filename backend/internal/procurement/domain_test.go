package procurement

import (
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
)

func sampleOrder() Document {
	d := Document{EntityID: 1, Type: documents.TypeSupplierOrder, OrgID: 9,
		Currency: "USD", RateToBase: 1000000,
		Lines: []documents.Line{{ProductID: 1, Label: "Steel", Qty: 10, UnitNet: 6000, VATRateBps: 2000}}}
	tot, _ := documents.Sum(d.Lines)
	d.Totals = tot // gross 72000 > threshold
	return d
}

func TestValidateAndApproval(t *testing.T) {
	d := sampleOrder()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if !d.RequiresApproval(DefaultApprovalThreshold) {
		t.Fatal("large order should require approval")
	}
	small := d
	small.Lines = []documents.Line{{ProductID: 1, Qty: 1, UnitNet: 100}}
	tot, _ := documents.Sum(small.Lines)
	small.Totals = tot
	if small.RequiresApproval(DefaultApprovalThreshold) {
		t.Fatal("small order should not require approval")
	}
}

func TestConvert(t *testing.T) {
	d := sampleOrder()
	d.ID = 3
	o, err := Convert(d, documents.TypeReception)
	if err != nil {
		t.Fatal(err)
	}
	if o.SourceID != 3 || o.SourceType != documents.TypeSupplierOrder {
		t.Fatalf("lineage: %+v", o)
	}
	if _, err := Convert(d, documents.TypeSupplierProposal); err == nil {
		t.Error("backward conversion accepted")
	}
}

func TestCheckContract(t *testing.T) {
	line := documents.Line{ProductID: 1, Qty: 2, UnitNet: 6000}
	prices := []SupplierPrice{{ProductID: 1, OrgID: 9, UnitNet: 6000}}
	if err := CheckContract(line, 9, prices); err != nil {
		t.Fatal(err)
	}
	off := line
	off.UnitNet = 6100
	if err := CheckContract(off, 9, prices); err == nil {
		t.Error("off-contract price accepted")
	}
	// No contract → free pricing.
	if err := CheckContract(line, 10, prices); err != nil {
		t.Fatal(err)
	}
}
