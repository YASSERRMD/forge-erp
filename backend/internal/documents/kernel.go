// Package documents is the shared kernel for commercial documents (Dolibarr
// CommonObject/CommonInvoice/CommonOrder equivalents): reference numbering,
// status-transition validation, and line-level VAT totals with half-up rounding.
package documents

import (
	"errors"
	"fmt"
)

// DocType identifies a commercial document family.
type DocType string

const (
	TypeProposal DocType = "proposal" // Dolibarr llx_propal
	TypeOrder    DocType = "order"    // llx_commande
	TypeShipment DocType = "shipment" // llx_expedition
	TypeInvoice  DocType = "invoice"  // llx_facture
	// TypeCreditNote reverses invoiced amounts (Dolibarr avoir).
	TypeCreditNote DocType = "credit_note"
	// Supplier families (Dolibarr supplier_proposal / commande_fournisseur /
	// reception / facture_fourn) share kernel semantics with own lifecycles.
	TypeSupplierProposal DocType = "supplier_proposal"
	TypeSupplierOrder    DocType = "supplier_order"
	TypeReception        DocType = "reception"
	TypeSupplierInvoice  DocType = "supplier_invoice"
	// TypeTransfer is the inter-warehouse transfer document (Phase 5 PORT;
	// no Dolibarr single-table equivalent — paired llx_stock_mouvement rows).
	TypeTransfer DocType = "transfer"
)

// Prefix returns the reference prefix per type (Dolibarr numbering masks equivalent).
func (t DocType) Prefix() string {
	switch t {
	case TypeProposal:
		return "PROP"
	case TypeOrder:
		return "ORD"
	case TypeShipment:
		return "SHIP"
	case TypeInvoice:
		return "INV"
	case TypeCreditNote:
		return "CN"
	case TypeSupplierProposal:
		return "SPROP"
	case TypeSupplierOrder:
		return "SORD"
	case TypeReception:
		return "RCV"
	case TypeSupplierInvoice:
		return "SINV"
	case TypeTransfer:
		return "TRF"
	}
	return "DOC"
}

// NextRef builds PREFIX-YYYYMM-#### (seq zero-padded to 4; grows beyond 9999 naturally).
func NextRef(t DocType, yearMonth string, seq int64) string {
	return fmt.Sprintf("%s-%s-%04d", t.Prefix(), yearMonth, seq)
}

// Transitions whitelists legal status moves per type. Draft-only editing is enforced
// by handlers (validated documents reject PUT); cancellations are status flips.
// Proposal: 0 draft,1 validated,2 signed,3 billed,4 closed,9 cancelled.
// Order: 0 draft,1 validated,2 shipped,3 billed,4 closed,9 cancelled.
// Shipment: 0 draft,1 validated,2 closed,9 cancelled.
// Invoice: 0 draft,1 validated,2 part_paid,3 paid,9 cancelled.
// CreditNote: 0 draft,1 validated (applied),9 cancelled.
var Transitions = map[DocType]map[int16][]int16{
	TypeProposal: {
		0: {1, 9},
		1: {2, 9},
		2: {3, 9},
		3: {4},
	},
	TypeOrder: {
		0: {1, 9},
		1: {2, 9},
		2: {3, 9},
		3: {4},
	},
	TypeShipment: {
		0: {1, 9},
		1: {2, 9},
	},
	TypeInvoice: {
		0: {1, 9},
		1: {2, 3, 9},
		2: {3, 9},
	},
	TypeCreditNote: {
		0: {1, 9},
	},
	// Supplier lifecycles mirror the sales ones.
	TypeSupplierProposal: {
		0: {1, 9},
		1: {2, 9},
		2: {3, 9},
		3: {4},
	},
	TypeSupplierOrder: {
		0: {1, 9},
		1: {2, 9},
		2: {3, 9},
		3: {4},
	},
	TypeReception: {
		0: {1, 9},
		1: {2, 9},
	},
	TypeSupplierInvoice: {
		0: {1, 9},
		1: {2, 3, 9},
		2: {3, 9},
	},
	// Transfer: 0 draft,1 validated,9 cancelled. Validated is terminal:
	// stock already moved, so cancellation must be a reverse transfer.
	TypeTransfer: {
		0: {1, 9},
	},
}

