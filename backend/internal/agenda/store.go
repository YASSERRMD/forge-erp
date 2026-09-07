package agenda

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Store is the persistence contract for the agenda context.
type Store interface {
	CreateEvent(ctx context.Context, e *Event) error
	EventByID(ctx context.Context, id int64) (Event, error)
	ListEvents(ctx context.Context, entityID int64, from, to time.Time, limit, offset int) ([]Event, error)
	SetEventStatus(ctx context.Context, id int64, to EventStatus, rowVersion int64) (Event, error)
	DueReminders(ctx context.Context, entityID int64, now time.Time, limit int) ([]Event, error)
	DueRemindersAll(ctx context.Context, now time.Time, limit int) ([]Event, error)
	MarkReminded(ctx context.Context, id int64) error
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const eventCols = `id, entity_id, title, description, location, start_at, end_at, all_day, owner_login, attendees, org_id, project_id, reminder_min, reminded_at, status, created_at, updated_at, row_version`

func scanEvent(row pgx.Row) (Event, error) {
	var e Event
	var attendees []byte
	err := row.Scan(&e.ID, &e.EntityID, &e.Title, &e.Description, &e.Location,
		&e.StartAt, &e.EndAt, &e.AllDay, &e.OwnerLogin, &attendees, &e.OrgID, &e.ProjectID,
		&e.ReminderMin, &e.RemindedAt, &e.Status, &e.CreatedAt, &e.UpdatedAt, &e.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, identity.ErrNotFound
	}
	if err != nil {
		return Event{}, err
	}
	_ = json.Unmarshal(attendees, &e.Attendees)
	return e, nil
}

func (s *PGStore) CreateEvent(ctx context.Context, e *Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	att, _ := json.Marshal(e.Attendees)
	if att == nil {
		att = []byte("[]")
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_events
		(entity_id, title, description, location, start_at, end_at, all_day, owner_login,
		 attendees, org_id, project_id, reminder_min, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id, row_version`,
		e.EntityID, e.Title, e.Description, e.Location, e.StartAt, e.EndAt, e.AllDay,
		e.OwnerLogin, att, e.OrgID, e.ProjectID, e.ReminderMin, e.Status,
	).Scan(&e.ID, &e.RowVersion)
}

func (s *PGStore) EventByID(ctx context.Context, id int64) (Event, error) {
	return scanEvent(s.pool.QueryRow(ctx, `SELECT `+eventCols+` FROM ferp_events WHERE id=$1`, id))
}

func (s *PGStore) ListEvents(ctx context.Context, entityID int64, from, to time.Time, limit, offset int) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+eventCols+` FROM ferp_events
		WHERE entity_id=$1 AND start_at < $3 AND end_at > $2 ORDER BY start_at LIMIT $4 OFFSET $5`,
		entityID, from, to, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PGStore) SetEventStatus(ctx context.Context, id int64, to EventStatus, rowVersion int64) (Event, error) {
	e, err := s.EventByID(ctx, id)
	if err != nil {
		return Event{}, err
	}
	if e.RowVersion != rowVersion {
		return Event{}, identity.ErrVersionConflict
	}
	if !e.CanTransition(to) {
		return Event{}, errors.New("agenda: illegal transition")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_events SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return Event{}, err
	}
	if tag.RowsAffected() == 0 {
		return Event{}, identity.ErrVersionConflict
	}
	e.Status = to
	e.RowVersion++
	return e, nil
}

// DueReminders returns scheduled events whose reminder window has opened.
func (s *PGStore) DueReminders(ctx context.Context, entityID int64, now time.Time, limit int) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+eventCols+` FROM ferp_events
		WHERE entity_id=$1 AND status=0 AND reminder_min > 0 AND reminded_at IS NULL
		AND start_at - (reminder_min || ' minutes')::interval <= $2
		ORDER BY start_at LIMIT $3`, entityID, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DueRemindersAll returns due reminders across entities (daemon path).
func (s *PGStore) DueRemindersAll(ctx context.Context, now time.Time, limit int) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+eventCols+` FROM ferp_events
		WHERE status=0 AND reminder_min > 0 AND reminded_at IS NULL
		AND start_at - (reminder_min || ' minutes')::interval <= $1
		ORDER BY start_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PGStore) MarkReminded(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_events SET reminded_at=now(), updated_at=now()
		WHERE id=$1 AND reminded_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrVersionConflict
	}
	return nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu     sync.Mutex
	seq    int64
	events map[int64]Event
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{events: map[int64]Event{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateEvent(_ context.Context, e *Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e.ID = m.next()
	e.RowVersion = 1
	m.events[e.ID] = *e
	return nil
}

func (m *MemoryStore) EventByID(_ context.Context, id int64) (Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.events[id]
	if !ok {
		return Event{}, identity.ErrNotFound
	}
	return e, nil
}

func (m *MemoryStore) ListEvents(_ context.Context, entityID int64, from, to time.Time, limit, offset int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Event
	for _, e := range m.events {
		if e.EntityID == entityID && e.StartAt.Before(to) && from.Before(e.EndAt) {
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

func (m *MemoryStore) SetEventStatus(_ context.Context, id int64, to EventStatus, rowVersion int64) (Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.events[id]
	if !ok {
		return Event{}, identity.ErrNotFound
	}
	if e.RowVersion != rowVersion {
		return Event{}, identity.ErrVersionConflict
	}
	if !e.CanTransition(to) {
		return Event{}, errors.New("agenda: illegal transition")
	}
	e.Status = to
	e.RowVersion++
	m.events[id] = e
	return e, nil
}

func (m *MemoryStore) DueReminders(_ context.Context, entityID int64, now time.Time, _ int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Event
	for _, e := range m.events {
		if e.EntityID == entityID && e.ReminderDue(now) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *MemoryStore) DueRemindersAll(_ context.Context, now time.Time, _ int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Event
	for _, e := range m.events {
		if e.ReminderDue(now) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *MemoryStore) MarkReminded(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.events[id]
	if !ok {
		return identity.ErrNotFound
	}
	if e.RemindedAt != nil {
		return identity.ErrVersionConflict
	}
	now := time.Now().UTC()
	e.RemindedAt = &now
	m.events[id] = e
	return nil
}
