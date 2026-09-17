package collab

import (
	"context"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Service owns comment use cases.
type Service struct {
	Store Store
	Bus   platform.Bus
}

// NewService builds a Service.
func NewService(s Store, bus platform.Bus) *Service { return &Service{Store: s, Bus: bus} }

func (s *Service) publish(ctx context.Context, entityID, id int64, subject string) {
	if s == nil || s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: "comment", EntityID: entityID, ID: id})
}

// Add validates and persists one comment.
func (s *Service) Add(ctx context.Context, db platform.DBTX, c *Comment) error {
	if err := s.Store.Add(ctx, db, c); err != nil {
		return err
	}
	s.publish(ctx, c.EntityID, c.ID, "forgeerp.collab.comment.created.v1")
	return nil
}

// Remove deletes one author-owned comment.
func (s *Service) Remove(ctx context.Context, db platform.DBTX, entityID, id int64, author string) error {
	if err := s.Store.Remove(ctx, db, entityID, id, author); err != nil {
		return err
	}
	s.publish(ctx, entityID, id, "forgeerp.collab.comment.removed.v1")
	return nil
}
