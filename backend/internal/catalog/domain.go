// Package catalog implements products/services (Dolibarr llx_product and
// satellites), warehouses (llx_entrepot), the append-only stock ledger
// (llx_stock_mouvement) with weighted-average (PMP) costing, and lot
// traceability (llx_product_lot). Money in minor units; quantities in integer
// base units (intentional difference: Dolibarr allows fractional quantities).
package catalog

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ProductType distinguishes stocked goods from services (Dolibarr fk_product_type).
type ProductType int16

const (
	ProductGoods   ProductType = 0
	ProductService ProductType = 1
)

// Product status (Dolibarr llx_product.tosell/tobuy collapsed to one flag pair).
type ProductStatus int16

const (
	ProductActive   ProductStatus = 1
	ProductArchived ProductStatus = 0
)

var skuPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Product is a sellable/purchasable item scoped to an entity.
type Product struct {
	ID           int64          `json:"id"`
	EntityID     int64          `json:"entity_id"`
	SKU          string         `json:"sku"` // llx_product.ref, unique per entity
	Name         string         `json:"name"`
	Type         ProductType    `json:"type"`
	Unit         string         `json:"unit"`
	NetPrice     int64          `json:"net_price"`      // minor units, excl. VAT
	VATRateBps   int            `json:"vat_rate_bps"`   // basis points: 2000 = 20% (llx_c_tva.taux*100)
	Status       ProductStatus  `json:"status"`
	StockTracked bool           `json:"stock_tracked"`  // goods normally tracked; services never
	CustomFields map[string]any `json:"custom_fields"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	CreatedBy    *int64         `json:"created_by"`
	UpdatedBy    *int64         `json:"updated_by"`
	RowVersion   int64          `json:"row_version"`
}

// Validate enforces catalog rules.
func (p Product) Validate() error {
	if strings.TrimSpace(p.SKU) == "" || !skuPattern.MatchString(p.SKU) {
		return fmt.Errorf("catalog: bad SKU %q", p.SKU)
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("catalog: product name required")
	}
	if p.Type != ProductGoods && p.Type != ProductService {
		return fmt.Errorf("catalog: bad product type %d", p.Type)
	}
	if p.NetPrice < 0 {
		return errors.New("catalog: negative net price")
	}
	if p.VATRateBps < 0 || p.VATRateBps > 10000 {
		return fmt.Errorf("catalog: bad VAT rate %d bps", p.VATRateBps)
	}
	if p.Type == ProductService && p.StockTracked {
		return errors.New("catalog: services cannot be stock-tracked")
	}
	return nil
}

// GrossPrice returns VAT-inclusive price in minor units (half-up rounding).
func (p Product) GrossPrice() int64 {
	return p.NetPrice + (p.NetPrice*int64(p.VATRateBps)+5000)/10000
}

// Warehouse scopes stock (Dolibarr llx_entrepot).
type Warehouse struct {
	ID        int64     `json:"id"`
	EntityID  int64     `json:"entity_id"`
	Code      string    `json:"code"` // unique per entity
	Label     string    `json:"label"`
	Status    int16     `json:"status"` // 1 open, 0 closed
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Validate warehouse rules.
func (w Warehouse) Validate() error {
	if strings.TrimSpace(w.Code) == "" {
		return errors.New("catalog: warehouse code required")
	}
	if strings.TrimSpace(w.Label) == "" {
		return errors.New("catalog: warehouse label required")
	}
	return nil
}

// MovementReason classifies ledger entries (Dolibarr inventorycode/movement labels).
type MovementReason string

const (
	ReasonReceipt      MovementReason = "receipt"
	ReasonShipment     MovementReason = "shipment"
	ReasonAdjustIn     MovementReason = "adjust_in"
	ReasonAdjustOut    MovementReason = "adjust_out"
	ReasonTransferIn   MovementReason = "transfer_in"
	ReasonTransferOut  MovementReason = "transfer_out"
	ReasonConsume      MovementReason = "consume" // MO component consumption (manufacturing)
	ReasonProduce      MovementReason = "produce" // MO finished-good receipt (manufacturing)
)

// StockMovement is one append-only ledger line. Qty > 0 = stock in, < 0 = out.
// UnitCost applies to inbound lines (receipt/adjust_in/transfer_in).
type StockMovement struct {
	ID          int64          `json:"id"`
	EntityID    int64          `json:"entity_id"`
	ProductID   int64          `json:"product_id"`
	WarehouseID int64          `json:"warehouse_id"`
	LotID       *int64         `json:"lot_id"`
	Qty         int64          `json:"qty"`
	UnitCost    int64          `json:"unit_cost"`
	Reason      MovementReason `json:"reason"`
	Ref         string         `json:"ref"` // source document ref (e.g. shipment/invoice)
	CreatedAt   time.Time      `json:"created_at"`
	CreatedBy   *int64         `json:"created_by"`
}

// Validate movement rules.
func (m StockMovement) Validate() error {
	if m.ProductID == 0 || m.WarehouseID == 0 {
		return errors.New("catalog: movement requires product and warehouse")
	}
	if m.Qty == 0 {
		return errors.New("catalog: movement quantity cannot be zero")
	}
	if m.UnitCost < 0 {
		return errors.New("catalog: negative unit cost")
	}
	switch m.Reason {
	case ReasonReceipt, ReasonShipment, ReasonAdjustIn, ReasonAdjustOut,
		ReasonTransferIn, ReasonTransferOut, ReasonConsume, ReasonProduce:
		return nil
	}
	return fmt.Errorf("catalog: unknown movement reason %q", m.Reason)
}

// Inbound reports whether the line adds stock.
func (m StockMovement) Inbound() bool { return m.Qty > 0 }

// StockLevel is the derived on-hand position with PMP valuation.
type StockLevel struct {
	ProductID   int64 `json:"product_id"`
	WarehouseID int64 `json:"warehouse_id"`
	Qty         int64 `json:"qty"`
	TotalValue  int64 `json:"total_value"` // minor units at PMP
}

// PMP returns the weighted-average unit cost (0 when empty).
func (l StockLevel) PMP() int64 {
	if l.Qty <= 0 {
		return 0
	}
	return (l.TotalValue + l.Qty/2) / l.Qty
}

// ComputePMP returns the new average after receiving qty at unitCost (half-up).
func ComputePMP(curQty, curValue, receiptQty, unitCost int64) (newQty, newValue int64) {
	newQty = curQty + receiptQty
	newValue = curValue + receiptQty*unitCost
	return newQty, newValue
}

// Apply validates a movement against the current level (negative-stock guard)
// and returns the new level. allowNegative mirrors Dolibarr's stock option.
func Apply(level StockLevel, m StockMovement, allowNegative bool) (StockLevel, error) {
	if err := m.Validate(); err != nil {
		return level, err
	}
	next := level
	next.Qty += m.Qty
	if next.Qty < 0 && !allowNegative {
		return level, fmt.Errorf("catalog: insufficient stock (have %d, move %d)", level.Qty, m.Qty)
	}
	if m.Inbound() {
		_, next.TotalValue = ComputePMP(level.Qty, level.TotalValue, m.Qty, m.UnitCost)
	} else {
		out := -m.Qty
		relief := level.PMP() * out
		next.TotalValue = level.TotalValue - relief
		if next.Qty == 0 || next.TotalValue < 0 {
			next.TotalValue = 0
		}
	}
	return next, nil
}

// Lot tracks batch/lot identity with optional expiry (Dolibarr llx_product_lot).
type Lot struct {
	ID        int64      `json:"id"`
	EntityID  int64      `json:"entity_id"`
	ProductID int64      `json:"product_id"`
	Number    string     `json:"number"` // unique per product
	ExpiresAt *time.Time `json:"expires_at"`
}

// Validate lot rules.
func (l Lot) Validate() error {
	if l.ProductID == 0 {
		return errors.New("catalog: lot requires a product")
	}
	if strings.TrimSpace(l.Number) == "" {
		return errors.New("catalog: lot number required")
	}
	return nil
}
