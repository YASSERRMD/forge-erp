package dynprice

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence contract for price rules.
type Store interface {
	CreateRule(ctx context.Context, db platform.DBTX, r *Rule) error
	RuleByID(ctx context.Context, db platform.DBTX, entityID, id int64) (Rule, error)
	RuleByCode(ctx context.Context, db platform.DBTX, entityID int64, code string) (Rule, error)
	ListRules(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Rule, error)
	UpdateRule(ctx context.Context, db platform.DBTX, r *Rule) error
	DeleteRule(ctx context.Context, db platform.DBTX, entityID, id int64) error
	Assign(ctx context.Context, db platform.DBTX, a *Assignment) error
	Unassign(ctx context.Context, db platform.DBTX, entityID, id int64) error
	AssignmentsFor(ctx context.Context, db platform.DBTX, entityID, productID, orgID int64) ([]Assignment, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const ruleCols = `id, entity_id, code, label, expression, status, row_version`

func scanRule(row pgx.Row) (Rule, error) {
	var r Rule
	err := row.Scan(&r.ID, &r.EntityID, &r.Code, &r.Label, &r.Expression, &r.Status, &r.RowVersion)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Rule{}, platform.ErrNotFound
		}
		return Rule{}, err
	}
	return r, nil
}

// CreateRule validates (expression must parse) and inserts the rule.
func (s *PGStore) CreateRule(ctx context.Context, db platform.DBTX, r *Rule) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if r.Status != RuleActive && r.Status != RuleArchived {
		r.Status = RuleActive
	}
	err := db.QueryRow(ctx, `INSERT INTO ferp_price_rules
		(entity_id, code, label, expression, status) VALUES ($1,$2,$3,$4,$5)
		RETURNING id, row_version`,
		r.EntityID, r.Code, r.Label, r.Expression, r.Status).Scan(&r.ID, &r.RowVersion)
	return err
}

// RuleByID fetches one rule (404 outside the caller's entity).
func (s *PGStore) RuleByID(ctx context.Context, db platform.DBTX, entityID, id int64) (Rule, error) {
	return scanRule(db.QueryRow(ctx, `SELECT `+ruleCols+` FROM ferp_price_rules
		WHERE id=$1 AND entity_id=$2`, id, entityID))
}

// RuleByCode fetches one rule by code.
func (s *PGStore) RuleByCode(ctx context.Context, db platform.DBTX, entityID int64, code string) (Rule, error) {
	return scanRule(db.QueryRow(ctx, `SELECT `+ruleCols+` FROM ferp_price_rules
		WHERE code=$1 AND entity_id=$2`, code, entityID))
}

