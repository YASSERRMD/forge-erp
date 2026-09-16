package pos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Offline queue + session cash domain (Phase 2 TakePOS depth). Money stays
// int64 minor units; every method takes (ctx, db platform.DBTX) per the
// Store contract so the replay service can run queue-mark + checkout in one
// transaction.

// QueueStatus is the lifecycle of an offline till payload.
type QueueStatus string

const (
	QueueQueued QueueStatus = "queued"
	QueueSent   QueueStatus = "sent"
	QueueFailed QueueStatus = "failed"
)

// QueuedSale is one offline till payload awaiting replay. Payload mirrors
// CheckoutCmd (without entity/session, which ride as columns); the
// idempotency key is unique per entity so retried syncs collapse.
type QueuedSale struct {
	ID             int64       `json:"id"`
	EntityID       int64       `json:"entity_id"`
	SessionID      int64       `json:"session_id"`
	IdempotencyKey string      `json:"idempotency_key"`
	OrgID          int64       `json:"org_id"`
	Lines          []SaleLine  `json:"lines"`
	Method         string      `json:"method"`
	Tendered       int64       `json:"tendered"`
	Payments       []Tender    `json:"payments,omitempty"`
	Status         QueueStatus `json:"status"`
	Attempts       int         `json:"attempts"`
	LastError      string      `json:"last_error"`
	CreatedAt      time.Time   `json:"created_at"`
	SentAt         *time.Time  `json:"sent_at"`
}

// Validate checks queue invariants (line/money rules re-checked by Checkout
// at replay; enqueue stays light so the till never blocks offline).
func (q QueuedSale) Validate() error {
	if q.EntityID <= 0 || q.SessionID <= 0 {
		return errors.New("pos: entity_id and session_id required")
	}
	if q.IdempotencyKey == "" {
		return errors.New("pos: idempotency_key required")
	}
	if len(q.Lines) == 0 {
		return errors.New("pos: queued sale requires at least one line")
	}
	return nil
}

// CheckoutCmd converts the queued payload back to a checkout command.
func (q QueuedSale) CheckoutCmd() CheckoutCmd {
	return CheckoutCmd{
		EntityID: q.EntityID, SessionID: q.SessionID, OrgID: q.OrgID,
		Lines: q.Lines, Method: q.Method, Tendered: q.Tendered, Payments: q.Payments,
	}
}

// Payout is cash paid OUT of the drawer mid-shift (petty cash, COD supplier).
// It reduces the expected drawer cash.
type Payout struct {
	ID        int64     `json:"id"`
	EntityID  int64     `json:"entity_id"`
	SessionID int64     `json:"session_id"`
	Amount    int64     `json:"amount"` // minor units, > 0
	Reason    string    `json:"reason"`
	CreatedBy *int64    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// Validate checks payout invariants.
func (p Payout) Validate() error {
	if p.EntityID <= 0 || p.SessionID <= 0 {
		return errors.New("pos: entity_id and session_id required")
	}
	if p.Amount <= 0 {
		return errors.New("pos: payout amount must be positive")
	}
	return nil
}

// CashCount is one drawer count snapshot: counted cash vs expected cash.
// Variance = counted − expected (negative = short).
type CashCount struct {
	ID           int64     `json:"id"`
	EntityID     int64     `json:"entity_id"`
	SessionID    int64     `json:"session_id"`
	CountedCash  int64     `json:"counted_cash"`
	ExpectedCash int64     `json:"expected_cash"`
	Variance     int64     `json:"variance"`
	CreatedAt    time.Time `json:"created_at"`
}

// queuePayload is the JSONB envelope stored in ferp_pos_queue.payload.
type queuePayload struct {
	OrgID    int64      `json:"org_id"`
	Lines    []SaleLine `json:"lines"`
	Method   string     `json:"method"`
	Tendered int64      `json:"tendered"`
	Payments []Tender   `json:"payments,omitempty"`
}

const queueCols = `id, entity_id, session_id, idempotency_key, payload, status, attempts, last_error, created_at, sent_at`

func scanQueue(row pgx.Row) (QueuedSale, error) {
	var q QueuedSale
	var raw []byte
	var status string
	if err := row.Scan(&q.ID, &q.EntityID, &q.SessionID, &q.IdempotencyKey, &raw,
		&status, &q.Attempts, &q.LastError, &q.CreatedAt, &q.SentAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return QueuedSale{}, identity.ErrNotFound
		}
		return QueuedSale{}, err
	}
	q.Status = QueueStatus(status)
	var p queuePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return QueuedSale{}, err
	}
	q.OrgID, q.Lines, q.Method, q.Tendered, q.Payments = p.OrgID, p.Lines, p.Method, p.Tendered, p.Payments
	return q, nil
}

func queuePayloadOf(q *QueuedSale) []byte {
	raw, _ := json.Marshal(queuePayload{
		OrgID: q.OrgID, Lines: q.Lines, Method: q.Method,
		Tendered: q.Tendered, Payments: q.Payments,
	})
	return raw
}

