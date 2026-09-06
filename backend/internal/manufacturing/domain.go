// Package manufacturing implements bills of materials and manufacturing
// orders lite (Dolibarr llx_bom + llx_mrp_mo): explode a BOM into component
// requirements and post consume/produce stock moves through the catalog
// ledger when an order is produced.
package manufacturing

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// BOM status (Dolibarr llx_bom.status).
type BOMStatus int16

const (
	BOMDraft    BOMStatus = 0
	BOMActive   BOMStatus = 1
	BOMObsolete BOMStatus = 2
)

// MO status (Dolibarr llx_mrp_mo.status).
type MOStatus int16

const (
	MODraft      MOStatus = 0
	MOValidated  MOStatus = 1
	MOInProgress MOStatus = 2
	MOProduced   MOStatus = 3
	MOCanceled   MOStatus = -1
)

// BOM is a bill of materials for one finished product.
type BOM struct {
	ID        int64     `json:"id"`
	EntityID  int64     `json:"entity_id"`
	Ref       string    `json:"ref"` // unique per entity
	ProductID int64     `json:"product_id"`
	Label     string    `json:"label"`
	Revision  int32     `json:"revision"`
	Status    BOMStatus `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	RowVersion int64    `json:"row_version"`
}

// Validate checks BOM invariants.
func (b BOM) Validate() error {
	if b.EntityID <= 0 {
		return errors.New("manufacturing: entity_id required")
	}
	if strings.TrimSpace(b.Ref) == "" {
		return errors.New("manufacturing: ref required")
	}
	if b.ProductID <= 0 {
		return errors.New("manufacturing: product_id required")
	}
	if strings.TrimSpace(b.Label) == "" {
		return errors.New("manufacturing: label required")
	}
	return nil
}

// CanTransition reports whether a BOM status change is legal.
func (b BOM) CanTransition(to BOMStatus) bool {
	switch b.Status {
	case BOMDraft:
		return to == BOMActive || to == BOMObsolete
	case BOMActive:
		return to == BOMObsolete
	default:
		return false
	}
}

// BOMLine is one component requirement per finished unit (integer units).
type BOMLine struct {
	ID          int64 `json:"id"`
	EntityID    int64 `json:"entity_id"`
	BOMID       int64 `json:"bom_id"`
	ComponentID int64 `json:"component_id"`
	Qty         int64 `json:"qty"` // per finished unit, > 0
	Position    int32 `json:"position"`
}

// Validate checks line invariants (self-reference needs the BOM product id).
func (l BOMLine) Validate(bomProductID int64) error {
	if l.EntityID <= 0 || l.BOMID <= 0 {
		return errors.New("manufacturing: entity_id and bom_id required")
	}
	if l.ComponentID <= 0 {
		return errors.New("manufacturing: component_id required")
	}
	if l.ComponentID == bomProductID {
		return errors.New("manufacturing: component cannot be the finished product")
	}
	if l.Qty <= 0 {
		return errors.New("manufacturing: qty must be positive")
	}
	return nil
}

// Requirement is an exploded component need for a production run.
type Requirement struct {
	ComponentID int64 `json:"component_id"`
	Qty         int64 `json:"qty"`
}

// Explode scales BOM lines to a production quantity.
func Explode(lines []BOMLine, qty int64) ([]Requirement, error) {
	if qty <= 0 {
		return nil, errors.New("manufacturing: production qty must be positive")
	}
	out := make([]Requirement, 0, len(lines))
	for _, l := range lines {
		out = append(out, Requirement{ComponentID: l.ComponentID, Qty: l.Qty * qty})
	}
	return out, nil
}

// ManufacturingOrder is a production run (Dolibarr llx_mrp_mo).
type ManufacturingOrder struct {
	ID          int64     `json:"id"`
	EntityID    int64     `json:"entity_id"`
	Ref         string    `json:"ref"` // unique per entity
	BOMID       int64     `json:"bom_id"`
	ProductID   int64     `json:"product_id"`
	WarehouseID int64     `json:"warehouse_id"` // produce into / consume from
	Qty         int64     `json:"qty"`          // finished units, > 0
	Status      MOStatus  `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	RowVersion  int64     `json:"row_version"`
}

// Validate checks MO invariants.
func (m ManufacturingOrder) Validate() error {
	if m.EntityID <= 0 {
		return errors.New("manufacturing: entity_id required")
	}
	if strings.TrimSpace(m.Ref) == "" {
		return errors.New("manufacturing: ref required")
	}
	if m.BOMID <= 0 || m.ProductID <= 0 || m.WarehouseID <= 0 {
		return errors.New("manufacturing: bom_id, product_id and warehouse_id required")
	}
	if m.Qty <= 0 {
		return errors.New("manufacturing: qty must be positive")
	}
	return nil
}

// CanTransition reports whether an MO status change is legal.
func (m ManufacturingOrder) CanTransition(to MOStatus) bool {
	switch m.Status {
	case MODraft:
		return to == MOValidated || to == MOCanceled
	case MOValidated:
		return to == MOInProgress || to == MOCanceled
	case MOInProgress:
		return to == MOProduced || to == MOCanceled
	default:
		return false
	}
}

// ProducePlan carries the ledger postings for one production run.
type ProducePlan struct {
	Consumes []ConsumeLine `json:"consumes"`
	Produce  ProduceLine  `json:"produce"`
}

// ConsumeLine is one component posting (negative qty).
type ConsumeLine struct {
	ComponentID int64 `json:"component_id"`
	Qty         int64 `json:"qty"` // negative
}

// ProduceLine is the finished-good posting (positive qty).
type ProduceLine struct {
	ProductID int64 `json:"product_id"`
	Qty       int64 `json:"qty"` // positive
}

// PlanProduce builds the ledger plan for producing mo from its BOM lines.
func PlanProduce(mo ManufacturingOrder, lines []BOMLine) (ProducePlan, error) {
	if mo.Status != MOInProgress {
		return ProducePlan{}, fmt.Errorf("manufacturing: MO must be in progress to produce")
	}
	reqs, err := Explode(lines, mo.Qty)
	if err != nil {
		return ProducePlan{}, err
	}
	plan := ProducePlan{Produce: ProduceLine{ProductID: mo.ProductID, Qty: mo.Qty}}
	for _, r := range reqs {
		plan.Consumes = append(plan.Consumes, ConsumeLine{ComponentID: r.ComponentID, Qty: -r.Qty})
	}
	return plan, nil
}
