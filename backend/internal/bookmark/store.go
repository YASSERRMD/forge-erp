package bookmark

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Store is the persistence contract for bookmarks.
type Store interface {
	Add(ctx context.Context, db platform.DBTX, b *Bookmark) error
	Remove(ctx context.Context, db platform.DBTX, entityID int64, id int64, userLogin string) error
	ListForUser(ctx context.Context, db platform.DBTX, entityID int64, userLogin string) ([]Bookmark, error)
}

// PGStore implements Store against ferp_bookmarks (pending_p5c).
type PGStore struct{}

// NewPGStore builds a PGStore.
func NewPGStore() *PGStore { return &PGStore{} }

// Add inserts one bookmark (duplicates conflict with ErrConflict).
func (s *PGStore) Add(ctx context.Context, db platform.DBTX, b *Bookmark) error {
	if err := b.Validate(); err != nil {
		return err
	}
	err := db.QueryRow(ctx, `INSERT INTO ferp_bookmarks
		(entity_id, user_login, scope, object_type, object_id)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`,
		b.EntityID, b.UserLogin, b.Scope, b.ObjectType, b.ObjectID,
	).Scan(&b.ID, &b.CreatedAt)
	if err != nil {
		if platform.ErrorCode(err) == 409 {
			return fmt.Errorf("bookmark: already bookmarked: %w", platform.ErrConflict)
		}
		return err
	}
	return nil
}

// Remove deletes one bookmark owned by userLogin.
func (s *PGStore) Remove(ctx context.Context, db platform.DBTX, entityID int64, id int64, userLogin string) error {
	tag, err := db.Exec(ctx, `DELETE FROM ferp_bookmarks WHERE id=$1 AND entity_id=$2 AND user_login=$3`,
		id, entityID, userLogin)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("bookmark %d: %w", id, platform.ErrNotFound)
	}
	return nil
}

// ListForUser returns one user's bookmarks, newest first.
func (s *PGStore) ListForUser(ctx context.Context, db platform.DBTX, entityID int64, userLogin string) ([]Bookmark, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, user_login, scope, object_type, object_id, created_at
		FROM ferp_bookmarks WHERE entity_id=$1 AND user_login=$2 ORDER BY id DESC`, entityID, userLogin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bookmark
	for rows.Next() {
		var b Bookmark
		if err := rows.Scan(&b.ID, &b.EntityID, &b.UserLogin, &b.Scope, &b.ObjectType, &b.ObjectID, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu   sync.Mutex
	seq  int64
	rows map[int64]Bookmark
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore { return &MemoryStore{rows: map[int64]Bookmark{}} }

// Add inserts one bookmark (duplicates conflict).
func (m *MemoryStore) Add(_ context.Context, _ platform.DBTX, b *Bookmark) error {
	if err := b.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.rows {
		if e.EntityID == b.EntityID && e.UserLogin == b.UserLogin &&
			e.Scope == b.Scope && e.ObjectType == b.ObjectType && e.ObjectID == b.ObjectID {
			return fmt.Errorf("bookmark: already bookmarked: %w", platform.ErrConflict)
		}
	}
	m.seq++
	b.ID = m.seq
	b.CreatedAt = time.Now().UTC()
	m.rows[b.ID] = *b
	return nil
}

// Remove deletes one bookmark owned by userLogin.
func (m *MemoryStore) Remove(_ context.Context, _ platform.DBTX, entityID int64, id int64, userLogin string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.rows[id]
	if !ok || b.EntityID != entityID || b.UserLogin != userLogin {
		return fmt.Errorf("bookmark %d: %w", id, platform.ErrNotFound)
	}
	delete(m.rows, id)
	return nil
}

// ListForUser returns one user's bookmarks.
func (m *MemoryStore) ListForUser(_ context.Context, _ platform.DBTX, entityID int64, userLogin string) ([]Bookmark, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Bookmark
	for _, b := range m.rows {
		if b.EntityID == entityID && b.UserLogin == userLogin {
			out = append(out, b)
		}
	}
	return out, nil
}