// CanTransition reports whether a status move is legal.
func CanTransition(t DocType, from, to int16) bool {
	for _, allowed := range Transitions[t][from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// LineKind discriminates priced lines from Dolibarr-style section and
// subtotal rows (llx_*_det.special_code equivalent). The zero value ""
// decodes as normal so every pre-subtotal document keeps its totals.
type LineKind string

const (
	LineNormal   LineKind = "normal"
	LineSection  LineKind = "section"  // title row: label only, amounts ignored
	LineSubtotal LineKind = "subtotal" // running-total marker, amounts ignored
)

// Line is one document line (Dolibarr *_det rows). Amounts in minor units.
type Line struct {
	ProductID  int64    `json:"product_id"`
	Label      string   `json:"label"`
	Qty        int64    `json:"qty"`
	UnitNet    int64    `json:"unit_net"`
	VATRateBps int      `json:"vat_rate_bps"`
	DiscountPc int      `json:"discount_pc"` // 0–100
	Kind       LineKind `json:"kind,omitempty"`
}

// KindOf normalizes the kind ("" means normal).
func (l Line) KindOf() LineKind {
	if l.Kind == "" {
		return LineNormal
	}
	return l.Kind
}

// Validate line rules. Section lines need a label only (quantities and
// prices are display-ignored); subtotal lines need nothing; normal lines
// keep the legacy priced-line rules.
func (l Line) Validate() error {
	switch l.KindOf() {
	case LineSection:
		if l.Label == "" {
			return errors.New("documents: section line requires a label")
		}
		return nil
	case LineSubtotal:
		return nil
	case LineNormal:
		// "" and "normal" both land here via KindOf.
	default:
		return fmt.Errorf("documents: bad line kind %q", l.Kind)
	}
	if l.Qty <= 0 {
		return errors.New("documents: line quantity must be positive")
	}
	if l.UnitNet < 0 {
		return errors.New("documents: negative unit price")
	}
	if l.VATRateBps < 0 || l.VATRateBps > 10000 {
		return fmt.Errorf("documents: bad VAT rate %d", l.VATRateBps)
	}
	if l.DiscountPc < 0 || l.DiscountPc > 100 {
		return fmt.Errorf("documents: bad discount %d", l.DiscountPc)
	}
	return nil
}

// Net returns the line net after discount (half-up). Section and subtotal
// marker lines always net zero.
func (l Line) Net() int64 {
	if l.KindOf() != LineNormal {
		return 0
	}
	gross := l.Qty * l.UnitNet
	return (gross*(100-int64(l.DiscountPc)) + 50) / 100
}

// VAT returns line VAT on the discounted net (half-up; Dolibarr per-line rounding).
// Section and subtotal marker lines carry no VAT.
func (l Line) VAT() int64 {
	if l.KindOf() != LineNormal {
		return 0
	}
	return (l.Net()*int64(l.VATRateBps) + 5000) / 10000
}

// Totals aggregates net/VAT/gross across lines (Dolibarr total_ht/total_tva/total_ttc).
type Totals struct {
	Net   int64 `json:"net"`
	VAT   int64 `json:"vat"`
	Gross int64 `json:"gross"`
}

// Sum totals lines; validation errors fail the whole document.
// Section and subtotal marker lines validate as markers and contribute
// zero — document totals always equal the priced (normal) lines.
func Sum(lines []Line) (Totals, error) {
	var t Totals
	for _, l := range lines {
		if err := l.Validate(); err != nil {
			return Totals{}, err
		}
		t.Net += l.Net()
		t.VAT += l.VAT()
	}
	t.Gross = t.Net + t.VAT
	return t, nil
}

// SubtotalBlocks walks lines and returns one Totals per subtotal marker:
// the net/VAT/gross of the normal lines since the previous subtotal marker
// (or the start of the document). Section markers neither contribute nor
// reset the running block. Validation errors fail the whole document.
func SubtotalBlocks(lines []Line) ([]Totals, error) {
	var out []Totals
	var cur Totals
	for _, l := range lines {
		if err := l.Validate(); err != nil {
			return nil, err
		}
		if l.KindOf() == LineSubtotal {
			cur.Gross = cur.Net + cur.VAT
			out = append(out, cur)
			cur = Totals{}
			continue
		}
		cur.Net += l.Net()
		cur.VAT += l.VAT()
	}
	return out, nil
}
