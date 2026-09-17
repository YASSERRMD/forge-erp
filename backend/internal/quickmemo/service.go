package quickmemo

import (
	"context"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Service owns memo use cases (thin delegation + event fan-out).
type Service struct {
	Store Store
	Bus   platform.Bus
}

// NewService builds a Service.
func NewService(s Store, bus platform.Bus) *Service { return &Service{Store: s, Bus: bus} }

func (s *Service) publish(ctx context.Context, entityID int64, id int64) {
	if s == nil || s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{Subject: "forgeerp.quickmemo.change.v1", Entity: "memo", EntityID: entityID, ID: id})
}

// Create persists one memo.
func (s *Service) Create(ctx context.Context, db platform.DBTX, m *Memo) error {
	if err := s.Store.Create(ctx, db, m); err != nil {
		return err
	}
	s.publish(ctx, m.EntityID, m.ID)
	return nil
}

// Update edits one memo with an optimistic-locking guard.
func (s *Service) Update(ctx context.Context, db platform.DBTX, entityID int64, id int64, userLogin, title, body string, rowVersion int64) (Memo, error) {
	m, err := s.Store.Update(ctx, db, entityID, id, userLogin, title, body, rowVersion)
	if err != nil {
		return Memo{}, err
	}
	s.publish(ctx, entityID, id)
	return m, nil
}

// Delete removes one memo.
func (s *Service) Delete(ctx context.Context, db platform.DBTX, entityID int64, id int64, userLogin string) error {
	if err := s.Store.Delete(ctx, db, entityID, id, userLogin); err != nil {
		return err
	}
	s.publish(ctx, entityID, id)
	return nil
}