// ListRules pages rules within one entity, ordered by code.
func (s *PGStore) ListRules(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Rule, error) {
	rows, err := db.Query(ctx, `SELECT `+ruleCols+` FROM ferp_price_rules
		WHERE entity_id=$1 ORDER BY code LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateRule edits a rule with an optimistic-lock guard.
func (s *PGStore) UpdateRule(ctx context.Context, db platform.DBTX, r *Rule) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	res, err := db.Exec(ctx, `UPDATE ferp_price_rules SET label=$1, expression=$2, status=$3,
		updated_at=now(), row_version=row_version+1
		WHERE id=$4 AND entity_id=$5 AND row_version=$6`,
		r.Label, r.Expression, r.Status, r.ID, r.EntityID, r.RowVersion)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		if _, gerr := s.RuleByID(ctx, db, r.EntityID, r.ID); gerr != nil {
			return gerr
		}
		return fmt.Errorf("dynprice rule %d: %w", r.ID, platform.ErrVersionConflict)
	}
	r.RowVersion++
	return nil
}

// DeleteRule removes a rule (assignments cascade).
func (s *PGStore) DeleteRule(ctx context.Context, db platform.DBTX, entityID, id int64) error {
	res, err := db.Exec(ctx, `DELETE FROM ferp_price_rules WHERE id=$1 AND entity_id=$2`, id, entityID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// Assign binds a rule to a product/org (org 0 = all customers).
func (s *PGStore) Assign(ctx context.Context, db platform.DBTX, a *Assignment) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if _, err := s.RuleByID(ctx, db, a.EntityID, a.RuleID); err != nil {
		return err
	}
	err := db.QueryRow(ctx, `INSERT INTO ferp_price_assignments
		(entity_id, rule_id, product_id, org_id) VALUES ($1,$2,$3,$4) RETURNING id`,
		a.EntityID, a.RuleID, a.ProductID, a.OrgID).Scan(&a.ID)
	return err
}

// Unassign removes one assignment.
func (s *PGStore) Unassign(ctx context.Context, db platform.DBTX, entityID, id int64) error {
	res, err := db.Exec(ctx, `DELETE FROM ferp_price_assignments WHERE id=$1 AND entity_id=$2`, id, entityID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// AssignmentsFor returns assignments for a product: org-specific first,
// then the org-0 fallbacks (caller evaluates in order).
func (s *PGStore) AssignmentsFor(ctx context.Context, db platform.DBTX, entityID, productID, orgID int64) ([]Assignment, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, rule_id, product_id, org_id
		FROM ferp_price_assignments WHERE entity_id=$1 AND product_id=$2 AND (org_id=$3 OR org_id=0)
		ORDER BY org_id DESC, id`, entityID, productID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Assignment
	for rows.Next() {
		var a Assignment
		if err := rows.Scan(&a.ID, &a.EntityID, &a.RuleID, &a.ProductID, &a.OrgID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu     sync.Mutex
	seq    int64
	rules  map[int64]Rule
	assign map[int64]Assignment
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rules: map[int64]Rule{}, assign: map[int64]Assignment{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateRule(_ context.Context, _ platform.DBTX, r *Rule) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.rules {
		if e.EntityID == r.EntityID && e.Code == r.Code {
			return fmt.Errorf("dynprice rule %q: %w", r.Code, platform.ErrConflict)
		}
	}
	r.ID = m.next()
	if r.Status != RuleActive && r.Status != RuleArchived {
		r.Status = RuleActive
	}
	r.RowVersion = 1
	m.rules[r.ID] = *r
	return nil
}

func (m *MemoryStore) RuleByID(_ context.Context, _ platform.DBTX, entityID, id int64) (Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rules[id]
	if !ok || r.EntityID != entityID {
		return Rule{}, fmt.Errorf("dynprice rule %d: %w", id, platform.ErrNotFound)
	}
	return r, nil
}

func (m *MemoryStore) RuleByCode(_ context.Context, _ platform.DBTX, entityID int64, code string) (Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rules {
		if r.EntityID == entityID && r.Code == code {
			return r, nil
		}
	}
	return Rule{}, fmt.Errorf("dynprice rule %q: %w", code, platform.ErrNotFound)
}

func (m *MemoryStore) ListRules(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Rule
	for _, r := range m.rules {
		if r.EntityID == entityID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) UpdateRule(_ context.Context, _ platform.DBTX, r *Rule) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.rules[r.ID]
	if !ok || cur.EntityID != r.EntityID {
		return fmt.Errorf("dynprice rule %d: %w", r.ID, platform.ErrNotFound)
	}
	if cur.RowVersion != r.RowVersion {
		return fmt.Errorf("dynprice rule %d: %w", r.ID, platform.ErrVersionConflict)
	}
	cur.Label = r.Label
	cur.Expression = r.Expression
	cur.Status = r.Status
	cur.RowVersion++
	m.rules[r.ID] = cur
	*r = cur
	return nil
}

func (m *MemoryStore) DeleteRule(_ context.Context, _ platform.DBTX, entityID, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.rules[id]
	if !ok || cur.EntityID != entityID {
		return fmt.Errorf("dynprice rule %d: %w", id, platform.ErrNotFound)
	}
	delete(m.rules, id)
	for aid, a := range m.assign {
		if a.RuleID == id {
			delete(m.assign, aid)
		}
	}
	return nil
}

func (m *MemoryStore) Assign(_ context.Context, _ platform.DBTX, a *Assignment) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rules[a.RuleID]
	if !ok || r.EntityID != a.EntityID {
		return fmt.Errorf("dynprice rule %d: %w", a.RuleID, platform.ErrNotFound)
	}
	for _, e := range m.assign {
		if e.EntityID == a.EntityID && e.ProductID == a.ProductID && e.OrgID == a.OrgID {
			return fmt.Errorf("dynprice assignment: %w", platform.ErrConflict)
		}
	}
	a.ID = m.next()
	m.assign[a.ID] = *a
	return nil
}

func (m *MemoryStore) Unassign(_ context.Context, _ platform.DBTX, entityID, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.assign[id]
	if !ok || a.EntityID != entityID {
		return fmt.Errorf("dynprice assignment %d: %w", id, platform.ErrNotFound)
	}
	delete(m.assign, id)
	return nil
}

func (m *MemoryStore) AssignmentsFor(_ context.Context, _ platform.DBTX, entityID, productID, orgID int64) ([]Assignment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Assignment
	for _, a := range m.assign {
		if a.EntityID == entityID && a.ProductID == productID && (a.OrgID == orgID || a.OrgID == 0) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OrgID != out[j].OrgID {
			return out[i].OrgID > out[j].OrgID
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
