// Package dynprice implements dynamic price expressions (Phase 5 PORT:
// Dolibarr DynamicPrices equivalent): named rules like "max(base * 0.9,
// cost)" evaluated over base|qty|cost, assigned per (product, org) with
// org 0 meaning all customers. Evaluation is integer-exact (rational
// arithmetic, half-up rounding) — no floats anywhere near money.
package dynprice

import (
	"fmt"
	"strings"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Rule statuses.
const (
	RuleActive   int16 = 1
	RuleArchived int16 = 0
)

// Rule is one named price expression.
type Rule struct {
	ID         int64  `json:"id"`
	EntityID   int64  `json:"entity_id"`
	Code       string `json:"code"`
	Label      string `json:"label"`
	Expression string `json:"expression"`
	Status     int16  `json:"status"`
	RowVersion int64  `json:"row_version"`
}

// Validate checks rule invariants (expression must parse).
func (r Rule) Validate() error {
	if r.EntityID <= 0 {
		return fmt.Errorf("dynprice: entity_id required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(r.Code) == "" {
		return fmt.Errorf("dynprice: code required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(r.Expression) == "" {
		return fmt.Errorf("dynprice: expression required: %w", platform.ErrValidation)
	}
	if _, err := Parse(r.Expression); err != nil {
		return fmt.Errorf("dynprice: %w: %w", err, platform.ErrValidation)
	}
	return nil
}

// Assignment binds a rule to a product (org 0 = all customers).
type Assignment struct {
	ID        int64 `json:"id"`
	EntityID  int64 `json:"entity_id"`
	RuleID    int64 `json:"rule_id"`
	ProductID int64 `json:"product_id"`
	OrgID     int64 `json:"org_id"`
}

// Validate checks assignment invariants.
func (a Assignment) Validate() error {
	if a.EntityID <= 0 {
		return fmt.Errorf("dynprice: entity_id required: %w", platform.ErrValidation)
	}
	if a.RuleID <= 0 || a.ProductID <= 0 {
		return fmt.Errorf("dynprice: rule and product required: %w", platform.ErrValidation)
	}
	if a.OrgID < 0 {
		return fmt.Errorf("dynprice: org_id must be non-negative: %w", platform.ErrValidation)
	}
	return nil
}

// Input carries the evaluation variables (all int64 minor/base units).
type Input struct {
	Base int64 `json:"base"` // list price, minor units
	Qty  int64 `json:"qty"`  // quantity, base units
	Cost int64 `json:"cost"` // unit cost, minor units
}
