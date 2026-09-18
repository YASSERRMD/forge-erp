// Close audit trail (Phase 3): every fiscal-year lock/unlock records who
// and when in ferp_close_log. Reopening a locked year is allowed but always
// leaves a trail row — Dolibarr has no reopen trail at all.
package finance

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Close actions (ferp_close_log.action values).
const (
	CloseActionClose  = "close"
	CloseActionReopen = "reopen"
)

// CloseLogEntry is one lock/unlock audit row.
type CloseLogEntry struct {
	ID        int64     `json:"id"`
	EntityID  int64     `json:"entity_id"`
	YearID    int64     `json:"year_id"`
	Action    string    `json:"action"`
	EntryID   *int64    `json:"entry_id,omitempty"` // close: the carry-forward entry
	Actor     *int64    `json:"actor,omitempty"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// CloseLogStore persists the close audit trail. *PGStore implements it
// against ferp_close_log; MemoryCloseLogStore is the fake.
type CloseLogStore interface {
	LogClose(ctx context.Context, db platform.DBTX, e *CloseLogEntry) error
	ListCloseLog(ctx context.Context, db platform.DBTX, entityID, yearID int64) ([]CloseLogEntry, error)
}

// LogClose appends one audit row.
func (s *PGStore) LogClose(ctx context.Context, db platform.DBTX, e *CloseLogEntry) error {
	if e.Action != CloseActionClose && e.Action != CloseActionReopen {
		return fmt.Errorf("finance: bad close action %q: %w", e.Action, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_close_log (entity_id, year_id, action, entry_id, actor, note)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, created_at`,
		e.EntityID, e.YearID, e.Action, e.EntryID, e.Actor, e.Note).
		Scan(&e.ID, &e.CreatedAt)
}

// ListCloseLog returns an entity's trail for one year, oldest first.
func (s *PGStore) ListCloseLog(ctx context.Context, db platform.DBTX, entityID, yearID int64) ([]CloseLogEntry, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, year_id, action, entry_id, actor, note, created_at
		FROM ferp_close_log WHERE entity_id=$1 AND year_id=$2 ORDER BY id`,
		entityID, yearID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CloseLogEntry
	for rows.Next() {
		var e CloseLogEntry
		if err := rows.Scan(&e.ID, &e.EntityID, &e.YearID, &e.Action,
			&e.EntryID, &e.Actor, &e.Note, &e.CreatedAt); err != nil {
			if err == pgx.ErrNoRows {
				break
			}
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MemoryCloseLogStore is the in-process fake for close-trail tests.
type MemoryCloseLogStore struct {
	mu   sync.Mutex
	seq  int64
	rows []CloseLogEntry
}

// NewMemoryCloseLogStore builds an empty fake.
func NewMemoryCloseLogStore() *MemoryCloseLogStore { return &MemoryCloseLogStore{} }

// LogClose appends one audit row.
func (m *MemoryCloseLogStore) LogClose(_ context.Context, _ platform.DBTX, e *CloseLogEntry) error {
	if e.Action != CloseActionClose && e.Action != CloseActionReopen {
		return fmt.Errorf("finance: bad close action %q: %w", e.Action, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	e.ID = m.seq
	e.CreatedAt = time.Now().UTC()
	m.rows = append(m.rows, *e)
	return nil
}

// ListCloseLog returns an entity's trail for one year, oldest first.
func (m *MemoryCloseLogStore) ListCloseLog(_ context.Context, _ platform.DBTX, entityID, yearID int64) ([]CloseLogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CloseLogEntry
	for _, e := range m.rows {
		if e.EntityID == entityID && e.YearID == yearID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
