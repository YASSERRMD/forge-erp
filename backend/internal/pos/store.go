package pos

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

// Store is the persistence contract for till records (checkout orchestration
// lives in the handler over the catalog/sales seams).
type Store interface {
	CreateTerminal(ctx context.Context, t *Terminal) error
	TerminalByID(ctx context.Context, id int64) (Terminal, error)
	ListTerminals(ctx context.Context, entityID int64) ([]Terminal, error)
	OpenSession(ctx context.Context, s *Session) error
	SessionByID(ctx context.Context, id int64) (Session, error)
	CloseSession(ctx context.Context, id int64, rowVersion int64) (Session, error)
	CreateSale(ctx context.Context, s *Sale) error
	SaleByID(ctx context.Context, id int64) (Sale, error)
	SalesOfSession(ctx context.Context, sessionID int64) ([]Sale, error)
	VoidSale(ctx context.Context, id int64) (Sale, error)
	MarkReturned(ctx context.Context, id int64) (Sale, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const terminalCols = `id, entity_id, code, label, warehouse_id, status, created_at, updated_at, row_version`

func scanTerminal(row pgx.Row) (Terminal, error) {
	var t Terminal
	err := row.Scan(&t.ID, &t.EntityID, &t.Code, &t.Label, &t.WarehouseID,
		&t.Status, &t.CreatedAt, &t.UpdatedAt, &t.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Terminal{}, identity.ErrNotFound
	}
	return t, err
}

func (s *PGStore) CreateTerminal(ctx context.Context, t *Terminal) error {
	if err := t.Validate(); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_pos_terminals
		(entity_id, code, label, warehouse_id, status)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, row_version`,
		t.EntityID, t.Code, t.Label, t.WarehouseID, t.Status,
	).Scan(&t.ID, &t.RowVersion)
}

func (s *PGStore) TerminalByID(ctx context.Context, id int64) (Terminal, error) {
	return scanTerminal(s.pool.QueryRow(ctx, `SELECT `+terminalCols+` FROM ferp_pos_terminals WHERE id=$1`, id))
}

func (s *PGStore) ListTerminals(ctx context.Context, entityID int64) ([]Terminal, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+terminalCols+` FROM ferp_pos_terminals WHERE entity_id=$1 ORDER BY code`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Terminal
	for rows.Next() {
		t, err := scanTerminal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const sessionCols = `id, entity_id, terminal_id, cashier, opening_float, status, opened_at, closed_at, row_version`

func scanSession(row pgx.Row) (Session, error) {
	var se Session
	err := row.Scan(&se.ID, &se.EntityID, &se.TerminalID, &se.Cashier, &se.OpeningFloat,
		&se.Status, &se.OpenedAt, &se.ClosedAt, &se.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, identity.ErrNotFound
	}
	return se, err
}

func (s *PGStore) OpenSession(ctx context.Context, se *Session) error {
	if err := se.Validate(); err != nil {
		return err
	}
	t, err := s.TerminalByID(ctx, se.TerminalID)
	if err != nil {
		return err
	}
	if t.Status != TerminalActive {
		return errors.New("pos: terminal inactive")
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_pos_sessions
		(entity_id, terminal_id, cashier, opening_float, status)
		VALUES ($1,$2,$3,$4,0) RETURNING id, opened_at, row_version`,
		se.EntityID, se.TerminalID, se.Cashier, se.OpeningFloat,
	).Scan(&se.ID, &se.OpenedAt, &se.RowVersion)
}

func (s *PGStore) SessionByID(ctx context.Context, id int64) (Session, error) {
	return scanSession(s.pool.QueryRow(ctx, `SELECT `+sessionCols+` FROM ferp_pos_sessions WHERE id=$1`, id))
}

func (s *PGStore) CloseSession(ctx context.Context, id int64, rowVersion int64) (Session, error) {
	se, err := s.SessionByID(ctx, id)
	if err != nil {
		return Session{}, err
	}
	if se.RowVersion != rowVersion {
		return Session{}, identity.ErrVersionConflict
	}
	if se.Status != SessionOpen {
		return Session{}, errors.New("pos: session already closed")
	}
	now := time.Now().UTC()
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_pos_sessions SET status=1, closed_at=$1, row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, now, id, rowVersion)
	if err != nil {
		return Session{}, err
	}
	if tag.RowsAffected() == 0 {
		return Session{}, identity.ErrVersionConflict
	}
	se.Status = SessionClosed
	se.ClosedAt = &now
	se.RowVersion++
	return se, nil
}

const saleCols = `id, entity_id, session_id, ref, org_id, lines, total_gross, method, tendered, change, status, invoice_id, created_at, created_by`

func scanSale(row pgx.Row) (Sale, error) {
	var sa Sale
	var lines []byte
	err := row.Scan(&sa.ID, &sa.EntityID, &sa.SessionID, &sa.Ref, &sa.OrgID, &lines,
		&sa.TotalGross, &sa.Method, &sa.Tendered, &sa.Change, &sa.Status, &sa.InvoiceID,
		&sa.CreatedAt, &sa.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Sale{}, identity.ErrNotFound
	}
	if err != nil {
		return Sale{}, err
	}
	_ = json.Unmarshal(lines, &sa.Lines)
	return sa, nil
}

