// Package stocktransfer implements the inter-warehouse transfer document
// (Phase 5 PORT: Dolibarr StockTransfer equivalent): a draft transfer with
// product lines validates into paired stock movements (out of the source,
// into the destination) sharing one TRF-YYYYMM-#### reference. Validated
// transfers are terminal — reversal is a new transfer in the other direction.
package stocktransfer

import (
	"fmt"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Transfer statuses (mirror documents.TypeTransfer transitions).
const (
	StatusDraft     int16 = 0
	StatusValidated int16 = 1
	StatusCanceled  int16 = 9
)

// Transfer is one inter-warehouse transfer header.
type Transfer struct {
	ID                int64     `json:"id"`
	EntityID          int64     `json:"entity_id"`
	Ref               string    `json:"ref"`
	SourceWarehouseID int64     `json:"source_warehouse_id"`
	DestWarehouseID   int64     `json:"dest_warehouse_id"`
	Status            int16     `json:"status"`
	Note              string    `json:"note"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	CreatedBy         *int64    `json:"created_by,omitempty"`
	UpdatedBy         *int64    `json:"updated_by,omitempty"`
	RowVersion        int64     `json:"row_version"`
}

// Validate checks header invariants (ref is minted by the store, not the caller).
func (t Transfer) Validate() error {
	if t.EntityID <= 0 {
		return fmt.Errorf("stocktransfer: entity_id required: %w", platform.ErrValidation)
	}
	if t.SourceWarehouseID <= 0 || t.DestWarehouseID <= 0 {
		return fmt.Errorf("stocktransfer: source and destination warehouses required: %w", platform.ErrValidation)
	}
	if t.SourceWarehouseID == t.DestWarehouseID {
		return fmt.Errorf("stocktransfer: source and destination differ: %w", platform.ErrValidation)
	}
	return nil
}

// TransferLine is one product movement within a transfer (base units;
// UnitCost is the PMP snapshot taken at validation, 0 until then).
type TransferLine struct {
	ID         int64 `json:"id"`
	TransferID int64 `json:"transfer_id"`
	Pos        int   `json:"pos"`
	ProductID  int64 `json:"product_id"`
	Qty        int64 `json:"qty"`
	UnitCost   int64 `json:"unit_cost"`
}

// Validate checks line invariants.
func (l TransferLine) Validate() error {
	if l.ProductID <= 0 {
		return fmt.Errorf("stocktransfer: product required: %w", platform.ErrValidation)
	}
	if l.Qty <= 0 {
		return fmt.Errorf("stocktransfer: qty must be positive: %w", platform.ErrValidation)
	}
	if l.UnitCost < 0 {
		return fmt.Errorf("stocktransfer: unit_cost must be non-negative: %w", platform.ErrValidation)
	}
	return nil
}
