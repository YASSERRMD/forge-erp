package partnership

import (
	"context"
	"fmt"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Service owns the referral/accrual boundary: tier resolution and amount
// math live here so HTTP and future callers share the guarantee. Payout
// (settlement) is out of scope — this service only records accruals.
type Service struct {
	Store Store
	Bus   platform.Bus
	DB    platform.DBTX
	Now   func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) published(ctx context.Context, entityID, id int64, subject, entity string) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

// RegisterReferral records a referral link from a referrer org to a referred
// org under a program (both org_ids must exist in partners — enforced by the
// caller seam; this package stores the ids only). A blank Code is minted
// server-side; collisions surface as ErrConflict for retry.
func (s *Service) RegisterReferral(ctx context.Context, entityID, programID, referrerOrgID, referredOrgID int64, code string) (Referral, error) {
	if _, err := s.Store.ProgramByID(ctx, s.DB, entityID, programID); err != nil {
		return Referral{}, err
	}
	r := &Referral{EntityID: entityID, ProgramID: programID,
		ReferrerOrgID: referrerOrgID, ReferredOrgID: referredOrgID,
		Code: code, Status: ReferralActive}
	if r.Code == "" {
		r.Code = fmt.Sprintf("REF-%d-%d", programID, s.now().UnixNano())
	}
	if err := r.Validate(); err != nil {
		return Referral{}, fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if err := s.Store.CreateReferral(ctx, s.DB, r); err != nil {
		return Referral{}, err
	}
	s.published(ctx, entityID, r.ID, "forgeerp.partnership.referral.registered.v1", "referral")
	return *r, nil
}

// AccrueCommission records commission on one referred sale total. The tier
// rate resolves against the cumulative referred total (prior accruals +
// this sale), and the amount is saleTotal * rate / 10000 in int64 minor
// units. Inactive referrals reject with ErrValidation. Payout is out of
// scope — the accrual row is the terminal record.
func (s *Service) AccrueCommission(ctx context.Context, entityID, referralID, saleTotal int64) (Accrual, error) {
	if saleTotal <= 0 {
		return Accrual{}, fmt.Errorf("partnership: sale_total must be positive: %w", platform.ErrValidation)
	}
	r, err := s.Store.ReferralByID(ctx, s.DB, entityID, referralID)
	if err != nil {
		return Accrual{}, err
	}
	if r.Status != ReferralActive {
		return Accrual{}, fmt.Errorf("partnership: referral not active: %w", platform.ErrValidation)
	}
	p, err := s.Store.ProgramByID(ctx, s.DB, entityID, r.ProgramID)
	if err != nil {
		return Accrual{}, err
	}
	prior, err := s.Store.ReferredTotal(ctx, s.DB, entityID, referralID)
	if err != nil {
		return Accrual{}, err
	}
	rate := TierRate(p.Tiers, prior+saleTotal)
	amount, err := AccrueAmount(saleTotal, rate)
	if err != nil {
		return Accrual{}, fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	a := &Accrual{EntityID: entityID, ReferralID: referralID,
		SaleTotal: saleTotal, RateBps: rate, Amount: amount}
	if err := s.Store.RecordAccrual(ctx, s.DB, a); err != nil {
		return Accrual{}, err
	}
	s.published(ctx, entityID, a.ID, "forgeerp.partnership.commission.accrued.v1", "accrual")
	return *a, nil
}
