package website

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Store is the persistence contract for published pages.
type Store interface {
	PageBySlug(ctx context.Context, db platform.DBTX, entityID int64, slug string) (Page, error)
	ListPages(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Page, error)
}

// PGStore serves published knowledge articles as pages (ferp_articles,
// migration 0018 — read-only, no website tables of its own).
type PGStore struct{}

// NewPGStore builds a PGStore.
func NewPGStore() *PGStore { return &PGStore{} }

// PageBySlug returns one published article as a page.
func (s *PGStore) PageBySlug(ctx context.Context, db platform.DBTX, entityID int64, slug string) (Page, error) {
	var p Page
	err := db.QueryRow(ctx, `SELECT slug, title, body, updated_at FROM ferp_articles
		WHERE entity_id=$1 AND slug=$2 AND status=1`, entityID, slug,
	).Scan(&p.Slug, &p.Title, &p.Body, &p.UpdatedAt)
	if err != nil {
		return Page{}, fmt.Errorf("website: page %q: %w", slug, platform.ErrNotFound)
	}
	return p, nil
}

// ListPages returns published articles as pages, newest first.
func (s *PGStore) ListPages(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Page, error) {
	rows, err := db.Query(ctx, `SELECT slug, title, body, updated_at FROM ferp_articles
		WHERE entity_id=$1 AND status=1 ORDER BY updated_at DESC LIMIT $2 OFFSET $3`,
		entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Page
	for rows.Next() {
		var p Page
		if err := rows.Scan(&p.Slug, &p.Title, &p.Body, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DictStore is a dict-backed fake: pages seeded in-process (tests, offline
// previews). Only validated pages are accepted.
type DictStore struct {
	mu    sync.Mutex
	pages map[string]Page // key: entityID + "\x00" + slug
}

// NewDictStore builds an empty dict.
func NewDictStore() *DictStore { return &DictStore{pages: map[string]Page{}} }

func pageKey(entityID int64, slug string) string { return fmt.Sprintf("%d\x00%s", entityID, slug) }

// PutPage seeds one page (test/offline helper, not an HTTP write path).
func (d *DictStore) PutPage(_ context.Context, entityID int64, p Page) error {
	if err := p.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pages[pageKey(entityID, p.Slug)] = p
	return nil
}

// PageBySlug returns one dict page (drafts cannot exist here — only seeded
// published content is served).
func (d *DictStore) PageBySlug(_ context.Context, _ platform.DBTX, entityID int64, slug string) (Page, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.pages[pageKey(entityID, slug)]
	if !ok {
		return Page{}, fmt.Errorf("website: page %q: %w", slug, platform.ErrNotFound)
	}
	return p, nil
}

// ListPages returns dict pages sorted by slug.
func (d *DictStore) ListPages(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]Page, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []Page
	for key, p := range d.pages {
		sep := strings.Index(key, "\x00")
		eid, _ := strconv.ParseInt(key[:sep], 10, 64)
		if eid == entityID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