func (s *PGStore) CreateSale(ctx context.Context, sa *Sale) error {
	raw, _ := json.Marshal(sa.Lines)
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_pos_sales
		(entity_id, session_id, ref, org_id, lines, total_gross, method, tendered, change, status, invoice_id, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id, created_at`,
		sa.EntityID, sa.SessionID, sa.Ref, sa.OrgID, raw, sa.TotalGross, sa.Method,
		sa.Tendered, sa.Change, sa.Status, sa.InvoiceID, sa.CreatedBy,
	).Scan(&sa.ID, &sa.CreatedAt)
}

func (s *PGStore) SaleByID(ctx context.Context, id int64) (Sale, error) {
	return scanSale(s.pool.QueryRow(ctx, `SELECT `+saleCols+` FROM ferp_pos_sales WHERE id=$1`, id))
}

func (s *PGStore) SalesOfSession(ctx context.Context, sessionID int64) ([]Sale, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+saleCols+` FROM ferp_pos_sales WHERE session_id=$1 ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sale
	for rows.Next() {
		sa, err := scanSale(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sa)
	}
	return out, rows.Err()
}

func (s *PGStore) VoidSale(ctx context.Context, id int64) (Sale, error) {
	sa, err := s.SaleByID(ctx, id)
	if err != nil {
		return Sale{}, err
	}
	if sa.Status != SaleCompleted {
		return Sale{}, errors.New("pos: only completed sales can be voided")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_pos_sales SET status=-1 WHERE id=$1 AND status=1`, id)
	if err != nil {
		return Sale{}, err
	}
	if tag.RowsAffected() == 0 {
		return Sale{}, identity.ErrVersionConflict
	}
	sa.Status = SaleVoided
	return sa, nil
}

func (s *PGStore) MarkReturned(ctx context.Context, id int64) (Sale, error) {
	sa, err := s.SaleByID(ctx, id)
	if err != nil {
		return Sale{}, err
	}
	if sa.Status != SaleCompleted {
		return Sale{}, errors.New("pos: only completed sales can be returned")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_pos_sales SET status=2 WHERE id=$1 AND status=1`, id)
	if err != nil {
		return Sale{}, err
	}
	if tag.RowsAffected() == 0 {
		return Sale{}, identity.ErrVersionConflict
	}
	sa.Status = SaleReturned
	return sa, nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu        sync.Mutex
	seq       int64
	terminals map[int64]Terminal
	sessions  map[int64]Session
	sales     map[int64]Sale
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		terminals: map[int64]Terminal{}, sessions: map[int64]Session{}, sales: map[int64]Sale{},
	}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateTerminal(_ context.Context, t *Terminal) error {
	if err := t.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.terminals {
		if e.EntityID == t.EntityID && e.Code == t.Code {
			return errors.New("pos: duplicate terminal code")
		}
	}
	t.ID = m.next()
	t.RowVersion = 1
	m.terminals[t.ID] = *t
	return nil
}

func (m *MemoryStore) TerminalByID(_ context.Context, id int64) (Terminal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.terminals[id]
	if !ok {
		return Terminal{}, identity.ErrNotFound
	}
	return t, nil
}

func (m *MemoryStore) ListTerminals(_ context.Context, entityID int64) ([]Terminal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Terminal
	for _, t := range m.terminals {
		if t.EntityID == entityID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *MemoryStore) OpenSession(_ context.Context, se *Session) error {
	if err := se.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.terminals[se.TerminalID]
	if !ok {
		return errors.New("pos: terminal not found")
	}
	if t.Status != TerminalActive {
		return errors.New("pos: terminal inactive")
	}
	se.ID = m.next()
	se.Status = SessionOpen
	se.OpenedAt = time.Now().UTC()
	se.RowVersion = 1
	m.sessions[se.ID] = *se
	return nil
}

func (m *MemoryStore) SessionByID(_ context.Context, id int64) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	se, ok := m.sessions[id]
	if !ok {
		return Session{}, identity.ErrNotFound
	}
	return se, nil
}

func (m *MemoryStore) CloseSession(_ context.Context, id int64, rowVersion int64) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	se, ok := m.sessions[id]
	if !ok {
		return Session{}, identity.ErrNotFound
	}
	if se.RowVersion != rowVersion {
		return Session{}, identity.ErrVersionConflict
	}
	if se.Status != SessionOpen {
		return Session{}, errors.New("pos: session already closed")
	}
	now := time.Now().UTC()
	se.Status = SessionClosed
	se.ClosedAt = &now
	se.RowVersion++
	m.sessions[id] = se
	return se, nil
}

func (m *MemoryStore) CreateSale(_ context.Context, sa *Sale) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.sales {
		if e.EntityID == sa.EntityID && e.Ref == sa.Ref {
			return errors.New("pos: duplicate sale ref")
		}
	}
	sa.ID = m.next()
	sa.CreatedAt = time.Now().UTC()
	m.sales[sa.ID] = *sa
	return nil
}

func (m *MemoryStore) SaleByID(_ context.Context, id int64) (Sale, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sa, ok := m.sales[id]
	if !ok {
		return Sale{}, identity.ErrNotFound
	}
	return sa, nil
}

func (m *MemoryStore) SalesOfSession(_ context.Context, sessionID int64) ([]Sale, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Sale
	for _, sa := range m.sales {
		if sa.SessionID == sessionID {
			out = append(out, sa)
		}
	}
	return out, nil
}

func (m *MemoryStore) VoidSale(_ context.Context, id int64) (Sale, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sa, ok := m.sales[id]
	if !ok {
		return Sale{}, identity.ErrNotFound
	}
	if sa.Status != SaleCompleted {
		return Sale{}, errors.New("pos: only completed sales can be voided")
	}
	sa.Status = SaleVoided
	m.sales[id] = sa
	return sa, nil
}

func (m *MemoryStore) MarkReturned(_ context.Context, id int64) (Sale, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sa, ok := m.sales[id]
	if !ok {
		return Sale{}, identity.ErrNotFound
	}
	if sa.Status != SaleCompleted {
		return Sale{}, errors.New("pos: only completed sales can be returned")
	}
	sa.Status = SaleReturned
	m.sales[id] = sa
	return sa, nil
}
