package bookmark

import (
	"context"
	"errors"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Service owns bookmark use cases (toggle needs read+write).
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
	_ = s.Bus.Publish(ctx, platform.Event{Subject: "forgeerp.bookmark.change.v1", Entity: "bookmark", EntityID: entityID, ID: id})
}

// Add persists one bookmark.
func (s *Service) Add(ctx context.Context, db platform.DBTX, b *Bookmark) error {
	if err := s.Store.Add(ctx, db, b); err != nil {
		return err
	}
	s.publish(ctx, b.EntityID, b.ID)
	return nil
}

// Toggle flips membership: present → removed (added=false), absent → added.
// Re-adding an existing bookmark is idempotent, never a conflict.
func (s *Service) Toggle(ctx context.Context, db platform.DBTX, b Bookmark) (added bool, err error) {
	if err := b.Validate(); err != nil {
		return false, err
	}
	list, err := s.Store.ListForUser(ctx, db, b.EntityID, b.UserLogin)
	if err != nil {
		return false, err
	}
	for _, e := range list {
		if e.Scope == b.Scope && e.ObjectType == b.ObjectType && e.ObjectID == b.ObjectID {
			if err := s.Store.Remove(ctx, db, b.EntityID, e.ID, b.UserLogin); err != nil {
				return false, err
			}
			s.publish(ctx, b.EntityID, e.ID)
			return false, nil
		}
	}
	if err := s.Store.Add(ctx, db, &b); err != nil {
		// Lost race with a concurrent add: treat as already present.
		if errors.Is(err, platform.ErrConflict) {
			return true, nil
		}
		return false, err
	}
	s.publish(ctx, b.EntityID, b.ID)
	return true, nil
}
