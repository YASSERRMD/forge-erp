// Package website is NOT a CMS: it is a read-only content API for a static
// site. Published knowledge articles (and future published records) are
// exposed as JSON pages plus a sitemap endpoint. There are no drafts, no
// edits, no themes here — authoring stays in the owning contexts.
package website

import (
	"fmt"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Page is one published document rendered for the static site.
type Page struct {
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Validate checks page invariants (slug/title present).
func (p Page) Validate() error {
	if strings.TrimSpace(p.Slug) == "" {
		return fmt.Errorf("website: slug required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(p.Title) == "" {
		return fmt.Errorf("website: title required: %w", platform.ErrValidation)
	}
	return nil
}

// SitemapEntry is one URL in the generated sitemap.
type SitemapEntry struct {
	Loc     string `json:"loc"`
	LastMod string `json:"lastmod"`
}
