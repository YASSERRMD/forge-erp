// Package sales implements quote-to-cash (Dolibarr propal → commande →
// expedition → facture → paiement): proposals, orders, shipments, invoices with
// shared kernel semantics, conversion lineage, and payment allocation.
package sales

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
)

// Statuses reuse the kernel code points; named aliases document each lifecycle.
const (
	ProposalDraft     int16 = 0
	ProposalValidated int16 = 1
	ProposalSigned    int16 = 2
	ProposalBilled    int16 = 3
	ProposalClosed    int16 = 4

	OrderDraft     int16 = 0
	OrderValidated int16 = 1
	OrderShipped   int16 = 2
	OrderBilled    int16 = 3
	OrderClosed    int16 = 4

	ShipmentDraft     int16 = 0
	ShipmentValidated int16 = 1
	ShipmentClosed    int16 = 2

	InvoiceDraft    int16 = 0
	InvoiceValidated int16 = 1
	InvoicePartPaid int16 = 2
	InvoicePaid     int16 = 3

	StatusCancelled int16 = 9
)

// Document is a commercial document header (lines stored separately).
type Document struct {
	ID         int64             `json:"id"`
	EntityID   int64             `json:"entity_id"`
	Type       documents.DocType `json:"type"`
	Ref        string            `json:"ref"`
	Status     int16             `json:"status"`
	OrgID      int64             `json:"org_id"`
	Currency   string            `json:"currency"`    // ISO-4217 (multicurrency snapshot code)
	RateToBase int64             `json:"rate_to_base"` // ×1e6 vs base currency at doc date
	SourceType documents.DocType `json:"source_type"`
	SourceID   int64             `json:"source_id"`
	Lines      []documents.Line  `json:"lines"`
	Totals     documents.Totals  `json:"totals"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	CreatedBy  *int64            `json:"created_by"`
	UpdatedBy  *int64            `json:"updated_by"`
	RowVersion int64             `json:"row_version"`
}

// Validate header + lines (totals recomputed by the store/handler, never trusted).
func (d Document) Validate() error {
	switch d.Type {
	case documents.TypeProposal, documents.TypeOrder, documents.TypeShipment, documents.TypeInvoice, documents.TypeCreditNote:
	default:
		return fmt.Errorf("sales: unknown document type %q", d.Type)
	}
	if d.OrgID == 0 {
		return errors.New("sales: document requires a customer")
	}
	if len(d.Lines) == 0 {
		return errors.New("sales: document requires at least one line")
	}
	if _, err := documents.Sum(d.Lines); err != nil {
		return err
	}
	if strings.TrimSpace(d.Currency) == "" {
		return errors.New("sales: currency required")
	}
	return nil
}

// MoveTo validates a status transition through the kernel table.
func (d Document) MoveTo(to int16) error {
	if !documents.CanTransition(d.Type, d.Status, to) {
		return fmt.Errorf("sales: illegal transition %s %d -> %d", d.Type, d.Status, to)
	}
	return nil
}

// Convert clones a document into the next family with lineage
// (Dolibarr create-from actions: proposal→order→shipment→invoice).
func Convert(src Document, to documents.DocType) (Document, error) {
	next := map[[2]documents.DocType]bool{
		{documents.TypeProposal, documents.TypeOrder}:   true,
		{documents.TypeOrder, documents.TypeShipment}:   true,
		{documents.TypeOrder, documents.TypeInvoice}:    true,
		{documents.TypeShipment, documents.TypeInvoice}: true,
	}
	if !next[[2]documents.DocType{src.Type, to}] {
		return Document{}, fmt.Errorf("sales: cannot convert %s to %s", src.Type, to)
	}
	out := Document{
		EntityID: src.EntityID, Type: to, Status: 0, OrgID: src.OrgID,
		Currency: src.Currency, RateToBase: src.RateToBase,
		SourceType: src.Type, SourceID: src.ID, Lines: src.Lines,
	}
	tot, err := documents.Sum(out.Lines)
	if err != nil {
		return Document{}, err
	}
	out.Totals = tot
	return out, nil
}

// Allocate applies amount against an invoice balance; returns applied + leftover.
// Overpayment is refused (credit notes are Phase-06 reversals, not silent over-apply).
func Allocate(balance, amount int64) (applied, leftover int64, err error) {
	if amount <= 0 {
		return 0, 0, errors.New("sales: payment amount must be positive")
	}
	if amount > balance {
		return 0, 0, fmt.Errorf("sales: overpayment refused (balance %d, got %d)", balance, amount)
	}
	return amount, 0, nil
}

// AllocateAcross spreads one payment over several invoice balances in order,
// returning per-invoice applied amounts and any unapplied remainder.
func AllocateAcross(balances []int64, amount int64) (applied []int64, remainder int64, err error) {
	if amount <= 0 {
		return nil, 0, errors.New("sales: payment amount must be positive")
	}
	applied = make([]int64, len(balances))
	rest := amount
	for i, b := range balances {
		if rest == 0 {
			break
		}
		if rest >= b {
			applied[i] = b
			rest -= b
		} else {
			applied[i] = rest
			rest = 0
		}
	}
	return applied, rest, nil
}
