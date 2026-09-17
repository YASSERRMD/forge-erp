package collab

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence contract for comments.
type Store interface {
	Add(ctx context.Context, db platform.DBTX, c *Comment) error
	ListForObject(ctx context.Context, db platform.DBTX, entityID int64, scope, objectType string, objectID int64, thread string, limit, offset int) ([]Comment, error)
	Remove(ctx context.Context, db platform.DBTX, entityID, id int64, author string) error
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const commentCols = `id, entity_id, scope, object_type, object_id, thread, author, body, created_at, row_version`

func scanComment(row pgx.Row) (Comment, error) {
	var c Comment
	err := row.Scan(&c.ID, &c.EntityID, &c.Scope, &c.ObjectType, &c.ObjectID,
		&c.Thread, &c.Author, &c.Body, &c.CreatedAt, &c.RowVersion)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Comment{}, platform.ErrNotFound
		}
		return Comment{}, err
	}
	return c, nil
}

// Add persists one comment.
func (s *PGStore) Add(ctx context.Context, db platform.DBTX, c *Comment) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_collab_comments
		(entity_id, scope, object_type, object_id, thread, author, body)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, created_at, row_version`,
		c.EntityID, c.Scope, c.ObjectType, c.ObjectID, c.Thread, c.Author, c.Body).
		Scan(&c.ID, &c.CreatedAt, &c.RowVersion)
}

// ListForObject returns comments on one object, oldest first (thread ""
// lists every thread; a thread value filters to it).
func (s *PGStore) ListForObject(ctx context.Context, db platform.DBTX, entityID int64, scope, objectType string, objectID int64, thread string, limit, offset int) ([]Comment, error) {
	q := `SELECT ` + commentCols + ` FROM ferp_collab_comments
		WHERE entity_id=$1 AND scope=$2 AND object_type=$3 AND object_id=$4
		AND ($5='' OR thread=$5) ORDER BY id LIMIT $6 OFFSET $7`
	rows, err := db.Query(ctx, q, entityID, scope, objectType, objectID, thread, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Remove deletes one comment owned by author (ErrNotFound otherwise —
// authors can only delete their own notes).
func (s *PGStore) Remove(ctx context.Context, db platform.DBTX, entityID, id int64, author string) error {
	res, err := db.Exec(ctx, `DELETE FROM ferp_collab_comments
		WHERE id=$1 AND entity_id=$2 AND author=$3`, id, entityID, author)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("collab comment %d: %w", id, platform.ErrNotFound)
	}
	return nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu   sync.Mutex
	seq  int64
	rows map[int64]Comment
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore { return &MemoryStore{rows: map[int64]Comment{}} }

func (m *MemoryStore) Add(_ context.Context, _ platform.DBTX, c *Comment) error {
	if err := c.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	c.ID = m.seq
	m.rows[c.ID] = *c
	return nil
}

func (m *MemoryStore) ListForObject(_ context.Context, _ platform.DBTX, entityID int64, scope, objectType string, objectID int64, thread string, limit, offset int) ([]Comment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Comment
	for _, c := range m.rows {
		if c.EntityID == entityID && c.Scope == scope && c.ObjectType == objectType &&
			c.ObjectID == objectID && (thread == "" || c.Thread == thread) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) Remove(_ context.Context, _ platform.DBTX, entityID, id int64, author string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.rows[id]
	if !ok || c.EntityID != entityID || c.Author != author {
		return fmt.Errorf("collab comment %d: %w", id, platform.ErrNotFound)
	}
	delete(m.rows, id)
	return nil
}
