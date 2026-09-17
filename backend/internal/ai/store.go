package ai

import (
	"context"
	"sort"
	"sync"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence contract for run logs.
type Store interface {
	Log(ctx context.Context, db platform.DBTX, r *Run) error
	List(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Run, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

// Log appends one run row.
func (s *PGStore) Log(ctx context.Context, db platform.DBTX, r *Run) error {
	return db.QueryRow(ctx, `INSERT INTO ferp_ai_runs
		(entity_id, model, prompt_excerpt, output_excerpt, duration_ms)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`,
		r.EntityID, r.Model, r.PromptExcerpt, r.OutputExcerpt, r.DurationMs).
		Scan(&r.ID, &r.CreatedAt)
}

// List returns run logs newest first.
func (s *PGStore) List(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Run, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, model, prompt_excerpt,
		output_excerpt, duration_ms, created_at FROM ferp_ai_runs
		WHERE entity_id=$1 ORDER BY id DESC LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.EntityID, &r.Model, &r.PromptExcerpt,
			&r.OutputExcerpt, &r.DurationMs, &r.CreatedAt); err != nil {
			if err == pgx.ErrNoRows {
				break
			}
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu   sync.Mutex
	seq  int64
	rows map[int64]Run
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore { return &MemoryStore{rows: map[int64]Run{}} }

// Log appends one run.
func (m *MemoryStore) Log(_ context.Context, _ platform.DBTX, r *Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	r.ID = m.seq
	m.rows[r.ID] = *r
	return nil
}

// List returns runs newest first.
func (m *MemoryStore) List(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Run
	for _, r := range m.rows {
		if r.EntityID == entityID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
