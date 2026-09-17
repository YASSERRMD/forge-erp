package stocktransfer

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence contract for transfers.
type Store interface {
	Create(ctx context.Context, db platform.DBTX, t *Transfer, yearMonth string) error
	TransferByID(ctx context.Context, db platform.DBTX, entityID, id int64) (Transfer, error)
	List(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Transfer, error)
	UpdateDraft(ctx context.Context, db platform.DBTX, t *Transfer) error
	SetStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to int16, rowVersion int64) (Transfer, error)
	DeleteDraft(ctx context.Context, db platform.DBTX, entityID, id int64) error
	AddLine(ctx context.Context, db platform.DBTX, entityID, transferID int64, l *TransferLine) error
	LinesOf(ctx context.Context, db platform.DBTX, entityID, transferID int64) ([]TransferLine, error)
	SetLineCost(ctx context.Context, db platform.DBTX, entityID, lineID, unitCost int64) error
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const transferCols = `id, entity_id, ref, source_warehouse_id, dest_warehouse_id,
	status, note, created_at, updated_at, created_by, updated_by, row_version`

func scanTransfer(row pgx.Row) (Transfer, error) {
	var t Transfer
	err := row.Scan(&t.ID, &t.EntityID, &t.Ref, &t.SourceWarehouseID, &t.DestWarehouseID,
		&t.Status, &t.Note, &t.CreatedAt, &t.UpdatedAt, &t.CreatedBy, &t.UpdatedBy, &t.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, platform.ErrNotFound
	}
	return t, err
}

// nextRefLocked bumps the monthly counter inside the caller's transaction
// (same scheme as sales.nextRefLocked, type 'transfer' → TRF-YYYYMM-####).
func nextRefLocked(ctx context.Context, db platform.DBTX, entityID int64, ym string) (string, error) {
	var seq int64
	err := db.QueryRow(ctx, `INSERT INTO ferp_doc_counters (entity_id, type, year_month, next_seq)
		VALUES ($1,'transfer',$2,2) ON CONFLICT (entity_id, type, year_month)
		DO UPDATE SET next_seq=ferp_doc_counters.next_seq+1
		RETURNING next_seq-1`, entityID, ym).Scan(&seq)
	if err != nil {
		return "", err
	}
	return documents.NextRef(documents.TypeTransfer, ym, seq), nil
}

// Create validates the header, mints the reference, and inserts the draft.
func (s *PGStore) Create(ctx context.Context, db platform.DBTX, t *Transfer, yearMonth string) error {
	if err := t.Validate(); err != nil {
		return err
	}
	ref, err := nextRefLocked(ctx, db, t.EntityID, yearMonth)
	if err != nil {
		return err
	}
	t.Ref = ref
	t.Status = StatusDraft
	return db.QueryRow(ctx, `INSERT INTO ferp_stock_transfers
		(entity_id, ref, source_warehouse_id, dest_warehouse_id, status, note, created_by)
		VALUES ($1,$2,$3,$4,0,$5,$6)
		RETURNING id, created_at, updated_at, row_version`,
		t.EntityID, t.Ref, t.SourceWarehouseID, t.DestWarehouseID, t.Note, t.CreatedBy).
		Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt, &t.RowVersion)
}

// TransferByID fetches one transfer (404 outside the caller's entity).
func (s *PGStore) TransferByID(ctx context.Context, db platform.DBTX, entityID, id int64) (Transfer, error) {
	return scanTransfer(db.QueryRow(ctx, `SELECT `+transferCols+` FROM ferp_stock_transfers
		WHERE id=$1 AND entity_id=$2`, id, entityID))
}

