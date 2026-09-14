package booking

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

// Store is the persistence contract for the booking context.
type Store interface {
	CreateResource(ctx context.Context, db platform.DBTX, r *Resource) error
	ResourceByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Resource, error)
	ListResources(ctx context.Context, db platform.DBTX, entityID int64) ([]Resource, error)
	CreateBooking(ctx context.Context, db platform.DBTX, b *Booking) error
	BookingByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Booking, error)
	BookingsOf(ctx context.Context, db platform.DBTX, entityID int64, resourceID int64, from, to time.Time) ([]Booking, error)
	SetBookingStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to BookingStatus, rowVersion int64) (Booking, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const resourceCols = `id, entity_id, code, label, capacity, status, created_at, updated_at, row_version`

func scanResource(row pgx.Row) (Resource, error) {
	var r Resource
	err := row.Scan(&r.ID, &r.EntityID, &r.Code, &r.Label, &r.Capacity,
		&r.Status, &r.CreatedAt, &r.UpdatedAt, &r.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resource{}, identity.ErrNotFound
	}
	return r, err
}

func (s *PGStore) CreateResource(ctx context.Context, db platform.DBTX, r *Resource) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_resources
		(entity_id, code, label, capacity, status)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, row_version`,
		r.EntityID, r.Code, r.Label, r.Capacity, r.Status,
	).Scan(&r.ID, &r.RowVersion)
}

func (s *PGStore) ResourceByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Resource, error) {
	return scanResource(db.QueryRow(ctx, `SELECT `+resourceCols+` FROM ferp_resources WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) ListResources(ctx context.Context, db platform.DBTX, entityID int64) ([]Resource, error) {
	rows, err := db.Query(ctx, `SELECT `+resourceCols+` FROM ferp_resources WHERE entity_id=$1 ORDER BY code`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Resource
	for rows.Next() {
		r, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const bookingCols = `id, entity_id, resource_id, org_id, user_login, start_at, end_at, seats, status, created_at, updated_at, row_version`

func scanBooking(row pgx.Row) (Booking, error) {
	var b Booking
	err := row.Scan(&b.ID, &b.EntityID, &b.ResourceID, &b.OrgID, &b.UserLogin,
		&b.StartAt, &b.EndAt, &b.Seats, &b.Status, &b.CreatedAt, &b.UpdatedAt, &b.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, identity.ErrNotFound
	}
	return b, err
}

// overlapping returns live bookings intersecting the window.
func (s *PGStore) overlapping(ctx context.Context, db platform.DBTX, entityID int64, resourceID int64, start, end time.Time) ([]Booking, error) {
	rows, err := db.Query(ctx, `SELECT `+bookingCols+` FROM ferp_bookings
		WHERE resource_id=$1 AND entity_id=$2 AND status IN (0,1) AND start_at < $4 AND end_at > $3
		ORDER BY start_at`, resourceID, entityID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Booking
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *PGStore) CreateBooking(ctx context.Context, db platform.DBTX, b *Booking) error {
	if err := b.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	res, err := s.ResourceByID(ctx, db, b.EntityID, b.ResourceID)
	if err != nil {
		return err
	}
	if res.Status != ResourceActive {
		return fmt.Errorf("booking: resource inactive: %w", platform.ErrValidation)
	}
	live, err := s.overlapping(ctx, db, b.EntityID, b.ResourceID, b.StartAt, b.EndAt)
	if err != nil {
		return err
	}
	if err := FitsCapacity(res.Capacity, live, *b); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_bookings
		(entity_id, resource_id, org_id, user_login, start_at, end_at, seats, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, row_version`,
		b.EntityID, b.ResourceID, b.OrgID, b.UserLogin, b.StartAt, b.EndAt, b.Seats, b.Status,
	).Scan(&b.ID, &b.RowVersion)
}

func (s *PGStore) BookingByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Booking, error) {
	return scanBooking(db.QueryRow(ctx, `SELECT `+bookingCols+` FROM ferp_bookings WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) BookingsOf(ctx context.Context, db platform.DBTX, entityID int64, resourceID int64, from, to time.Time) ([]Booking, error) {
	rows, err := db.Query(ctx, `SELECT `+bookingCols+` FROM ferp_bookings
		WHERE resource_id=$1 AND entity_id=$2 AND start_at < $4 AND end_at > $3 ORDER BY start_at`, resourceID, entityID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Booking
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *PGStore) SetBookingStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to BookingStatus, rowVersion int64) (Booking, error) {
	b, err := s.BookingByID(ctx, db, entityID, id)
	if err != nil {
		return Booking{}, err
	}
	if b.RowVersion != rowVersion {
		return Booking{}, identity.ErrVersionConflict
	}
	if !b.CanTransition(to) {
		return Booking{}, fmt.Errorf("booking: illegal transition: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_bookings SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND entity_id=$4 AND row_version=$3`, to, id, rowVersion, entityID)
	if err != nil {
		return Booking{}, err
	}
	if tag.RowsAffected() == 0 {
		return Booking{}, identity.ErrVersionConflict
	}
	b.Status = to
	b.RowVersion++
	return b, nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu        sync.Mutex
	seq       int64
	resources map[int64]Resource
	bookings  map[int64]Booking
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{resources: map[int64]Resource{}, bookings: map[int64]Booking{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateResource(_ context.Context, _ platform.DBTX, r *Resource) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.resources {
		if e.EntityID == r.EntityID && e.Code == r.Code {
			return fmt.Errorf("booking: duplicate resource code: %w", platform.ErrConflict)
		}
	}
	r.ID = m.next()
	r.RowVersion = 1
	m.resources[r.ID] = *r
	return nil
}

func (m *MemoryStore) ResourceByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.resources[id]
	if !ok || r.EntityID != entityID {
		return Resource{}, identity.ErrNotFound
	}
	return r, nil
}

func (m *MemoryStore) ListResources(_ context.Context, _ platform.DBTX, entityID int64) ([]Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Resource
	for _, r := range m.resources {
		if r.EntityID == entityID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *MemoryStore) CreateBooking(_ context.Context, _ platform.DBTX, b *Booking) error {
	if err := b.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	res, ok := m.resources[b.ResourceID]
	if !ok || res.EntityID != b.EntityID {
		return fmt.Errorf("booking: resource not found: %w", platform.ErrNotFound)
	}
	if res.Status != ResourceActive {
		return fmt.Errorf("booking: resource inactive: %w", platform.ErrValidation)
	}
	var live []Booking
	for _, o := range m.bookings {
		if o.ResourceID == b.ResourceID && o.EntityID == b.EntityID {
			live = append(live, o)
		}
	}
	if err := FitsCapacity(res.Capacity, live, *b); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	b.ID = m.next()
	b.RowVersion = 1
	m.bookings[b.ID] = *b
	return nil
}

func (m *MemoryStore) BookingByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (Booking, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bookings[id]
	if !ok || b.EntityID != entityID {
		return Booking{}, identity.ErrNotFound
	}
	return b, nil
}

func (m *MemoryStore) BookingsOf(_ context.Context, _ platform.DBTX, entityID int64, resourceID int64, from, to time.Time) ([]Booking, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Booking
	for _, b := range m.bookings {
		if b.EntityID == entityID && b.ResourceID == resourceID && b.StartAt.Before(to) && from.Before(b.EndAt) {
			out = append(out, b)
		}
	}
	return out, nil
}

func (m *MemoryStore) SetBookingStatus(_ context.Context, _ platform.DBTX, entityID int64, id int64, to BookingStatus, rowVersion int64) (Booking, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bookings[id]
	if !ok || b.EntityID != entityID {
		return Booking{}, identity.ErrNotFound
	}
	if b.RowVersion != rowVersion {
		return Booking{}, identity.ErrVersionConflict
	}
	if !b.CanTransition(to) {
		return Booking{}, fmt.Errorf("booking: illegal transition: %w", platform.ErrValidation)
	}
	b.Status = to
	b.RowVersion++
	m.bookings[id] = b
	return b, nil
}
