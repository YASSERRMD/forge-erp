package datapolicy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence contract for the datapolicy context.
type Store interface {
	UpsertRule(ctx context.Context, db platform.DBTX, r *RetentionRule) error
	RulesOf(ctx context.Context, db platform.DBTX, entityID int64) ([]RetentionRule, error)
	RuleByScope(ctx context.Context, db platform.DBTX, entityID int64, scope string) (RetentionRule, error)
	LogErasure(ctx context.Context, db platform.DBTX, e *ErasureRequest) error
	ListErasures(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]ErasureRequest, error)
	MarkErasureDone(ctx context.Context, db platform.DBTX, entityID, id int64) (ErasureRequest, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (s *PGStore) UpsertRule(ctx context.Context, db platform.DBTX, r *RetentionRule) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_retention_rules (entity_id, scope, retain_days, action)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (entity_id, scope) DO UPDATE SET retain_days=EXCLUDED.retain_days,
			action=EXCLUDED.action, updated_at=now()
		RETURNING entity_id`,
		r.EntityID, r.Scope, r.RetainDays, r.Action).Scan(&r.EntityID)
}

func (s *PGStore) RulesOf(ctx context.Context, db platform.DBTX, entityID int64) ([]RetentionRule, error) {
	rows, err := db.Query(ctx, `SELECT entity_id, scope, retain_days, action
		FROM ferp_retention_rules WHERE entity_id=$1 ORDER BY scope`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RetentionRule
	for rows.Next() {
		var r RetentionRule
		if err := rows.Scan(&r.EntityID, &r.Scope, &r.RetainDays, &r.Action); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PGStore) RuleByScope(ctx context.Context, db platform.DBTX, entityID int64, scope string) (RetentionRule, error) {
	var r RetentionRule
	err := db.QueryRow(ctx, `SELECT entity_id, scope, retain_days, action
		FROM ferp_retention_rules WHERE entity_id=$1 AND scope=$2`, entityID, scope).
		Scan(&r.EntityID, &r.Scope, &r.RetainDays, &r.Action)
	if errors.Is(err, pgx.ErrNoRows) {
		return RetentionRule{}, platform.ErrNotFound
	}
	return r, err
}

const erasureCols = `id, entity_id, scope, subject_id, reason, status, created_at`

func scanErasure(row pgx.Row) (ErasureRequest, error) {
	var e ErasureRequest
	err := row.Scan(&e.ID, &e.EntityID, &e.Scope, &e.SubjectID, &e.Reason, &e.Status, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErasureRequest{}, platform.ErrNotFound
	}
	return e, err
}

func (s *PGStore) LogErasure(ctx context.Context, db platform.DBTX, e *ErasureRequest) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if e.Status == "" {
		e.Status = ErasurePending
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_erasure_requests (entity_id, scope, subject_id, reason, status)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`,
		e.EntityID, e.Scope, e.SubjectID, e.Reason, e.Status).Scan(&e.ID, &e.CreatedAt)
}

func (s *PGStore) ListErasures(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]ErasureRequest, error) {
	rows, err := db.Query(ctx, `SELECT `+erasureCols+` FROM ferp_erasure_requests
		WHERE entity_id=$1 ORDER BY id LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ErasureRequest
	for rows.Next() {
		e, err := scanErasure(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PGStore) MarkErasureDone(ctx context.Context, db platform.DBTX, entityID, id int64) (ErasureRequest, error) {
	var e ErasureRequest
	err := db.QueryRow(ctx, `UPDATE ferp_erasure_requests SET status='done', updated_at=now()
		WHERE id=$1 AND entity_id=$2 AND status='pending'
		RETURNING `+erasureCols, id, entityID).
		Scan(&e.ID, &e.EntityID, &e.Scope, &e.SubjectID, &e.Reason, &e.Status, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErasureRequest{}, platform.ErrNotFound
	}
	return e, err
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu       sync.Mutex
	seq      int64
	rules    map[int64]map[string]RetentionRule // entity -> scope -> rule
	erasures map[int64]ErasureRequest
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rules: map[int64]map[string]RetentionRule{}, erasures: map[int64]ErasureRequest{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) UpsertRule(_ context.Context, _ platform.DBTX, r *RetentionRule) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rules[r.EntityID] == nil {
		m.rules[r.EntityID] = map[string]RetentionRule{}
	}
	r.ID = m.next()
	m.rules[r.EntityID][r.Scope] = *r
	return nil
}

func (m *MemoryStore) RulesOf(_ context.Context, _ platform.DBTX, entityID int64) ([]RetentionRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RetentionRule
	for _, r := range m.rules[entityID] {
		out = append(out, r)
	}
	return out, nil
}

func (m *MemoryStore) RuleByScope(_ context.Context, _ platform.DBTX, entityID int64, scope string) (RetentionRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rules[entityID][scope]
	if !ok {
		return RetentionRule{}, platform.ErrNotFound
	}
	return r, nil
}

func (m *MemoryStore) LogErasure(_ context.Context, _ platform.DBTX, e *ErasureRequest) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.Status == "" {
		e.Status = ErasurePending
	}
	e.ID = m.next()
	e.CreatedAt = time.Now().UTC()
	m.erasures[e.ID] = *e
	return nil
}

func (m *MemoryStore) ListErasures(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]ErasureRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ErasureRequest
	for _, e := range m.erasures {
		if e.EntityID == entityID {
			out = append(out, e)
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

func (m *MemoryStore) MarkErasureDone(_ context.Context, _ platform.DBTX, entityID, id int64) (ErasureRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.erasures[id]
	if !ok || e.EntityID != entityID || e.Status != ErasurePending {
		return ErasureRequest{}, platform.ErrNotFound
	}
	e.Status = ErasureDone
	m.erasures[id] = e
	return e, nil
}
