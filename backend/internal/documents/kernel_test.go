package documents

import (
	"testing"
)

func TestNextRef(t *testing.T) {
	if got := NextRef(TypeProposal, "202609", 7); got != "PROP-202609-0007" {
		t.Fatalf("ref = %q", got)
	}
	if got := NextRef(TypeInvoice, "202612", 12345); got != "INV-202612-12345" {
		t.Fatalf("ref overflow = %q", got)
	}
}

func TestCanTransition(t *testing.T) {
	if !CanTransition(TypeProposal, 0, 1) || !CanTransition(TypeInvoice, 1, 3) {
		t.Fatal("legal moves rejected")
	}
	// No editing history backwards, no skipping validation.
	if CanTransition(TypeInvoice, 0, 3) || CanTransition(TypeOrder, 2, 1) {
		t.Fatal("illegal moves accepted")
	}
	// Terminal states have no exits.
	if CanTransition(TypeProposal, 4, 0) || CanTransition(TypeShipment, 9, 0) {
		t.Fatal("terminal exit accepted")
	}
}

func TestLineTotals(t *testing.T) {
	// 2 × 199 net, 10% discount, 20% VAT:
	// net = (398*90+50)/100 = 358; vat = (358*2000+5000)/10000 = 72; gross 430.
	l := Line{Qty: 2, UnitNet: 199, VATRateBps: 2000, DiscountPc: 10}
	if n := l.Net(); n != 358 {
		t.Fatalf("net = %d want 358", n)
	}
	if v := l.VAT(); v != 72 {
		t.Fatalf("vat = %d want 72", v)
	}
	tot, err := Sum([]Line{l, {Qty: 1, UnitNet: 100, VATRateBps: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if tot.Net != 458 || tot.VAT != 72 || tot.Gross != 530 {
		t.Fatalf("totals = %+v", tot)
	}
	if _, err := Sum([]Line{{Qty: 0, UnitNet: 5}}); err == nil {
		t.Fatal("zero-qty line accepted")
	}
}
