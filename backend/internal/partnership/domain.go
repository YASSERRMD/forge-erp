// Package partnership implements partner programs (referral tiers, referral
// links to organizations via org_id, commission rules as basis points).
//
// Referral registration + commission accrual on referred sales totals are in
// scope and recorded as accrual rows. Commission PAYOUT (settlement, credit
// notes, bank transfers) is explicitly OUT OF SCOPE: no payout endpoint,
// table, or transition exists here; a later phase will settle accruals.
package partnership

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Referral statuses.
const (
	ReferralActive   = "active"
	ReferralInactive = "inactive"
)

// Tier is one commission band: referred cumulative sales >= MinTotal (minor
// units) earn RateBps basis points (0..10000) on each new referred sale.
type Tier struct {
	Name     string `json:"name"`
	MinTotal int64  `json:"min_total"`
	RateBps  int64  `json:"rate_bps"`
}

// Validate checks one tier.
func (t Tier) Validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("partnership: tier name required")
	}
	if t.MinTotal < 0 {
		return errors.New("partnership: tier min_total negative")
	}
	if t.RateBps < 0 || t.RateBps > 10000 {
		return fmt.Errorf("partnership: tier rate_bps %d out of range 0..10000", t.RateBps)
	}
	return nil
}

// Program is a partner program with ordered commission tiers.
type Program struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	Tiers      []Tier    `json:"tiers"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	RowVersion int64     `json:"row_version"`
}

// Validate checks program invariants (tiers ascending, rates in range).
func (p Program) Validate() error {
	if p.EntityID <= 0 {
		return errors.New("partnership: entity_id required")
	}
	if strings.TrimSpace(p.Code) == "" || strings.TrimSpace(p.Name) == "" {
		return errors.New("partnership: code and name required")
	}
	prev := int64(-1)
	for i, t := range p.Tiers {
		if err := t.Validate(); err != nil {
			return err
		}
		if t.MinTotal <= prev {
			return fmt.Errorf("partnership: tier %d min_total must ascend", i)
		}
		prev = t.MinTotal
	}
	return nil
}

// TierRate resolves the rate for a cumulative referred total: the highest
// tier whose MinTotal is reached (0 when no tier matches or no tiers).
func TierRate(tiers []Tier, cumulativeTotal int64) int64 {
	var rate int64
	for _, t := range tiers {
		if cumulativeTotal >= t.MinTotal {
			rate = t.RateBps
		}
	}
	return rate
}

// AccrueAmount computes the commission for one referred sale entirely in
// int64 minor units (no floats): amount = saleTotal * rateBps / 10000.
func AccrueAmount(saleTotal, rateBps int64) (int64, error) {
	if saleTotal <= 0 {
		return 0, errors.New("partnership: sale_total must be positive")
	}
	if rateBps < 0 || rateBps > 10000 {
		return 0, fmt.Errorf("partnership: rate_bps %d out of range 0..10000", rateBps)
	}
	return saleTotal * rateBps / 10000, nil
}

// Referral links a referrer organization to a referred organization under a
// program. Org links are plain org_id values owned by partners/members —
// this package never writes those tables (local seam only).
type Referral struct {
	ID           int64     `json:"id"`
	EntityID     int64     `json:"entity_id"`
	ProgramID    int64     `json:"program_id"`
	ReferrerOrgID int64    `json:"referrer_org_id"`
	ReferredOrgID int64    `json:"referred_org_id"`
	Code         string    `json:"code"` // referral-link token, unique per entity
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	RowVersion   int64     `json:"row_version"`
}

// Validate checks referral invariants.
func (r Referral) Validate() error {
	if r.EntityID <= 0 || r.ProgramID <= 0 {
		return errors.New("partnership: entity_id and program_id required")
	}
	if r.ReferrerOrgID <= 0 || r.ReferredOrgID <= 0 {
		return errors.New("partnership: referrer and referred org ids required")
	}
	if r.ReferrerOrgID == r.ReferredOrgID {
		return errors.New("partnership: referrer and referred orgs must differ")
	}
	if r.Status != "" && r.Status != ReferralActive && r.Status != ReferralInactive {
		return fmt.Errorf("partnership: unknown referral status %q", r.Status)
	}
	return nil
}

// Accrual records commission earned on one referred sale total. Payout is
// out of scope (see package comment): rows accumulate, nothing settles them.
type Accrual struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	ReferralID int64     `json:"referral_id"`
	SaleTotal  int64     `json:"sale_total"`
	RateBps    int64     `json:"rate_bps"`
	Amount     int64     `json:"amount"`
	CreatedAt  time.Time `json:"created_at"`
}

// Validate checks accrual invariants.
func (a Accrual) Validate() error {
	if a.EntityID <= 0 || a.ReferralID <= 0 {
		return errors.New("partnership: entity_id and referral_id required")
	}
	if _, err := AccrueAmount(a.SaleTotal, a.RateBps); err != nil {
		return err
	}
	if a.Amount != a.SaleTotal*a.RateBps/10000 {
		return errors.New("partnership: amount inconsistent with sale_total * rate_bps")
	}
	return nil
}