// List pages transfers within one entity, newest first.
func (s *PGStore) List(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Transfer, error) {
	rows, err := db.Query(ctx, `SELECT `+transferCols+` FROM ferp_stock_transfers
		WHERE entity_id=$1 ORDER BY id DESC LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Transfer
	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateDraft edits note/warehouses of a draft with an optimistic-lock guard.
func (s *PGStore) UpdateDraft(ctx context.Context, db platform.DBTX, t *Transfer) error {
	if err := t.Validate(); err != nil {
		return err
	}
	res, err := db.Exec(ctx, `UPDATE ferp_stock_transfers
		SET source_warehouse_id=$1, dest_warehouse_id=$2, note=$3, updated_at=now(),
			updated_by=$4, row_version=row_version+1
		WHERE id=$5 AND entity_id=$6 AND status=0 AND row_version=$7`,
		t.SourceWarehouseID, t.DestWarehouseID, t.Note, t.UpdatedBy,
		t.ID, t.EntityID, t.RowVersion)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		cur, gerr := s.TransferByID(ctx, db, t.EntityID, t.ID)
		if gerr != nil {
			return gerr
		}
		if cur.Status != StatusDraft {
			return fmt.Errorf("stocktransfer %d: not a draft: %w", t.ID, platform.ErrValidation)
		}
		return fmt.Errorf("stocktransfer %d: %w", t.ID, platform.ErrVersionConflict)
	}
	t.RowVersion++
	t.UpdatedAt = time.Now().UTC()
	return nil
}

// SetStatus flips status through the documents.TypeTransfer transition
// table with an optimistic-lock guard.
func (s *PGStore) SetStatus(ctx context.Context, db platform.DBTX, entityID, id int64, to int16, rowVersion int64) (Transfer, error) {
	cur, err := s.TransferByID(ctx, db, entityID, id)
	if err != nil {
		return Transfer{}, err
	}
	if !documents.CanTransition(documents.TypeTransfer, cur.Status, to) {
		return Transfer{}, fmt.Errorf("stocktransfer %d: status %d→%d illegal: %w",
			id, cur.Status, to, platform.ErrValidation)
	}
	res, err := db.Exec(ctx, `UPDATE ferp_stock_transfers SET status=$1, updated_at=now(),
		row_version=row_version+1 WHERE id=$2 AND entity_id=$3 AND row_version=$4`,
		to, id, entityID, rowVersion)
	if err != nil {
		return Transfer{}, err
	}
	if res.RowsAffected() == 0 {
		return Transfer{}, fmt.Errorf("stocktransfer %d: %w", id, platform.ErrVersionConflict)
	}
	cur.Status = to
	cur.RowVersion++
	return cur, nil
}

// DeleteDraft removes a draft transfer and its lines.
func (s *PGStore) DeleteDraft(ctx context.Context, db platform.DBTX, entityID, id int64) error {
	res, err := db.Exec(ctx, `DELETE FROM ferp_stock_transfers
		WHERE id=$1 AND entity_id=$2 AND status=0`, id, entityID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		if _, gerr := s.TransferByID(ctx, db, entityID, id); gerr != nil {
			return gerr
		}
		return fmt.Errorf("stocktransfer %d: only drafts delete: %w", id, platform.ErrValidation)
	}
	return nil
}

const lineCols = `id, transfer_id, pos, product_id, qty, unit_cost`

func scanLine(row pgx.Row) (TransferLine, error) {
	var l TransferLine
	err := row.Scan(&l.ID, &l.TransferID, &l.Pos, &l.ProductID, &l.Qty, &l.UnitCost)
	if errors.Is(err, pgx.ErrNoRows) {
		return TransferLine{}, platform.ErrNotFound
	}
	return l, err
}

// AddLine appends one line to a draft transfer.
func (s *PGStore) AddLine(ctx context.Context, db platform.DBTX, entityID, transferID int64, l *TransferLine) error {
	if err := l.Validate(); err != nil {
		return err
	}
	cur, err := s.TransferByID(ctx, db, entityID, transferID)
	if err != nil {
		return err
	}
	if cur.Status != StatusDraft {
		return fmt.Errorf("stocktransfer %d: lines need a draft: %w", transferID, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_stock_transfer_lines
		(transfer_id, pos, product_id, qty, unit_cost)
		VALUES ($1, COALESCE((SELECT max(pos)+1 FROM ferp_stock_transfer_lines WHERE transfer_id=$1),0), $2, $3, $4)
		RETURNING id, pos`,
		transferID, l.ProductID, l.Qty, l.UnitCost).Scan(&l.ID, &l.Pos)
}

