// Package kb implements the knowledge base (Dolibarr knowledgemanagement):
// draft/published articles with tags and substring search over title/body.
package kb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Article status.
type ArticleStatus int16

const (
	ArticleDraft     ArticleStatus = 0
	ArticlePublished ArticleStatus = 1
)

// Article is one knowledge entry.
type Article struct {
	ID         int64         `json:"id"`
	EntityID   int64         `json:"entity_id"`
	Slug       string        `json:"slug"` // unique per entity
	Title      string        `json:"title"`
	Body       string        `json:"body"`
	Tags       []string      `json:"tags"`
	Status     ArticleStatus `json:"status"`
	Author     string        `json:"author"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	RowVersion int64         `json:"row_version"`
}

// Validate checks article invariants.
func (a Article) Validate() error {
	if a.EntityID <= 0 {
		return errors.New("kb: entity_id required")
	}
	if strings.TrimSpace(a.Slug) == "" {
		return errors.New("kb: slug required")
	}
	if strings.TrimSpace(a.Title) == "" {
		return errors.New("kb: title required")
	}
	return nil
}

// CanTransition reports whether an article status change is legal.
func (a Article) CanTransition(to ArticleStatus) bool {
	if a.Status == ArticleDraft {
		return to == ArticlePublished
	}
	return to == ArticleDraft // unpublish allowed
}

// Store is the persistence contract for the knowledge base.
type Store interface {
	CreateArticle(ctx context.Context, a *Article) error
	ArticleByID(ctx context.Context, id int64) (Article, error)
	UpdateArticle(ctx context.Context, id int64, title, body string, tags []string, rowVersion int64) (Article, error)
	ListArticles(ctx context.Context, entityID int64, publishedOnly bool, limit, offset int) ([]Article, error)
	SetArticleStatus(ctx context.Context, id int64, to ArticleStatus, rowVersion int64) (Article, error)
	SearchArticles(ctx context.Context, entityID int64, q string, limit int) ([]Article, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const articleCols = `id, entity_id, slug, title, body, tags, status, author, created_at, updated_at, row_version`

func scanArticle(row pgx.Row) (Article, error) {
	var a Article
	var tags []byte
	err := row.Scan(&a.ID, &a.EntityID, &a.Slug, &a.Title, &a.Body, &tags,
		&a.Status, &a.Author, &a.CreatedAt, &a.UpdatedAt, &a.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Article{}, identity.ErrNotFound
	}
	if err != nil {
		return Article{}, err
	}
	_ = json.Unmarshal(tags, &a.Tags)
	return a, nil
}

func (s *PGStore) CreateArticle(ctx context.Context, a *Article) error {
	if err := a.Validate(); err != nil {
		return err
	}
	tags, _ := json.Marshal(a.Tags)
	if tags == nil {
		tags = []byte("[]")
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_articles
		(entity_id, slug, title, body, tags, status, author)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, row_version`,
		a.EntityID, a.Slug, a.Title, a.Body, tags, a.Status, a.Author,
	).Scan(&a.ID, &a.RowVersion)
}

func (s *PGStore) ArticleByID(ctx context.Context, id int64) (Article, error) {
	return scanArticle(s.pool.QueryRow(ctx, `SELECT `+articleCols+` FROM ferp_articles WHERE id=$1`, id))
}

