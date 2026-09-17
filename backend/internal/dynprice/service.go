package dynprice

import (
	"context"
	"fmt"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Service owns price-rule use cases.
type Service struct {
	Store Store
	Bus   platform.Bus
	DB    platform.DBTX
}

// NewService builds a Service.
func NewService(s Store, bus platform.Bus, db platform.DBTX) *Service {
	return &Service{Store: s, Bus: bus, DB: db}
}

func (s *Service) publish(ctx context.Context, entityID, id int64, subject string) {
	if s == nil || s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: "price_rule", EntityID: entityID, ID: id})
}

// CreateRule validates and persists one rule.
func (s *Service) CreateRule(ctx context.Context, r *Rule) error {
	if err := s.Store.CreateRule(ctx, s.DB, r); err != nil {
		return err
	}
	s.publish(ctx, r.EntityID, r.ID, "forgeerp.dynprice.rule.created.v1")
	return nil
}

// EvaluateRule runs one rule by id over in (archived rules are 422).
func (s *Service) EvaluateRule(ctx context.Context, entityID, id int64, in Input) (int64, error) {
	r, err := s.Store.RuleByID(ctx, s.DB, entityID, id)
	if err != nil {
		return 0, err
	}
	if r.Status != RuleActive {
		return 0, fmt.Errorf("dynprice rule %d: archived: %w", id, platform.ErrValidation)
	}
	price, err := Eval(r.Expression, in)
	if err != nil {
		return 0, fmt.Errorf("dynprice rule %d: %w: %w", id, err, platform.ErrValidation)
	}
	return price, nil
}

// PriceForProduct evaluates the first matching assignment for
// (product, org): org-specific rules win over org-0 fallbacks. With no
// assignment the caller's base passes through unchanged (ok=false).
func (s *Service) PriceForProduct(ctx context.Context, entityID, productID, orgID int64, in Input) (price int64, ok bool, err error) {
	list, err := s.Store.AssignmentsFor(ctx, s.DB, entityID, productID, orgID)
	if err != nil {
		return 0, false, err
	}
	for _, a := range list {
		r, err := s.Store.RuleByID(ctx, s.DB, entityID, a.RuleID)
		if err != nil || r.Status != RuleActive {
			continue
		}
		price, err := Eval(r.Expression, in)
		if err != nil {
			continue
		}
		return price, true, nil
	}
	return in.Base, false, nil
}
