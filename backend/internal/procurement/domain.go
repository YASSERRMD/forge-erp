// Package procurement implements procure-to-pay (Dolibarr supplier_proposal →
// commande_fournisseur → reception → facture_fourn → paiementfourn): supplier
// documents reuse the documents kernel, receptions post stock receipts (PMP),
// and supplier price lists pin contract prices onto order lines.
package procurement

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
)

// Statuses mirror the sales lifecycles on supplier families.
const (
	Draft     int16 = 0
	Validated int16 = 1
	Stage2    int16 = 2 // signed / received / part-paid depending on family
	Billed    int16 = 3
	Closed    int16 = 4

	PartPaid  int16 = 2
	Paid      int16 = 3
	Cancelled int16 = 9
)

// DefaultApprovalThreshold above which orders need approval (minor units).
const DefaultApprovalThreshold = 50000

// Document is a supplier commercial document header.
type Document struct {
	ID         int64             `json:"id"`
	EntityID   int64             `json:"entity_id"`
	Type       documents.DocType `json:"type"`
	Ref        string            `json:"ref"`
	Status     int16             `json:"status"`
	OrgID      int64             `json:"org_id"` // supplier
	Currency   string            `json:"currency"`
	RateToBase int64             `json:"rate_to_base"`
	SourceType documents.DocType `json:"source_type"`
	SourceID   int64             `json:"source_id"`
	ApprovedBy *int64            `json:"approved_by"`
	Lines      []documents.Line  `json:"lines"`
	Totals     documents.Totals  `json:"totals"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	RowVersion int64             `json:"row_version"`
}

// Validate header + lines.
func (d Document) Validate() error {
	switch d.Type {
	case documents.TypeSupplierProposal, documents.TypeSupplierOrder,
		documents.TypeReception, documents.TypeSupplierInvoice:
	default:
		return fmt.Errorf("procurement: unknown document type %q", d.Type)
	}
	if d.OrgID == 0 {
		return errors.New("procurement: document requires a supplier")
	}
	if len(d.Lines) == 0 {
		return errors.New("procurement: document requires at least one line")
	}
	if _, err := documents.Sum(d.Lines); err != nil {
		return err
	}
	if strings.TrimSpace(d.Currency) == "" {
		return errors.New("procurement: currency required")
	}
	return nil
}

// MoveTo validates a status transition through the kernel table.
func (d Document) MoveTo(to int16) error {
	if !documents.CanTransition(d.Type, d.Status, to) {
		return fmt.Errorf("procurement: illegal transition %s %d -> %d", d.Type, d.Status, to)
	}
	return nil
}

// RequiresApproval reports whether validation needs an approver.
func (d Document) RequiresApproval(threshold int64) bool {
	return d.Type == documents.TypeSupplierOrder && d.Totals.Gross > threshold
}

// Convert clones a supplier document into the next family with lineage.
func Convert(src Document, to documents.DocType) (Document, error) {
	next := map[[2]documents.DocType]bool{
		{documents.TypeSupplierProposal, documents.TypeSupplierOrder}: true,
		{documents.TypeSupplierOrder, documents.TypeReception}:        true,
		{documents.TypeSupplierOrder, documents.TypeSupplierInvoice}:  true,
		{documents.TypeReception, documents.TypeSupplierInvoice}:      true,
	}
	if !next[[2]documents.DocType{src.Type, to}] {
		return Document{}, fmt.Errorf("procurement: cannot convert %s to %s", src.Type, to)
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

// SupplierPrice pins a contract price (Dolibarr llx_product_fournisseur_price).
type SupplierPrice struct {
	ID        int64  `json:"id"`
	EntityID  int64  `json:"entity_id"`
	ProductID int64  `json:"product_id"`
	OrgID     int64  `json:"org_id"` // supplier
	UnitNet   int64  `json:"unit_net"`
	Currency  string `json:"currency"`
}

// CheckContract enforces pinned prices: when a contract price exists for
// (product, supplier), the order line must match it exactly.
func CheckContract(line documents.Line, orgID int64, prices []SupplierPrice) error {
	for _, p := range prices {
		if p.ProductID == line.ProductID && p.OrgID == orgID {
			if line.UnitNet != p.UnitNet {
				return fmt.Errorf("procurement: line price %d off contract %d (product %d)",
					line.UnitNet, p.UnitNet, line.ProductID)
			}
			return nil
		}
	}
	return nil
}
