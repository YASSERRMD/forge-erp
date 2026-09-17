package label

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

// Store is the persistence contract for the label context.
type Store interface {
	CreateSheet(ctx context.Context, db platform.DBTX, s *SheetDef) error
	SheetByID(ctx context.Context, db platform.DBTX, entityID, id int64) (SheetDef, error)
	SheetByCode(ctx context.Context, db platform.DBTX, entityID int64, code string) (SheetDef, error)
	ListSheets(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]SheetDef, error)
	UpdateSheet(ctx context.Context, db platform.DBTX, s *SheetDef) error
	DeleteSheet(ctx context.Context, db platform.DBTX, entityID, id int64) error
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const sheetCols = `id, entity_id, code, name, rows, cols, label_w_mm, label_h_mm,
	margin_top_mm, margin_left_mm, gap_x_mm, gap_y_mm, fields, row_version`

func scanSheet(row pgx.Row) (SheetDef, error) {
	var s SheetDef
	var fields []byte
	err := row.Scan(&s.ID, &s.EntityID, &s.Code, &s.Name, &s.Rows, &s.Cols,
		&s.LabelWMM, &s.LabelHMM, &s.MarginTopMM, &s.MarginLeftMM,
		&s.GapXMM, &s.GapYMM, &fields, &s.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return SheetDef{}, platform.ErrNotFound
	}
	if err != nil {
		return SheetDef{}, err
	}
	_ = json.Unmarshal(fields, &s.Fields)
	if s.Fields == nil {
		s.Fields = []string{}
	}
	return s, nil
}

func (s *PGStore) CreateSheet(ctx context.Context, db platform.DBTX, sh *SheetDef) error {
	if err := sh.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	raw, _ := json.Marshal(sh.Fields)
	return db.QueryRow(ctx, `INSERT INTO ferp_label_sheets
		(entity_id, code, name, rows, cols, label_w_mm, label_h_mm,
		 margin_top_mm, margin_left_mm, gap_x_mm, gap_y_mm, fields)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id, row_version`,
		sh.EntityID, sh.Code, sh.Name, sh.Rows, sh.Cols, sh.LabelWMM, sh.LabelHMM,
		sh.MarginTopMM, sh.MarginLeftMM, sh.GapXMM, sh.GapYMM, raw).Scan(&sh.ID, &sh.RowVersion)
}

func (s *PGStore) SheetByID(ctx context.Context, db platform.DBTX, entityID, id int64) (SheetDef, error) {
	return scanSheet(db.QueryRow(ctx, `SELECT `+sheetCols+` FROM ferp_label_sheets WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) SheetByCode(ctx context.Context, db platform.DBTX, entityID int64, code string) (SheetDef, error) {
	return scanSheet(db.QueryRow(ctx, `SELECT `+sheetCols+` FROM ferp_label_sheets WHERE code=$1 AND entity_id=$2`, code, entityID))
}

func (s *PGStore) ListSheets(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]SheetDef, error) {
	rows, err := db.Query(ctx, `SELECT `+sheetCols+` FROM ferp_label_sheets
		WHERE entity_id=$1 ORDER BY code LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SheetDef
	for rows.Next() {
		sh, err := scanSheet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

func (s *PGStore) UpdateSheet(ctx context.Context, db platform.DBTX, sh *SheetDef) error {
	if err := sh.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	raw, _ := json.Marshal(sh.Fields)
	tag, err := db.Exec(ctx, `UPDATE ferp_label_sheets SET code=$1, name=$2, rows=$3, cols=$4,
		label_w_mm=$5, label_h_mm=$6, margin_top_mm=$7, margin_left_mm=$8,
		gap_x_mm=$9, gap_y_mm=$10, fields=$11, updated_at=now(), row_version=row_version+1
		WHERE id=$12 AND entity_id=$14 AND row_version=$13`,
		sh.Code, sh.Name, sh.Rows, sh.Cols, sh.LabelWMM, sh.LabelHMM,
		sh.MarginTopMM, sh.MarginLeftMM, sh.GapXMM, sh.GapYMM, raw,
		sh.ID, sh.RowVersion, sh.EntityID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return platform.ErrVersionConflict
	}
	sh.RowVersion++
	return nil
}

func (s *PGStore) DeleteSheet(ctx context.Context, db platform.DBTX, entityID, id int64) error {
	tag, err := db.Exec(ctx, `DELETE FROM ferp_label_sheets WHERE id=$1 AND entity_id=$2`, id, entityID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return platform.ErrNotFound
	}
	return nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu     sync.Mutex
	seq    int64
	sheets map[int64]SheetDef
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sheets: map[int64]SheetDef{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateSheet(_ context.Context, _ platform.DBTX, sh *SheetDef) error {
	if err := sh.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.sheets {
		if e.EntityID == sh.EntityID && e.Code == sh.Code {
			return fmt.Errorf("label: duplicate sheet code: %w", platform.ErrConflict)
		}
	}
	sh.ID = m.next()
	sh.RowVersion = 1
	m.sheets[sh.ID] = *sh
	return nil
}

func (m *MemoryStore) SheetByID(_ context.Context, _ platform.DBTX, entityID, id int64) (SheetDef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.sheets[id]
	if !ok || sh.EntityID != entityID {
		return SheetDef{}, platform.ErrNotFound
	}
	return sh, nil
}

func (m *MemoryStore) SheetByCode(_ context.Context, _ platform.DBTX, entityID int64, code string) (SheetDef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sh := range m.sheets {
		if sh.EntityID == entityID && sh.Code == code {
			return sh, nil
		}
	}
	return SheetDef{}, platform.ErrNotFound
}

func (m *MemoryStore) ListSheets(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]SheetDef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SheetDef
	for _, sh := range m.sheets {
		if sh.EntityID == entityID {
			out = append(out, sh)
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

func (m *MemoryStore) UpdateSheet(_ context.Context, _ platform.DBTX, sh *SheetDef) error {
	if err := sh.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.sheets[sh.ID]
	if !ok || cur.EntityID != sh.EntityID {
		return platform.ErrNotFound
	}
	if cur.RowVersion != sh.RowVersion {
		return platform.ErrVersionConflict
	}
	sh.RowVersion++
	m.sheets[sh.ID] = *sh
	return nil
}

func (m *MemoryStore) DeleteSheet(_ context.Context, _ platform.DBTX, entityID, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.sheets[id]
	if !ok || sh.EntityID != entityID {
		return platform.ErrNotFound
	}
	delete(m.sheets, id)
	return nil
}