// LinesOf returns a transfer's lines in position order.
func (s *PGStore) LinesOf(ctx context.Context, db platform.DBTX, entityID, transferID int64) ([]TransferLine, error) {
	if _, err := s.TransferByID(ctx, db, entityID, transferID); err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, `SELECT `+lineCols+` FROM ferp_stock_transfer_lines
		WHERE transfer_id=$1 ORDER BY pos, id`, transferID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TransferLine
	for rows.Next() {
		l, err := scanLine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SetLineCost records the PMP snapshot taken at validation.
func (s *PGStore) SetLineCost(ctx context.Context, db platform.DBTX, entityID, lineID, unitCost int64) error {
	res, err := db.Exec(ctx, `UPDATE ferp_stock_transfer_lines SET unit_cost=$1
		WHERE id=$2 AND transfer_id IN (SELECT id FROM ferp_stock_transfers WHERE entity_id=$3)`,
		unitCost, lineID, entityID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu        sync.Mutex
	seq       int64
	transfers map[int64]Transfer
	lines     map[int64][]TransferLine
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{transfers: map[int64]Transfer{}, lines: map[int64][]TransferLine{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) Create(_ context.Context, _ platform.DBTX, t *Transfer, yearMonth string) error {
	if err := t.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t.ID = m.next()
	t.Ref = documents.NextRef(documents.TypeTransfer, yearMonth, t.ID)
	t.Status = StatusDraft
	t.CreatedAt = time.Now().UTC()
	t.UpdatedAt = t.CreatedAt
	t.RowVersion = 1
	m.transfers[t.ID] = *t
	return nil
}

func (m *MemoryStore) TransferByID(_ context.Context, _ platform.DBTX, entityID, id int64) (Transfer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.transfers[id]
	if !ok || t.EntityID != entityID {
		return Transfer{}, fmt.Errorf("stocktransfer %d: %w", id, platform.ErrNotFound)
	}
	return t, nil
}

func (m *MemoryStore) List(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]Transfer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Transfer
	for _, t := range m.transfers {
		if t.EntityID == entityID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) UpdateDraft(_ context.Context, _ platform.DBTX, t *Transfer) error {
	if err := t.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.transfers[t.ID]
	if !ok || cur.EntityID != t.EntityID {
		return fmt.Errorf("stocktransfer %d: %w", t.ID, platform.ErrNotFound)
	}
	if cur.Status != StatusDraft {
		return fmt.Errorf("stocktransfer %d: not a draft: %w", t.ID, platform.ErrValidation)
	}
	if cur.RowVersion != t.RowVersion {
		return fmt.Errorf("stocktransfer %d: %w", t.ID, platform.ErrVersionConflict)
	}
	cur.SourceWarehouseID = t.SourceWarehouseID
	cur.DestWarehouseID = t.DestWarehouseID
	cur.Note = t.Note
	cur.UpdatedBy = t.UpdatedBy
	cur.RowVersion++
	cur.UpdatedAt = time.Now().UTC()
	m.transfers[t.ID] = cur
	*t = cur
	return nil
}

func (m *MemoryStore) SetStatus(_ context.Context, _ platform.DBTX, entityID, id int64, to int16, rowVersion int64) (Transfer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.transfers[id]
	if !ok || cur.EntityID != entityID {
		return Transfer{}, fmt.Errorf("stocktransfer %d: %w", id, platform.ErrNotFound)
	}
	if !documents.CanTransition(documents.TypeTransfer, cur.Status, to) {
		return Transfer{}, fmt.Errorf("stocktransfer %d: status %d→%d illegal: %w",
			id, cur.Status, to, platform.ErrValidation)
	}
	if cur.RowVersion != rowVersion {
		return Transfer{}, fmt.Errorf("stocktransfer %d: %w", id, platform.ErrVersionConflict)
	}
	cur.Status = to
	cur.RowVersion++
	m.transfers[id] = cur
	return cur, nil
}

func (m *MemoryStore) DeleteDraft(_ context.Context, _ platform.DBTX, entityID, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.transfers[id]
	if !ok || cur.EntityID != entityID {
		return fmt.Errorf("stocktransfer %d: %w", id, platform.ErrNotFound)
	}
	if cur.Status != StatusDraft {
		return fmt.Errorf("stocktransfer %d: only drafts delete: %w", id, platform.ErrValidation)
	}
	delete(m.transfers, id)
	delete(m.lines, id)
	return nil
}

func (m *MemoryStore) AddLine(_ context.Context, _ platform.DBTX, entityID, transferID int64, l *TransferLine) error {
	if err := l.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.transfers[transferID]
	if !ok || cur.EntityID != entityID {
		return fmt.Errorf("stocktransfer %d: %w", transferID, platform.ErrNotFound)
	}
	if cur.Status != StatusDraft {
		return fmt.Errorf("stocktransfer %d: lines need a draft: %w", transferID, platform.ErrValidation)
	}
	l.ID = m.next()
	l.TransferID = transferID
	l.Pos = len(m.lines[transferID])
	m.lines[transferID] = append(m.lines[transferID], *l)
	return nil
}

func (m *MemoryStore) LinesOf(_ context.Context, _ platform.DBTX, entityID, transferID int64) ([]TransferLine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.transfers[transferID]
	if !ok || cur.EntityID != entityID {
		return nil, fmt.Errorf("stocktransfer %d: %w", transferID, platform.ErrNotFound)
	}
	out := append([]TransferLine(nil), m.lines[transferID]...)
	return out, nil
}

func (m *MemoryStore) SetLineCost(_ context.Context, _ platform.DBTX, entityID, lineID, unitCost int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for tid, ls := range m.lines {
		for i, l := range ls {
			if l.ID == lineID {
				if m.transfers[tid].EntityID != entityID {
					return platform.ErrNotFound
				}
				ls[i].UnitCost = unitCost
				m.lines[tid] = ls
				return nil
			}
		}
	}
	return platform.ErrNotFound
}