// EnqueueOffline stores a till payload; a retried sync with the same
// (entity, key) returns the existing row instead of duplicating (idempotent
// on key). Only queued rows are returned on conflict — sent/failed history
// is never resurrected by a late retry.
func (s *PGStore) EnqueueOffline(ctx context.Context, db platform.DBTX, q *QueuedSale) error {
	if err := q.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	raw := queuePayloadOf(q)
	err := db.QueryRow(ctx, `INSERT INTO ferp_pos_queue
		(entity_id, session_id, idempotency_key, payload)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (entity_id, idempotency_key) DO NOTHING
		RETURNING id, created_at`,
		q.EntityID, q.SessionID, q.IdempotencyKey, raw,
	).Scan(&q.ID, &q.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, findErr := s.QueueByKey(ctx, db, q.EntityID, q.IdempotencyKey)
		if findErr != nil {
			return findErr
		}
		*q = existing
		return nil
	}
	if err != nil {
		return err
	}
	q.Status = QueueQueued
	return nil
}

// QueueByKey fetches one queued payload by idempotency key (entity-scoped).
func (s *PGStore) QueueByKey(ctx context.Context, db platform.DBTX, entityID int64, key string) (QueuedSale, error) {
	return scanQueue(db.QueryRow(ctx, `SELECT `+queueCols+` FROM ferp_pos_queue WHERE entity_id=$1 AND idempotency_key=$2`, entityID, key))
}

// QueuedDrain lists queued payloads for a session in FIFO order (replay input).
func (s *PGStore) QueuedDrain(ctx context.Context, db platform.DBTX, entityID, sessionID int64) ([]QueuedSale, error) {
	return s.queueList(ctx, db, entityID, sessionID, "queued")
}

// QueueList lists payloads for a session, optionally filtered by status
// ("" = all), newest last.
func (s *PGStore) QueueList(ctx context.Context, db platform.DBTX, entityID, sessionID int64, status string) ([]QueuedSale, error) {
	return s.queueList(ctx, db, entityID, sessionID, status)
}

