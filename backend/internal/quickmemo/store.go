package quickmemo

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Store is the persistence contract for memos.
type Store interface {
	Create(ctx context.Context, db platform.DBTX, m *Memo) error
	MemoByID(ctx context.Context, db platform.DBTX, entityID int64, id int64, userLogin string) (Memo, error)
	Update(ctx context.Context, db platform.DBTX, entityID int64, id int64, userLogin, title, body string, rowVersion int64) (Memo, error)
	Delete(ctx context.Context, db platform.DBTX, entityID int64, id int64, userLogin string) error
	ListForUser(ctx context.Context, db platform.DBTX, entityID int64, userLogin string, limit, offset int) ([]Memo, error)
}

// PGStore implements Store against ferp_quickmemos (pending_p5c).
type PGStore struct{}

// NewPGStore builds a PGStore.
func NewPGStore() *PGStore { return &PGStore{} }

const memoCols = `id, entity_id, user_login, title, body, created_at, updated_at, row_version`

// Create inserts one memo.
func (s *PGStore) Create(ctx context.Context, db platform.DBTX, m *Memo) error {
	if err := m.Validate(); err != nil {
		return err
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_quickmemos (entity_id, user_login, title, body)
		VALUES ($1,$2,$3,$4) RETURNING id, created_at, updated_at, row_version`,
		m.EntityID, m.UserLogin, m.Title, m.Body,
	).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt, &m.RowVersion)
}

// MemoByID returns one memo owned by userLogin.
func (s *PGStore) MemoByID(ctx context.Context, db platform.DBTX, entityID int64, id int64, userLogin string) (Memo, error) {
	var m Memo
	err := db.QueryRow(ctx, `SELECT `+memoCols+` FROM ferp_quickmemos
		WHERE id=$1 AND entity_id=$2 AND user_login=$3`, id, entityID, userLogin,
	).Scan(&m.ID, &m.EntityID, &m.UserLogin, &m.Title, &m.Body, &m.CreatedAt, &m.UpdatedAt, &m.RowVersion)
	if err != nil {
		return Memo{}, fmt.Errorf("quickmemo %d: %w", id, platform.ErrNotFound)
	}
	return m, nil
}

// Update edits one memo with an optimistic-locking guard.
func (s *PGStore) Update(ctx context.Context, db platform.DBTX, entityID int64, id int64, userLogin, title, body string, rowVersion int64) (Memo, error) {
	m, err := s.MemoByID(ctx, db, entityID, id, userLogin)
	if err != nil {
		return Memo{}, err
	}
	if m.RowVersion != rowVersion {
		return Memo{}, fmt.Errorf("quickmemo %d: %w", id, platform.ErrVersionConflict)
	}
	m.Title = title
	m.Body = body
	if err := m.Validate(); err != nil {
		return Memo{}, err
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_quickmemos SET title=$1, body=$2,
		updated_at=now(), row_version=row_version+1
		WHERE id=$3 AND entity_id=$4 AND user_login=$5 AND row_version=$6`,
		title, body, id, entityID, userLogin, rowVersion)
	if err != nil {
		return Memo{}, err
	}
	if tag.RowsAffected() == 0 {
		return Memo{}, fmt.Errorf("quickmemo %d: %w", id, platform.ErrVersionConflict)
	}
	m.RowVersion++
	return m, nil
}

// Delete removes one memo owned by userLogin.
func (s *PGStore) Delete(ctx context.Context, db platform.DBTX, entityID int64, id int64, userLogin string) error {
	tag, err := db.Exec(ctx, `DELETE FROM ferp_quickmemos WHERE id=$1 AND entity_id=$2 AND user_login=$3`,
		id, entityID, userLogin)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("quickmemo %d: %w", id, platform.ErrNotFound)
	}
	return nil
}

// ListForUser pages one user's memos, newest first.
func (s *PGStore) ListForUser(ctx context.Context, db platform.DBTX, entityID int64, userLogin string, limit, offset int) ([]Memo, error) {
	rows, err := db.Query(ctx, `SELECT `+memoCols+` FROM ferp_quickmemos
		WHERE entity_id=$1 AND user_login=$2 ORDER BY id DESC LIMIT $3 OFFSET $4`,
		entityID, userLogin, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memo
	for rows.Next() {
		var m Memo
		if err := rows.Scan(&m.ID, &m.EntityID, &m.UserLogin, &m.Title, &m.Body, &m.CreatedAt, &m.UpdatedAt, &m.RowVersion); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu   sync.Mutex
	seq  int64
	rows map[int64]Memo
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore { return &MemoryStore{rows: map[int64]Memo{}} }

// Create inserts one memo.
func (m *MemoryStore) Create(_ context.Context, _ platform.DBTX, memo *Memo) error {
	if err := memo.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	memo.ID = m.seq
	memo.RowVersion = 1
	now := time.Now().UTC()
	memo.CreatedAt = now
	memo.UpdatedAt = now
	m.rows[memo.ID] = *memo
	return nil
}

// MemoByID returns one memo owned by userLogin.
func (m *MemoryStore) MemoByID(_ context.Context, _ platform.DBTX, entityID int64, id int64, userLogin string) (Memo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	memo, ok := m.rows[id]
	if !ok || memo.EntityID != entityID || memo.UserLogin != userLogin {
		return Memo{}, fmt.Errorf("quickmemo %d: %w", id, platform.ErrNotFound)
	}
	return memo, nil
}

// Update edits one memo with an optimistic-locking guard.
func (m *MemoryStore) Update(_ context.Context, _ platform.DBTX, entityID int64, id int64, userLogin, title, body string, rowVersion int64) (Memo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	memo, ok := m.rows[id]
	if !ok || memo.EntityID != entityID || memo.UserLogin != userLogin {
		return Memo{}, fmt.Errorf("quickmemo %d: %w", id, platform.ErrNotFound)
	}
	if memo.RowVersion != rowVersion {
		return Memo{}, fmt.Errorf("quickmemo %d: %w", id, platform.ErrVersionConflict)
	}
	memo.Title = title
	memo.Body = body
	if err := memo.Validate(); err != nil {
		return Memo{}, err
	}
	memo.RowVersion++
	memo.UpdatedAt = time.Now().UTC()
	m.rows[id] = memo
	return memo, nil
}

// Delete removes one memo owned by userLogin.
func (m *MemoryStore) Delete(_ context.Context, _ platform.DBTX, entityID int64, id int64, userLogin string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	memo, ok := m.rows[id]
	if !ok || memo.EntityID != entityID || memo.UserLogin != userLogin {
		return fmt.Errorf("quickmemo %d: %w", id, platform.ErrNotFound)
	}
	delete(m.rows, id)
	return nil
}

// ListForUser pages one user's memos.
func (m *MemoryStore) ListForUser(_ context.Context, _ platform.DBTX, entityID int64, userLogin string, limit, offset int) ([]Memo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Memo
	for _, memo := range m.rows {
		if memo.EntityID == entityID && memo.UserLogin == userLogin {
			out = append(out, memo)
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
