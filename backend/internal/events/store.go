package events

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Store is the persistence contract for events + hiring.
type Store interface {
	CreateEvent(ctx context.Context, db platform.DBTX, e *OrgEvent) error
	EventByID(ctx context.Context, db platform.DBTX, entityID, id int64) (OrgEvent, error)
	ListEvents(ctx context.Context, db platform.DBTX, entityID int64) ([]OrgEvent, error)
	SetEventStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to OrgEventStatus, rowVersion int64) (OrgEvent, error)
	Register(ctx context.Context, db platform.DBTX, r *Registration) error
	RegistrationsOf(ctx context.Context, db platform.DBTX, eventID int64) ([]Registration, error)
	SetRegistrationStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to RegistrationStatus) (Registration, error)
	CreatePosition(ctx context.Context, db platform.DBTX, p *Position) error
	ListPositions(ctx context.Context, db platform.DBTX, entityID int64) ([]Position, error)
	SetPositionStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to PositionStatus, rowVersion int64) (Position, error)
	Apply(ctx context.Context, db platform.DBTX, a *Application) error
	ApplicationsOf(ctx context.Context, db platform.DBTX, positionID int64) ([]Application, error)
	SetApplicationStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to ApplicationStatus, rowVersion int64) (Application, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const eventCols = `id, entity_id, title, description, location, starts_at, ends_at, capacity, price, status, created_at, updated_at, row_version`

func scanEvent(row pgx.Row) (OrgEvent, error) {
	var e OrgEvent
	err := row.Scan(&e.ID, &e.EntityID, &e.Title, &e.Description, &e.Location,
		&e.StartsAt, &e.EndsAt, &e.Capacity, &e.Price, &e.Status,
		&e.CreatedAt, &e.UpdatedAt, &e.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrgEvent{}, identity.ErrNotFound
	}
	return e, err
}

func (s *PGStore) CreateEvent(ctx context.Context, db platform.DBTX, e *OrgEvent) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_org_events
		(entity_id, title, description, location, starts_at, ends_at, capacity, price, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id, row_version`,
		e.EntityID, e.Title, e.Description, e.Location, e.StartsAt, e.EndsAt,
		e.Capacity, e.Price, e.Status,
	).Scan(&e.ID, &e.RowVersion)
}

func (s *PGStore) EventByID(ctx context.Context, db platform.DBTX, entityID, id int64) (OrgEvent, error) {
	return scanEvent(db.QueryRow(ctx, `SELECT `+eventCols+` FROM ferp_org_events WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) ListEvents(ctx context.Context, db platform.DBTX, entityID int64) ([]OrgEvent, error) {
	rows, err := db.Query(ctx, `SELECT `+eventCols+` FROM ferp_org_events WHERE entity_id=$1 ORDER BY starts_at`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrgEvent
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PGStore) SetEventStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to OrgEventStatus, rowVersion int64) (OrgEvent, error) {
	e, err := s.EventByID(ctx, db, entityID, id)
	if err != nil {
		return OrgEvent{}, err
	}
	if e.RowVersion != rowVersion {
		return OrgEvent{}, identity.ErrVersionConflict
	}
	if !e.CanTransition(to) {
		return OrgEvent{}, fmt.Errorf("events: illegal transition: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_org_events SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND entity_id=$3 AND row_version=$4`, to, id, entityID, rowVersion)
	if err != nil {
		return OrgEvent{}, err
	}
	if tag.RowsAffected() == 0 {
		return OrgEvent{}, identity.ErrVersionConflict
	}
	e.Status = to
	e.RowVersion++
	return e, nil
}

const regCols = `id, entity_id, event_id, name, email, org_id, status, created_at`

func scanReg(row pgx.Row) (Registration, error) {
	var r Registration
	err := row.Scan(&r.ID, &r.EntityID, &r.EventID, &r.Name, &r.Email, &r.OrgID, &r.Status, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Registration{}, identity.ErrNotFound
	}
	return r, err
}

func (s *PGStore) Register(ctx context.Context, db platform.DBTX, r *Registration) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	e, err := s.EventByID(ctx, db, r.EntityID, r.EventID)
	if err != nil {
		return err
	}
	if e.Status != OrgEventPublished {
		return fmt.Errorf("events: registration open on published events only: %w", platform.ErrValidation)
	}
	regs, err := s.RegistrationsOf(ctx, db, r.EventID)
	if err != nil {
		return err
	}
	if err := CheckCapacity(e.Capacity, regs); err != nil {
		return err
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_registrations
		(entity_id, event_id, name, email, org_id, status)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		r.EntityID, r.EventID, r.Name, r.Email, r.OrgID, r.Status,
	).Scan(&r.ID)
}

func (s *PGStore) RegistrationsOf(ctx context.Context, db platform.DBTX, eventID int64) ([]Registration, error) {
	rows, err := db.Query(ctx, `SELECT `+regCols+` FROM ferp_registrations WHERE event_id=$1 ORDER BY id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Registration
	for rows.Next() {
		r, err := scanReg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PGStore) SetRegistrationStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to RegistrationStatus) (Registration, error) {
	var cur Registration
	err := db.QueryRow(ctx, `SELECT `+regCols+` FROM ferp_registrations WHERE id=$1 AND entity_id=$2`, id, entityID).Scan(
		&cur.ID, &cur.EntityID, &cur.EventID, &cur.Name, &cur.Email, &cur.OrgID, &cur.Status, &cur.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Registration{}, identity.ErrNotFound
	}
	if err != nil {
		return Registration{}, err
	}
	if !cur.CanTransition(to) {
		return Registration{}, fmt.Errorf("events: illegal transition: %w", platform.ErrValidation)
	}
	_, err = db.Exec(ctx, `UPDATE ferp_registrations SET status=$1 WHERE id=$2 AND entity_id=$3`, to, id, entityID)
	if err != nil {
		return Registration{}, err
	}
	cur.Status = to
	return cur, nil
}

const posCols = `id, entity_id, code, title, description, status, created_at, updated_at, row_version`

func scanPosition(row pgx.Row) (Position, error) {
	var p Position
	err := row.Scan(&p.ID, &p.EntityID, &p.Code, &p.Title, &p.Description,
		&p.Status, &p.CreatedAt, &p.UpdatedAt, &p.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Position{}, identity.ErrNotFound
	}
	return p, err
}

func (s *PGStore) CreatePosition(ctx context.Context, db platform.DBTX, p *Position) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_positions
		(entity_id, code, title, description, status)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, row_version`,
		p.EntityID, p.Code, p.Title, p.Description, p.Status,
	).Scan(&p.ID, &p.RowVersion)
}

func (s *PGStore) ListPositions(ctx context.Context, db platform.DBTX, entityID int64) ([]Position, error) {
	rows, err := db.Query(ctx, `SELECT `+posCols+` FROM ferp_positions WHERE entity_id=$1 ORDER BY code`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Position
	for rows.Next() {
		p, err := scanPosition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PGStore) SetPositionStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to PositionStatus, rowVersion int64) (Position, error) {
	p, err := scanPosition(db.QueryRow(ctx, `SELECT `+posCols+` FROM ferp_positions WHERE id=$1 AND entity_id=$2`, id, entityID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Position{}, identity.ErrNotFound
	}
	if err != nil {
		return Position{}, err
	}
	if p.RowVersion != rowVersion {
		return Position{}, identity.ErrVersionConflict
	}
	if !p.CanTransition(to) {
		return Position{}, fmt.Errorf("events: illegal transition: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_positions SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND entity_id=$3 AND row_version=$4`, to, id, entityID, rowVersion)
	if err != nil {
		return Position{}, err
	}
	if tag.RowsAffected() == 0 {
		return Position{}, identity.ErrVersionConflict
	}
	p.Status = to
	p.RowVersion++
	return p, nil
}

const appCols = `id, entity_id, position_id, name, email, status, created_at, updated_at, row_version`

func scanApp(row pgx.Row) (Application, error) {
	var a Application
	err := row.Scan(&a.ID, &a.EntityID, &a.PositionID, &a.Name, &a.Email,
		&a.Status, &a.CreatedAt, &a.UpdatedAt, &a.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Application{}, identity.ErrNotFound
	}
	return a, err
}

func (s *PGStore) Apply(ctx context.Context, db platform.DBTX, a *Application) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	p, err := scanPosition(db.QueryRow(ctx, `SELECT `+posCols+` FROM ferp_positions WHERE id=$1 AND entity_id=$2`, a.PositionID, a.EntityID))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	if err != nil {
		return err
	}
	if p.Status != PositionOpen {
		return fmt.Errorf("events: applications open on open positions only: %w", platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_applications
		(entity_id, position_id, name, email, status)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, row_version`,
		a.EntityID, a.PositionID, a.Name, a.Email, a.Status,
	).Scan(&a.ID, &a.RowVersion)
}

func (s *PGStore) ApplicationsOf(ctx context.Context, db platform.DBTX, positionID int64) ([]Application, error) {
	rows, err := db.Query(ctx, `SELECT `+appCols+` FROM ferp_applications WHERE position_id=$1 ORDER BY id`, positionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Application
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PGStore) SetApplicationStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to ApplicationStatus, rowVersion int64) (Application, error) {
	a, err := scanApp(db.QueryRow(ctx, `SELECT `+appCols+` FROM ferp_applications WHERE id=$1 AND entity_id=$2`, id, entityID))
	if err != nil {
		return Application{}, err
	}
	if a.RowVersion != rowVersion {
		return Application{}, identity.ErrVersionConflict
	}
	if !a.CanTransition(to) {
		return Application{}, fmt.Errorf("events: illegal transition: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_applications SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND entity_id=$3 AND row_version=$4`, to, id, entityID, rowVersion)
	if err != nil {
		return Application{}, err
	}
	if tag.RowsAffected() == 0 {
		return Application{}, identity.ErrVersionConflict
	}
	a.Status = to
	a.RowVersion++
	return a, nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu     sync.Mutex
	seq    int64
	events map[int64]OrgEvent
	regs   map[int64]Registration
	poss   map[int64]Position
	apps   map[int64]Application
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		events: map[int64]OrgEvent{}, regs: map[int64]Registration{},
		poss: map[int64]Position{}, apps: map[int64]Application{},
	}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateEvent(_ context.Context, _ platform.DBTX, e *OrgEvent) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e.ID = m.next()
	e.RowVersion = 1
	m.events[e.ID] = *e
	return nil
}

func (m *MemoryStore) EventByID(_ context.Context, _ platform.DBTX, entityID, id int64) (OrgEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.events[id]
	if !ok || e.EntityID != entityID {
		return OrgEvent{}, identity.ErrNotFound
	}
	return e, nil
}

func (m *MemoryStore) ListEvents(_ context.Context, _ platform.DBTX, entityID int64) ([]OrgEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []OrgEvent
	for _, e := range m.events {
		if e.EntityID == entityID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *MemoryStore) SetEventStatus(_ context.Context, _ platform.DBTX, entityID, id int64, to OrgEventStatus, rowVersion int64) (OrgEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.events[id]
	if !ok || e.EntityID != entityID {
		return OrgEvent{}, identity.ErrNotFound
	}
	if e.RowVersion != rowVersion {
		return OrgEvent{}, identity.ErrVersionConflict
	}
	if !e.CanTransition(to) {
		return OrgEvent{}, fmt.Errorf("events: illegal transition: %w", platform.ErrValidation)
	}
	e.Status = to
	e.RowVersion++
	m.events[id] = e
	return e, nil
}

func (m *MemoryStore) Register(_ context.Context, _ platform.DBTX, r *Registration) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.events[r.EventID]
	if !ok || e.EntityID != r.EntityID {
		return fmt.Errorf("events: event not found: %w", platform.ErrNotFound)
	}
	if e.Status != OrgEventPublished {
		return fmt.Errorf("events: registration open on published events only: %w", platform.ErrValidation)
	}
	var regs []Registration
	for _, x := range m.regs {
		if x.EventID == r.EventID {
			regs = append(regs, x)
		}
	}
	if err := CheckCapacity(e.Capacity, regs); err != nil {
		return err
	}
	r.ID = m.next()
	m.regs[r.ID] = *r
	return nil
}

func (m *MemoryStore) RegistrationsOf(_ context.Context, _ platform.DBTX, eventID int64) ([]Registration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Registration
	for _, r := range m.regs {
		if r.EventID == eventID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *MemoryStore) SetRegistrationStatus(_ context.Context, _ platform.DBTX, entityID, id int64, to RegistrationStatus) (Registration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.regs[id]
	if !ok || r.EntityID != entityID {
		return Registration{}, identity.ErrNotFound
	}
	if !r.CanTransition(to) {
		return Registration{}, fmt.Errorf("events: illegal transition: %w", platform.ErrValidation)
	}
	r.Status = to
	m.regs[id] = r
	return r, nil
}

func (m *MemoryStore) CreatePosition(_ context.Context, _ platform.DBTX, p *Position) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.poss {
		if e.EntityID == p.EntityID && e.Code == p.Code {
			return fmt.Errorf("events: duplicate position code: %w", platform.ErrConflict)
		}
	}
	p.ID = m.next()
	p.RowVersion = 1
	m.poss[p.ID] = *p
	return nil
}

func (m *MemoryStore) ListPositions(_ context.Context, _ platform.DBTX, entityID int64) ([]Position, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Position
	for _, p := range m.poss {
		if p.EntityID == entityID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *MemoryStore) SetPositionStatus(_ context.Context, _ platform.DBTX, entityID, id int64, to PositionStatus, rowVersion int64) (Position, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.poss[id]
	if !ok || p.EntityID != entityID {
		return Position{}, identity.ErrNotFound
	}
	if p.RowVersion != rowVersion {
		return Position{}, identity.ErrVersionConflict
	}
	if !p.CanTransition(to) {
		return Position{}, fmt.Errorf("events: illegal transition: %w", platform.ErrValidation)
	}
	p.Status = to
	p.RowVersion++
	m.poss[id] = p
	return p, nil
}

func (m *MemoryStore) Apply(_ context.Context, _ platform.DBTX, a *Application) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.poss[a.PositionID]
	if !ok || p.EntityID != a.EntityID {
		return identity.ErrNotFound
	}
	if p.Status != PositionOpen {
		return fmt.Errorf("events: applications open on open positions only: %w", platform.ErrValidation)
	}
	a.ID = m.next()
	a.RowVersion = 1
	m.apps[a.ID] = *a
	return nil
}

func (m *MemoryStore) ApplicationsOf(_ context.Context, _ platform.DBTX, positionID int64) ([]Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Application
	for _, a := range m.apps {
		if a.PositionID == positionID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *MemoryStore) SetApplicationStatus(_ context.Context, _ platform.DBTX, entityID, id int64, to ApplicationStatus, rowVersion int64) (Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[id]
	if !ok || a.EntityID != entityID {
		return Application{}, identity.ErrNotFound
	}
	if a.RowVersion != rowVersion {
		return Application{}, identity.ErrVersionConflict
	}
	if !a.CanTransition(to) {
		return Application{}, fmt.Errorf("events: illegal transition: %w", platform.ErrValidation)
	}
	a.Status = to
	a.RowVersion++
	m.apps[id] = a
	return a, nil
}
