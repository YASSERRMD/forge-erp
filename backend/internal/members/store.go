package members

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence contract for the members context.
type Store interface {
	CreateType(ctx context.Context, db platform.DBTX, t *MemberType) error
	ListTypes(ctx context.Context, db platform.DBTX, entityID int64) ([]MemberType, error)
	CreateMember(ctx context.Context, db platform.DBTX, m *Member) error
	MemberByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Member, error)
	ListMembers(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Member, error)
	SetMemberStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to MemberStatus, rowVersion int64) (Member, error)
	CreateSubscription(ctx context.Context, db platform.DBTX, s *Subscription) error
	SubscriptionByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Subscription, error)
	SetSubscriptionStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to SubscriptionStatus, rowVersion int64) (Subscription, error)
	SubscriptionsOf(ctx context.Context, db platform.DBTX, entityID int64, memberID int64) ([]Subscription, error)
	CreateDonation(ctx context.Context, db platform.DBTX, d *Donation) error
	DonationByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Donation, error)
	SetDonationStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to DonationStatus, rowVersion int64) (Donation, error)
	ListDonations(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Donation, error)
	// CreateMemberLoan validates terms, generates the French-amortisation
	// schedule, and persists header + lines atomically.
	CreateMemberLoan(ctx context.Context, db platform.DBTX, l *MemberLoan) error
	MemberLoanByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (MemberLoan, error)
	// MemberLoanSchedule lists an installment schedule in seq order.
	MemberLoanSchedule(ctx context.Context, db platform.DBTX, entityID int64, loanID int64) ([]MemberLoanLine, error)
	SetMemberLoanStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to MemberLoanStatus, rowVersion int64) (MemberLoan, error)
	// MarkLoanLinePaid flags one installment paid (idempotent guard: paid
	// lines conflict).
	MarkLoanLinePaid(ctx context.Context, db platform.DBTX, entityID int64, loanID int64, seq int, at time.Time) (MemberLoanLine, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (s *PGStore) CreateType(ctx context.Context, db platform.DBTX, t *MemberType) error {
	if err := t.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_member_types
		(entity_id, code, label, annual_fee) VALUES ($1,$2,$3,$4) RETURNING id`,
		t.EntityID, t.Code, t.Label, t.AnnualFee).Scan(&t.ID)
}

func (s *PGStore) ListTypes(ctx context.Context, db platform.DBTX, entityID int64) ([]MemberType, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, code, label, annual_fee, created_at
		FROM ferp_member_types WHERE entity_id=$1 ORDER BY code`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemberType
	for rows.Next() {
		var t MemberType
		if err := rows.Scan(&t.ID, &t.EntityID, &t.Code, &t.Label, &t.AnnualFee, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const memberCols = `id, entity_id, ref, type_id, first_name, last_name, company, email, status, created_at, updated_at, row_version`

func scanMember(row pgx.Row) (Member, error) {
	var m Member
	err := row.Scan(&m.ID, &m.EntityID, &m.Ref, &m.TypeID, &m.FirstName, &m.LastName,
		&m.Company, &m.Email, &m.Status, &m.CreatedAt, &m.UpdatedAt, &m.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, identity.ErrNotFound
	}
	return m, err
}

func (s *PGStore) CreateMember(ctx context.Context, db platform.DBTX, m *Member) error {
	if err := m.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	var typeID int64
	if err := db.QueryRow(ctx, `SELECT id FROM ferp_member_types WHERE id=$1 AND entity_id=$2`,
		m.TypeID, m.EntityID).Scan(&typeID); err != nil {
		return fmt.Errorf("members: unknown type: %w", platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_members
		(entity_id, ref, type_id, first_name, last_name, company, email, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, row_version`,
		m.EntityID, m.Ref, m.TypeID, m.FirstName, m.LastName, m.Company, m.Email, m.Status,
	).Scan(&m.ID, &m.RowVersion)
}

func (s *PGStore) MemberByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Member, error) {
	return scanMember(db.QueryRow(ctx, `SELECT `+memberCols+` FROM ferp_members WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) ListMembers(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Member, error) {
	rows, err := db.Query(ctx, `SELECT `+memberCols+` FROM ferp_members
		WHERE entity_id=$1 ORDER BY ref LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *PGStore) SetMemberStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to MemberStatus, rowVersion int64) (Member, error) {
	m, err := s.MemberByID(ctx, db, entityID, id)
	if err != nil {
		return Member{}, err
	}
	if m.RowVersion != rowVersion {
		return Member{}, identity.ErrVersionConflict
	}
	if !m.CanTransition(to) {
		return Member{}, fmt.Errorf("members: illegal transition: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_members SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND entity_id=$4 AND row_version=$3`, to, id, rowVersion, entityID)
	if err != nil {
		return Member{}, err
	}
	if tag.RowsAffected() == 0 {
		return Member{}, identity.ErrVersionConflict
	}
	m.Status = to
	m.RowVersion++
	return m, nil
}

const subCols = `id, entity_id, member_id, year, amount, status, created_at, updated_at, row_version`

func scanSub(row pgx.Row) (Subscription, error) {
	var su Subscription
	err := row.Scan(&su.ID, &su.EntityID, &su.MemberID, &su.Year, &su.Amount,
		&su.Status, &su.CreatedAt, &su.UpdatedAt, &su.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, identity.ErrNotFound
	}
	return su, err
}

func (s *PGStore) CreateSubscription(ctx context.Context, db platform.DBTX, su *Subscription) error {
	if err := su.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if _, err := s.MemberByID(ctx, db, su.EntityID, su.MemberID); err != nil {
		return fmt.Errorf("members: unknown member: %w", platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_subscriptions
		(entity_id, member_id, year, amount, status)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, row_version`,
		su.EntityID, su.MemberID, su.Year, su.Amount, su.Status,
	).Scan(&su.ID, &su.RowVersion)
}

func (s *PGStore) SetSubscriptionStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to SubscriptionStatus, rowVersion int64) (Subscription, error) {
	su, err := scanSub(db.QueryRow(ctx, `SELECT `+subCols+` FROM ferp_subscriptions WHERE id=$1 AND entity_id=$2`, id, entityID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, identity.ErrNotFound
	}
	if err != nil {
		return Subscription{}, err
	}
	if su.RowVersion != rowVersion {
		return Subscription{}, identity.ErrVersionConflict
	}
	if !su.CanTransition(to) {
		return Subscription{}, fmt.Errorf("members: illegal transition: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_subscriptions SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND entity_id=$4 AND row_version=$3`, to, id, rowVersion, entityID)
	if err != nil {
		return Subscription{}, err
	}
	if tag.RowsAffected() == 0 {
		return Subscription{}, identity.ErrVersionConflict
	}
	su.Status = to
	su.RowVersion++
	return su, nil
}

func (s *PGStore) SubscriptionsOf(ctx context.Context, db platform.DBTX, entityID int64, memberID int64) ([]Subscription, error) {
	rows, err := db.Query(ctx, `SELECT `+subCols+` FROM ferp_subscriptions WHERE member_id=$1 AND entity_id=$2 ORDER BY year`, memberID, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		su, err := scanSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, su)
	}
	return out, rows.Err()
}

const donationCols = `id, entity_id, ref, donor_name, org_id, amount, donated_at, method, status, created_at, updated_at, row_version`

func scanDonation(row pgx.Row) (Donation, error) {
	var d Donation
	err := row.Scan(&d.ID, &d.EntityID, &d.Ref, &d.DonorName, &d.OrgID, &d.Amount,
		&d.DonatedAt, &d.Method, &d.Status, &d.CreatedAt, &d.UpdatedAt, &d.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Donation{}, identity.ErrNotFound
	}
	return d, err
}

func (s *PGStore) CreateDonation(ctx context.Context, db platform.DBTX, d *Donation) error {
	if err := d.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_donations
		(entity_id, ref, donor_name, org_id, amount, donated_at, method, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, row_version`,
		d.EntityID, d.Ref, d.DonorName, d.OrgID, d.Amount, d.DonatedAt, d.Method, d.Status,
	).Scan(&d.ID, &d.RowVersion)
}

func (s *PGStore) SetDonationStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to DonationStatus, rowVersion int64) (Donation, error) {
	d, err := scanDonation(db.QueryRow(ctx, `SELECT `+donationCols+` FROM ferp_donations WHERE id=$1 AND entity_id=$2`, id, entityID))
	if err != nil {
		return Donation{}, err
	}
	if d.RowVersion != rowVersion {
		return Donation{}, identity.ErrVersionConflict
	}
	if !d.CanTransition(to) {
		return Donation{}, fmt.Errorf("members: illegal transition: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_donations SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND entity_id=$4 AND row_version=$3`, to, id, rowVersion, entityID)
	if err != nil {
		return Donation{}, err
	}
	if tag.RowsAffected() == 0 {
		return Donation{}, identity.ErrVersionConflict
	}
	d.Status = to
	d.RowVersion++
	return d, nil
}

func (s *PGStore) ListDonations(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Donation, error) {
	rows, err := db.Query(ctx, `SELECT `+donationCols+` FROM ferp_donations
		WHERE entity_id=$1 ORDER BY donated_at LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Donation
	for rows.Next() {
		d, err := scanDonation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu      sync.Mutex
	seq     int64
	types   map[int64]MemberType
	members map[int64]Member
	subs    map[int64]Subscription
	dons    map[int64]Donation
	mloans  map[int64]MemberLoan
	mlines  map[int64][]MemberLoanLine
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		types: map[int64]MemberType{}, members: map[int64]Member{},
		subs: map[int64]Subscription{}, dons: map[int64]Donation{},
		mloans: map[int64]MemberLoan{}, mlines: map[int64][]MemberLoanLine{},
	}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateType(_ context.Context, _ platform.DBTX, t *MemberType) error {
	if err := t.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.types {
		if e.EntityID == t.EntityID && e.Code == t.Code {
			return fmt.Errorf("members: duplicate type code: %w", platform.ErrConflict)
		}
	}
	t.ID = m.next()
	m.types[t.ID] = *t
	return nil
}

func (m *MemoryStore) ListTypes(_ context.Context, _ platform.DBTX, entityID int64) ([]MemberType, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MemberType
	for _, t := range m.types {
		if t.EntityID == entityID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *MemoryStore) CreateMember(_ context.Context, _ platform.DBTX, mb *Member) error {
	if err := mb.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ty, ok := m.types[mb.TypeID]
	if !ok || ty.EntityID != mb.EntityID {
		return fmt.Errorf("members: unknown type: %w", platform.ErrValidation)
	}
	for _, e := range m.members {
		if e.EntityID == mb.EntityID && e.Ref == mb.Ref {
			return fmt.Errorf("members: duplicate ref: %w", platform.ErrConflict)
		}
	}
	mb.ID = m.next()
	mb.RowVersion = 1
	m.members[mb.ID] = *mb
	return nil
}

func (m *MemoryStore) MemberByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mb, ok := m.members[id]
	if !ok || mb.EntityID != entityID {
		return Member{}, identity.ErrNotFound
	}
	return mb, nil
}

func (m *MemoryStore) ListMembers(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Member
	for _, mb := range m.members {
		if mb.EntityID == entityID {
			out = append(out, mb)
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

func (m *MemoryStore) SetMemberStatus(_ context.Context, _ platform.DBTX, entityID int64, id int64, to MemberStatus, rowVersion int64) (Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mb, ok := m.members[id]
	if !ok || mb.EntityID != entityID {
		return Member{}, identity.ErrNotFound
	}
	if mb.RowVersion != rowVersion {
		return Member{}, identity.ErrVersionConflict
	}
	if !mb.CanTransition(to) {
		return Member{}, fmt.Errorf("members: illegal transition: %w", platform.ErrValidation)
	}
	mb.Status = to
	mb.RowVersion++
	m.members[id] = mb
	return mb, nil
}

func (m *MemoryStore) CreateSubscription(_ context.Context, _ platform.DBTX, s *Subscription) error {
	if err := s.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	mb, ok := m.members[s.MemberID]
	if !ok || mb.EntityID != s.EntityID {
		return fmt.Errorf("members: unknown member: %w", platform.ErrValidation)
	}
	for _, e := range m.subs {
		if e.EntityID == s.EntityID && e.MemberID == s.MemberID && e.Year == s.Year {
			return fmt.Errorf("members: duplicate subscription year: %w", platform.ErrConflict)
		}
	}
	s.ID = m.next()
	s.RowVersion = 1
	m.subs[s.ID] = *s
	return nil
}

func (m *MemoryStore) SetSubscriptionStatus(_ context.Context, _ platform.DBTX, entityID int64, id int64, to SubscriptionStatus, rowVersion int64) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok || s.EntityID != entityID {
		return Subscription{}, identity.ErrNotFound
	}
	if s.RowVersion != rowVersion {
		return Subscription{}, identity.ErrVersionConflict
	}
	if !s.CanTransition(to) {
		return Subscription{}, fmt.Errorf("members: illegal transition: %w", platform.ErrValidation)
	}
	s.Status = to
	s.RowVersion++
	m.subs[id] = s
	return s, nil
}

func (m *MemoryStore) SubscriptionsOf(_ context.Context, _ platform.DBTX, entityID int64, memberID int64) ([]Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Subscription
	for _, s := range m.subs {
		if s.MemberID == memberID && s.EntityID == entityID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *MemoryStore) CreateDonation(_ context.Context, _ platform.DBTX, d *Donation) error {
	if err := d.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.dons {
		if e.EntityID == d.EntityID && e.Ref == d.Ref {
			return fmt.Errorf("members: duplicate donation ref: %w", platform.ErrConflict)
		}
	}
	d.ID = m.next()
	d.RowVersion = 1
	m.dons[d.ID] = *d
	return nil
}

func (m *MemoryStore) SetDonationStatus(_ context.Context, _ platform.DBTX, entityID int64, id int64, to DonationStatus, rowVersion int64) (Donation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.dons[id]
	if !ok || d.EntityID != entityID {
		return Donation{}, identity.ErrNotFound
	}
	if d.RowVersion != rowVersion {
		return Donation{}, identity.ErrVersionConflict
	}
	if !d.CanTransition(to) {
		return Donation{}, fmt.Errorf("members: illegal transition: %w", platform.ErrValidation)
	}
	d.Status = to
	d.RowVersion++
	m.dons[id] = d
	return d, nil
}

func (m *MemoryStore) ListDonations(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]Donation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Donation
	for _, d := range m.dons {
		if d.EntityID == entityID {
			out = append(out, d)
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
