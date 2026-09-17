package partnership

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence contract for the partnership context.
type Store interface {
	CreateProgram(ctx context.Context, db platform.DBTX, p *Program) error
	ProgramByID(ctx context.Context, db platform.DBTX, entityID, id int64) (Program, error)
	ListPrograms(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Program, error)
	CreateReferral(ctx context.Context, db platform.DBTX, r *Referral) error
	ReferralByID(ctx context.Context, db platform.DBTX, entityID, id int64) (Referral, error)
	ReferralByCode(ctx context.Context, db platform.DBTX, entityID int64, code string) (Referral, error)
	ListReferrals(ctx context.Context, db platform.DBTX, entityID, programID int64) ([]Referral, error)
	RecordAccrual(ctx context.Context, db platform.DBTX, a *Accrual) error
	AccrualsOf(ctx context.Context, db platform.DBTX, entityID, referralID int64) ([]Accrual, error)
	ReferredTotal(ctx context.Context, db platform.DBTX, entityID, referralID int64) (int64, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const programCols = `id, entity_id, code, name, tiers, created_at, updated_at, row_version`

func scanProgram(row pgx.Row) (Program, error) {
	var p Program
	var tiers []byte
	err := row.Scan(&p.ID, &p.EntityID, &p.Code, &p.Name, &tiers,
		&p.CreatedAt, &p.UpdatedAt, &p.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Program{}, platform.ErrNotFound
	}
	if err != nil {
		return Program{}, err
	}
	_ = json.Unmarshal(tiers, &p.Tiers)
	if p.Tiers == nil {
		p.Tiers = []Tier{}
	}
	return p, nil
}

func (s *PGStore) CreateProgram(ctx context.Context, db platform.DBTX, p *Program) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	raw, _ := json.Marshal(p.Tiers)
	return db.QueryRow(ctx, `INSERT INTO ferp_partner_programs (entity_id, code, name, tiers)
		VALUES ($1,$2,$3,$4) RETURNING id, row_version`,
		p.EntityID, p.Code, p.Name, raw).Scan(&p.ID, &p.RowVersion)
}

func (s *PGStore) ProgramByID(ctx context.Context, db platform.DBTX, entityID, id int64) (Program, error) {
	return scanProgram(db.QueryRow(ctx, `SELECT `+programCols+` FROM ferp_partner_programs WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) ListPrograms(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Program, error) {
	rows, err := db.Query(ctx, `SELECT `+programCols+` FROM ferp_partner_programs
		WHERE entity_id=$1 ORDER BY code LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Program
	for rows.Next() {
		p, err := scanProgram(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const referralCols = `id, entity_id, program_id, referrer_org_id, referred_org_id, code, status, created_at, updated_at, row_version`

func scanReferral(row pgx.Row) (Referral, error) {
	var r Referral
	err := row.Scan(&r.ID, &r.EntityID, &r.ProgramID, &r.ReferrerOrgID, &r.ReferredOrgID,
		&r.Code, &r.Status, &r.CreatedAt, &r.UpdatedAt, &r.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Referral{}, platform.ErrNotFound
	}
	return r, err
}

func (s *PGStore) CreateReferral(ctx context.Context, db platform.DBTX, r *Referral) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if r.Status == "" {
		r.Status = ReferralActive
	}
	// Program must exist in the same entity (seam guard; org rows stay untouched).
	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM ferp_partner_programs WHERE id=$1 AND entity_id=$2`,
		r.ProgramID, r.EntityID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("partnership: program not found: %w", platform.ErrNotFound)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_referrals
		(entity_id, program_id, referrer_org_id, referred_org_id, code, status)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, row_version`,
		r.EntityID, r.ProgramID, r.ReferrerOrgID, r.ReferredOrgID, r.Code, r.Status,
	).Scan(&r.ID, &r.RowVersion)
}

func (s *PGStore) ReferralByID(ctx context.Context, db platform.DBTX, entityID, id int64) (Referral, error) {
	return scanReferral(db.QueryRow(ctx, `SELECT `+referralCols+` FROM ferp_referrals WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) ReferralByCode(ctx context.Context, db platform.DBTX, entityID int64, code string) (Referral, error) {
	return scanReferral(db.QueryRow(ctx, `SELECT `+referralCols+` FROM ferp_referrals WHERE code=$1 AND entity_id=$2`, code, entityID))
}

func (s *PGStore) ListReferrals(ctx context.Context, db platform.DBTX, entityID, programID int64) ([]Referral, error) {
	rows, err := db.Query(ctx, `SELECT `+referralCols+` FROM ferp_referrals
		WHERE entity_id=$1 AND program_id=$2 ORDER BY id`, entityID, programID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Referral
	for rows.Next() {
		r, err := scanReferral(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const accrualCols = `id, entity_id, referral_id, sale_total, rate_bps, amount, created_at`

func scanAccrual(row pgx.Row) (Accrual, error) {
	var a Accrual
	err := row.Scan(&a.ID, &a.EntityID, &a.ReferralID, &a.SaleTotal, &a.RateBps, &a.Amount, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Accrual{}, platform.ErrNotFound
	}
	return a, err
}

func (s *PGStore) RecordAccrual(ctx context.Context, db platform.DBTX, a *Accrual) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_commission_accruals
		(entity_id, referral_id, sale_total, rate_bps, amount)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		a.EntityID, a.ReferralID, a.SaleTotal, a.RateBps, a.Amount).Scan(&a.ID)
}

func (s *PGStore) AccrualsOf(ctx context.Context, db platform.DBTX, entityID, referralID int64) ([]Accrual, error) {
	rows, err := db.Query(ctx, `SELECT `+accrualCols+` FROM ferp_commission_accruals
		WHERE entity_id=$1 AND referral_id=$2 ORDER BY id`, entityID, referralID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Accrual
	for rows.Next() {
		a, err := scanAccrual(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PGStore) ReferredTotal(ctx context.Context, db platform.DBTX, entityID, referralID int64) (int64, error) {
	var total *int64
	err := db.QueryRow(ctx, `SELECT SUM(sale_total) FROM ferp_commission_accruals
		WHERE entity_id=$1 AND referral_id=$2`, entityID, referralID).Scan(&total)
	if err != nil {
		return 0, err
	}
	if total == nil {
		return 0, nil
	}
	return *total, nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu       sync.Mutex
	seq      int64
	programs map[int64]Program
	refs     map[int64]Referral
	accruals map[int64]Accrual
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{programs: map[int64]Program{}, refs: map[int64]Referral{}, accruals: map[int64]Accrual{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateProgram(_ context.Context, _ platform.DBTX, p *Program) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.programs {
		if e.EntityID == p.EntityID && e.Code == p.Code {
			return fmt.Errorf("partnership: duplicate program code: %w", platform.ErrConflict)
		}
	}
	p.ID = m.next()
	p.RowVersion = 1
	m.programs[p.ID] = *p
	return nil
}

func (m *MemoryStore) ProgramByID(_ context.Context, _ platform.DBTX, entityID, id int64) (Program, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.programs[id]
	if !ok || p.EntityID != entityID {
		return Program{}, platform.ErrNotFound
	}
	return p, nil
}

func (m *MemoryStore) ListPrograms(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]Program, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Program
	for _, p := range m.programs {
		if p.EntityID == entityID {
			out = append(out, p)
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

func (m *MemoryStore) CreateReferral(_ context.Context, _ platform.DBTX, r *Referral) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for _, p := range m.programs {
		if p.ID == r.ProgramID && p.EntityID == r.EntityID {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("partnership: program not found: %w", platform.ErrNotFound)
	}
	for _, e := range m.refs {
		if e.EntityID == r.EntityID && e.Code != "" && e.Code == r.Code {
			return fmt.Errorf("partnership: duplicate referral code: %w", platform.ErrConflict)
		}
	}
	if r.Status == "" {
		r.Status = ReferralActive
	}
	r.ID = m.next()
	r.RowVersion = 1
	m.refs[r.ID] = *r
	return nil
}

func (m *MemoryStore) ReferralByID(_ context.Context, _ platform.DBTX, entityID, id int64) (Referral, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.refs[id]
	if !ok || r.EntityID != entityID {
		return Referral{}, platform.ErrNotFound
	}
	return r, nil
}

func (m *MemoryStore) ReferralByCode(_ context.Context, _ platform.DBTX, entityID int64, code string) (Referral, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.refs {
		if r.EntityID == entityID && r.Code == code {
			return r, nil
		}
	}
	return Referral{}, platform.ErrNotFound
}

func (m *MemoryStore) ListReferrals(_ context.Context, _ platform.DBTX, entityID, programID int64) ([]Referral, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Referral
	for _, r := range m.refs {
		if r.EntityID == entityID && r.ProgramID == programID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *MemoryStore) RecordAccrual(_ context.Context, _ platform.DBTX, a *Accrual) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.refs[a.ReferralID]; !ok {
		return fmt.Errorf("partnership: referral not found: %w", platform.ErrNotFound)
	}
	a.ID = m.next()
	m.accruals[a.ID] = *a
	return nil
}

func (m *MemoryStore) AccrualsOf(_ context.Context, _ platform.DBTX, entityID, referralID int64) ([]Accrual, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Accrual
	for _, a := range m.accruals {
		if a.EntityID == entityID && a.ReferralID == referralID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *MemoryStore) ReferredTotal(_ context.Context, _ platform.DBTX, entityID, referralID int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var total int64
	for _, a := range m.accruals {
		if a.EntityID == entityID && a.ReferralID == referralID {
			total += a.SaleTotal
		}
	}
	return total, nil
}