func (s *PGStore) queueList(ctx context.Context, db platform.DBTX, entityID, sessionID int64, status string) ([]QueuedSale, error) {
	q := `SELECT ` + queueCols + ` FROM ferp_pos_queue WHERE entity_id=$1 AND session_id=$2`
	args := []any{entityID, sessionID}
	if status != "" {
		q += ` AND status=$3`
		args = append(args, status)
	}
	q += ` ORDER BY id`
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QueuedSale
	for rows.Next() {
		e, err := scanQueue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkQueue records a replay attempt: attempts always increments; sent stamps
// sent_at, failed keeps the error for the next operator shift.
func (s *PGStore) MarkQueue(ctx context.Context, db platform.DBTX, entityID, id int64, status QueueStatus, lastErr string) (QueuedSale, error) {
	switch status {
	case QueueSent, QueueFailed:
	default:
		return QueuedSale{}, fmt.Errorf("pos: bad queue status %q: %w", status, platform.ErrValidation)
	}
	var sentAt *time.Time
	if status == QueueSent {
		now := time.Now().UTC()
		sentAt = &now
		lastErr = ""
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_pos_queue
		SET status=$1, attempts=attempts+1, last_error=$2, sent_at=COALESCE($3, sent_at)
		WHERE id=$4 AND entity_id=$5`, string(status), lastErr, sentAt, id, entityID)
	if err != nil {
		return QueuedSale{}, err
	}
	if tag.RowsAffected() == 0 {
		return QueuedSale{}, identity.ErrNotFound
	}
	return scanQueue(db.QueryRow(ctx, `SELECT `+queueCols+` FROM ferp_pos_queue WHERE id=$1 AND entity_id=$2`, id, entityID))
}

// RecordPayout books cash out of the drawer.
func (s *PGStore) RecordPayout(ctx context.Context, db platform.DBTX, p *Payout) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_pos_payouts
		(entity_id, session_id, amount, reason, created_by)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`,
		p.EntityID, p.SessionID, p.Amount, p.Reason, p.CreatedBy,
	).Scan(&p.ID, &p.CreatedAt)
}

// PayoutsOfSession lists a session's payouts in order.
func (s *PGStore) PayoutsOfSession(ctx context.Context, db platform.DBTX, entityID, sessionID int64) ([]Payout, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, session_id, amount, reason, created_by, created_at
		FROM ferp_pos_payouts WHERE entity_id=$1 AND session_id=$2 ORDER BY id`, entityID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Payout
	for rows.Next() {
		var p Payout
		if err := rows.Scan(&p.ID, &p.EntityID, &p.SessionID, &p.Amount, &p.Reason, &p.CreatedBy, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RecordCount stores a drawer count snapshot (variance precomputed by the
// service so PG never does money math).
func (s *PGStore) RecordCount(ctx context.Context, db platform.DBTX, c *CashCount) error {
	if c.CountedCash < 0 {
		return fmt.Errorf("pos: negative counted cash: %w", platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_pos_counts
		(entity_id, session_id, counted_cash, expected_cash, variance)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`,
		c.EntityID, c.SessionID, c.CountedCash, c.ExpectedCash, c.Variance,
	).Scan(&c.ID, &c.CreatedAt)
}

// CountsOfSession lists a session's count snapshots in order.
func (s *PGStore) CountsOfSession(ctx context.Context, db platform.DBTX, entityID, sessionID int64) ([]CashCount, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, session_id, counted_cash, expected_cash, variance, created_at
		FROM ferp_pos_counts WHERE entity_id=$1 AND session_id=$2 ORDER BY id`, entityID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CashCount
	for rows.Next() {
		var c CashCount
		if err := rows.Scan(&c.ID, &c.EntityID, &c.SessionID, &c.CountedCash, &c.ExpectedCash, &c.Variance, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Memory implementations (handler/service tests run without a database).

// EnqueueOffline implements Store (idempotent on key).
func (m *MemoryStore) EnqueueOffline(_ context.Context, _ platform.DBTX, q *QueuedSale) error {
	if err := q.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.queue {
		if e.EntityID == q.EntityID && e.IdempotencyKey == q.IdempotencyKey {
			*q = e
			return nil
		}
	}
	q.ID = m.next()
	q.Status = QueueQueued
	q.CreatedAt = time.Now().UTC()
	m.queue[q.ID] = *q
	return nil
}

// QueueByKey implements Store.
func (m *MemoryStore) QueueByKey(_ context.Context, _ platform.DBTX, entityID int64, key string) (QueuedSale, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.queue {
		if e.EntityID == entityID && e.IdempotencyKey == key {
			return e, nil
		}
	}
	return QueuedSale{}, identity.ErrNotFound
}

func (m *MemoryStore) queueList(entityID, sessionID int64, status string) []QueuedSale {
	var out []QueuedSale
	for _, e := range m.queue {
		if e.EntityID != entityID || e.SessionID != sessionID {
			continue
		}
		if status != "" && string(e.Status) != status {
			continue
		}
		out = append(out, e)
	}
	// m.next() is monotonic so sorting by ID is FIFO.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ID < out[j-1].ID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// QueuedDrain implements Store.
func (m *MemoryStore) QueuedDrain(_ context.Context, _ platform.DBTX, entityID, sessionID int64) ([]QueuedSale, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.queueList(entityID, sessionID, string(QueueQueued)), nil
}

// QueueList implements Store.
func (m *MemoryStore) QueueList(_ context.Context, _ platform.DBTX, entityID, sessionID int64, status string) ([]QueuedSale, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.queueList(entityID, sessionID, status), nil
}

// MarkQueue implements Store.
func (m *MemoryStore) MarkQueue(_ context.Context, _ platform.DBTX, entityID, id int64, status QueueStatus, lastErr string) (QueuedSale, error) {
	switch status {
	case QueueSent, QueueFailed:
	default:
		return QueuedSale{}, fmt.Errorf("pos: bad queue status %q: %w", status, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.queue[id]
	if !ok || e.EntityID != entityID {
		return QueuedSale{}, identity.ErrNotFound
	}
	e.Status = status
	e.Attempts++
	if status == QueueSent {
		now := time.Now().UTC()
		e.SentAt = &now
		e.LastError = ""
	} else {
		e.LastError = lastErr
	}
	m.queue[id] = e
	return e, nil
}

// RecordPayout implements Store.
func (m *MemoryStore) RecordPayout(_ context.Context, _ platform.DBTX, p *Payout) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p.ID = m.next()
	p.CreatedAt = time.Now().UTC()
	m.payouts[p.ID] = *p
	return nil
}

// PayoutsOfSession implements Store.
func (m *MemoryStore) PayoutsOfSession(_ context.Context, _ platform.DBTX, entityID, sessionID int64) ([]Payout, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Payout
	for _, p := range m.payouts {
		if p.EntityID == entityID && p.SessionID == sessionID {
			out = append(out, p)
		}
	}
	return out, nil
}

// RecordCount implements Store.
func (m *MemoryStore) RecordCount(_ context.Context, _ platform.DBTX, c *CashCount) error {
	if c.CountedCash < 0 {
		return fmt.Errorf("pos: negative counted cash: %w", platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c.ID = m.next()
	c.CreatedAt = time.Now().UTC()
	m.counts[c.ID] = *c
	return nil
}

// CountsOfSession implements Store.
func (m *MemoryStore) CountsOfSession(_ context.Context, _ platform.DBTX, entityID, sessionID int64) ([]CashCount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CashCount
	for _, c := range m.counts {
		if c.EntityID == entityID && c.SessionID == sessionID {
			out = append(out, c)
		}
	}
	return out, nil
}