// UpdateArticle edits a draft article (published must be unpublished first).
func (s *PGStore) UpdateArticle(ctx context.Context, id int64, title, body string, tags []string, rowVersion int64) (Article, error) {
	a, err := s.ArticleByID(ctx, id)
	if err != nil {
		return Article{}, err
	}
	if a.RowVersion != rowVersion {
		return Article{}, identity.ErrVersionConflict
	}
	if a.Status != ArticleDraft {
		return Article{}, errors.New("kb: only drafts are editable")
	}
	if strings.TrimSpace(title) == "" {
		return Article{}, errors.New("kb: title required")
	}
	raw, _ := json.Marshal(tags)
	if raw == nil {
		raw = []byte("[]")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_articles SET title=$1, body=$2, tags=$3,
		updated_at=now(), row_version=row_version+1 WHERE id=$4 AND row_version=$5`,
		title, body, raw, id, rowVersion)
	if err != nil {
		return Article{}, err
	}
	if tag.RowsAffected() == 0 {
		return Article{}, identity.ErrVersionConflict
	}
	a.Title = title
	a.Body = body
	a.Tags = tags
	a.RowVersion++
	return a, nil
}

func (s *PGStore) ListArticles(ctx context.Context, entityID int64, publishedOnly bool, limit, offset int) ([]Article, error) {
	q := `SELECT ` + articleCols + ` FROM ferp_articles WHERE entity_id=$1`
	args := []any{entityID}
	if publishedOnly {
		q += ` AND status=1`
	}
	q += ` ORDER BY updated_at DESC LIMIT $2 OFFSET $3`
	rows, err := s.pool.Query(ctx, q, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PGStore) SetArticleStatus(ctx context.Context, id int64, to ArticleStatus, rowVersion int64) (Article, error) {
	a, err := s.ArticleByID(ctx, id)
	if err != nil {
		return Article{}, err
	}
	if a.RowVersion != rowVersion {
		return Article{}, identity.ErrVersionConflict
	}
	if !a.CanTransition(to) {
		return Article{}, errors.New("kb: illegal transition")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_articles SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return Article{}, err
	}
	if tag.RowsAffected() == 0 {
		return Article{}, identity.ErrVersionConflict
	}
	a.Status = to
	a.RowVersion++
	return a, nil
}

func (s *PGStore) SearchArticles(ctx context.Context, entityID int64, q string, limit int) ([]Article, error) {
	q = "%" + strings.ToLower(strings.TrimSpace(q)) + "%"
	rows, err := s.pool.Query(ctx, `SELECT `+articleCols+` FROM ferp_articles
		WHERE entity_id=$1 AND status=1 AND (LOWER(title) LIKE $2 OR LOWER(body) LIKE $2)
		ORDER BY updated_at DESC LIMIT $3`, entityID, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu       sync.Mutex
	seq      int64
	articles map[int64]Article
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{articles: map[int64]Article{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateArticle(_ context.Context, a *Article) error {
	if err := a.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.articles {
		if e.EntityID == a.EntityID && e.Slug == a.Slug {
			return errors.New("kb: duplicate slug")
		}
	}
	a.ID = m.next()
	a.RowVersion = 1
	m.articles[a.ID] = *a
	return nil
}

func (m *MemoryStore) ArticleByID(_ context.Context, id int64) (Article, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.articles[id]
	if !ok {
		return Article{}, identity.ErrNotFound
	}
	return a, nil
}

func (m *MemoryStore) UpdateArticle(_ context.Context, id int64, title, body string, tags []string, rowVersion int64) (Article, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.articles[id]
	if !ok {
		return Article{}, identity.ErrNotFound
	}
	if a.RowVersion != rowVersion {
		return Article{}, identity.ErrVersionConflict
	}
	if a.Status != ArticleDraft {
		return Article{}, errors.New("kb: only drafts are editable")
	}
	if strings.TrimSpace(title) == "" {
		return Article{}, errors.New("kb: title required")
	}
	a.Title = title
	a.Body = body
	a.Tags = tags
	a.RowVersion++
	m.articles[id] = a
	return a, nil
}

func (m *MemoryStore) ListArticles(_ context.Context, entityID int64, publishedOnly bool, limit, offset int) ([]Article, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Article
	for _, a := range m.articles {
		if a.EntityID == entityID && (!publishedOnly || a.Status == ArticlePublished) {
			out = append(out, a)
		}
	}
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) SetArticleStatus(_ context.Context, id int64, to ArticleStatus, rowVersion int64) (Article, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.articles[id]
	if !ok {
		return Article{}, identity.ErrNotFound
	}
	if a.RowVersion != rowVersion {
		return Article{}, identity.ErrVersionConflict
	}
	if !a.CanTransition(to) {
		return Article{}, errors.New("kb: illegal transition")
	}
	a.Status = to
	a.RowVersion++
	m.articles[id] = a
	return a, nil
}

func (m *MemoryStore) SearchArticles(_ context.Context, entityID int64, q string, limit int) ([]Article, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q = strings.ToLower(strings.TrimSpace(q))
	var out []Article
	for _, a := range m.articles {
		if a.EntityID != entityID || a.Status != ArticlePublished {
			continue
		}
		if strings.Contains(strings.ToLower(a.Title), q) || strings.Contains(strings.ToLower(a.Body), q) {
			out = append(out, a)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}
