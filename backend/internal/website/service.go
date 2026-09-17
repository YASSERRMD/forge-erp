package website

import (
	"context"
	"fmt"
	"strings"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Service owns the read-only content surface.
type Service struct {
	Store Store
	Bus   platform.Bus
	DB    platform.DBTX
}

// NewService builds a Service.
func NewService(s Store, bus platform.Bus, db platform.DBTX) *Service {
	return &Service{Store: s, Bus: bus, DB: db}
}

// Sitemap builds the page index (loc = "/pages/<slug>", lastmod = date).
func (s *Service) Sitemap(ctx context.Context, entityID int64) ([]SitemapEntry, error) {
	pages, err := s.Store.ListPages(ctx, s.DB, entityID, 1000, 0)
	if err != nil {
		return nil, err
	}
	out := make([]SitemapEntry, 0, len(pages))
	for _, p := range pages {
		out = append(out, SitemapEntry{
			Loc:     "/pages/" + strings.TrimSpace(p.Slug),
			LastMod: p.UpdatedAt.Format("2006-01-02"),
		})
	}
	return out, nil
}

// Page resolves one slug (404 outside the entity or unpublished).
func (s *Service) Page(ctx context.Context, entityID int64, slug string) (Page, error) {
	if strings.TrimSpace(slug) == "" {
		return Page{}, fmt.Errorf("website: slug required: %w", platform.ErrValidation)
	}
	return s.Store.PageBySlug(ctx, s.DB, entityID, slug)
}
