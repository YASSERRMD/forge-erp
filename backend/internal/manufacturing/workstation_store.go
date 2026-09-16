package manufacturing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

const workstationCols = `id, entity_id, code, label, daily_capacity_min, status, created_at, updated_at, row_version`

func scanWorkstation(row pgx.Row) (Workstation, error) {
	var w Workstation
	err := row.Scan(&w.ID, &w.EntityID, &w.Code, &w.Label, &w.DailyCapacityMin,
		&w.Status, &w.CreatedAt, &w.UpdatedAt, &w.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workstation{}, identity.ErrNotFound
	}
	return w, err
}

func (s *PGStore) CreateWorkstation(ctx context.Context, db platform.DBTX, w *Workstation) error {
	if err := w.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_workstations
		(entity_id, code, label, daily_capacity_min, status)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, row_version`,
		w.EntityID, w.Code, w.Label, w.DailyCapacityMin, w.Status,
	).Scan(&w.ID, &w.RowVersion)
}

func (s *PGStore) WorkstationByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Workstation, error) {
	return scanWorkstation(db.QueryRow(ctx, `SELECT `+workstationCols+
		` FROM ferp_workstations WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) ListWorkstations(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Workstation, error) {
	rows, err := db.Query(ctx, `SELECT `+workstationCols+` FROM ferp_workstations
		WHERE entity_id=$1 ORDER BY code LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workstation
	for rows.Next() {
		w, err := scanWorkstation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

const bomOpCols = `id, entity_id, bom_id, seq, workstation_id, run_minutes_per_unit, setup_minutes`

func scanBOMOperation(row pgx.Row) (BOMOperation, error) {
	var o BOMOperation
	err := row.Scan(&o.ID, &o.EntityID, &o.BOMID, &o.Seq, &o.WorkstationID,
		&o.RunMinutesPerUnit, &o.SetupMinutes)
	if errors.Is(err, pgx.ErrNoRows) {
		return BOMOperation{}, identity.ErrNotFound
	}
	return o, err
}

func (s *PGStore) AddBOMOperation(ctx context.Context, db platform.DBTX, o *BOMOperation) error {
	if o.EntityID <= 0 || o.BOMID <= 0 {
		return fmt.Errorf("manufacturing: entity_id and bom_id required: %w", platform.ErrValidation)
	}
	if _, err := s.BOMByID(ctx, db, o.EntityID, o.BOMID); err != nil {
		return err
	}
	if _, err := s.WorkstationByID(ctx, db, o.EntityID, o.WorkstationID); err != nil {
		return err
	}
	if o.Seq <= 0 {
		var max int32
		if err := db.QueryRow(ctx, `SELECT COALESCE(MAX(seq),0) FROM ferp_bom_operations WHERE bom_id=$1`, o.BOMID).Scan(&max); err != nil {
			return err
		}
		o.Seq = max + 1
	}
	if err := o.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_bom_operations
		(entity_id, bom_id, seq, workstation_id, run_minutes_per_unit, setup_minutes)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		o.EntityID, o.BOMID, o.Seq, o.WorkstationID, o.RunMinutesPerUnit, o.SetupMinutes,
	).Scan(&o.ID)
}

func (s *PGStore) BOMOperations(ctx context.Context, db platform.DBTX, bomID int64) ([]BOMOperation, error) {
	rows, err := db.Query(ctx, `SELECT `+bomOpCols+` FROM ferp_bom_operations
		WHERE bom_id=$1 ORDER BY seq, id`, bomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BOMOperation
	for rows.Next() {
		o, err := scanBOMOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

const moOpCols = `id, entity_id, mo_id, seq, workstation_id, planned_minutes, scheduled_start, scheduled_end, status, overloaded, actual_minutes, row_version`

func scanMOOperation(row pgx.Row) (MOOperation, error) {
	var o MOOperation
	err := row.Scan(&o.ID, &o.EntityID, &o.MOID, &o.Seq, &o.WorkstationID,
		&o.PlannedMinutes, &o.ScheduledStart, &o.ScheduledEnd, &o.Status,
		&o.Overloaded, &o.ActualMinutes, &o.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return MOOperation{}, identity.ErrNotFound
	}
	return o, err
}

func (s *PGStore) ReplaceMOOperations(ctx context.Context, db platform.DBTX, entityID int64, moID int64, ops []MOOperation) error {
	if _, err := s.MOByID(ctx, db, entityID, moID); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, `DELETE FROM ferp_mo_operations WHERE mo_id=$1 AND entity_id=$2`, moID, entityID); err != nil {
		return err
	}
	for i := range ops {
		o := &ops[i]
		o.EntityID = entityID
		o.MOID = moID
		if err := db.QueryRow(ctx, `INSERT INTO ferp_mo_operations
			(entity_id, mo_id, seq, workstation_id, planned_minutes, scheduled_start, scheduled_end, status, overloaded, actual_minutes)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id, row_version`,
			o.EntityID, o.MOID, o.Seq, o.WorkstationID, o.PlannedMinutes,
			o.ScheduledStart, o.ScheduledEnd, o.Status, o.Overloaded, o.ActualMinutes,
		).Scan(&o.ID, &o.RowVersion); err != nil {
			return err
		}
	}
	return nil
}

func (s *PGStore) MOOperations(ctx context.Context, db platform.DBTX, moID int64) ([]MOOperation, error) {
	rows, err := db.Query(ctx, `SELECT `+moOpCols+` FROM ferp_mo_operations
		WHERE mo_id=$1 ORDER BY seq, id`, moID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MOOperation
	for rows.Next() {
		o, err := scanMOOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *PGStore) CompleteMOOperation(ctx context.Context, db platform.DBTX, entityID int64, moID int64, seq int32, actualMinutes int64) (MOOperation, error) {
	if actualMinutes < 0 {
		return MOOperation{}, fmt.Errorf("manufacturing: actual_minutes cannot be negative: %w", platform.ErrValidation)
	}
	var o MOOperation
	o, err := scanMOOperation(db.QueryRow(ctx, `UPDATE ferp_mo_operations
		SET status=$1, actual_minutes=$2, updated_at=now(), row_version=row_version+1
		WHERE mo_id=$3 AND seq=$4 AND entity_id=$5 AND status=$6
		RETURNING `+moOpCols, MOOpDone, actualMinutes, moID, seq, entityID, MOOpPending))
	if err == nil {
		return o, nil
	}
	if !errors.Is(err, identity.ErrNotFound) {
		return MOOperation{}, err
	}
	existing, findErr := s.moOperationBySeq(ctx, db, entityID, moID, seq)
	if findErr != nil {
		return MOOperation{}, findErr
	}
	if existing.Status != MOOpPending {
		return MOOperation{}, fmt.Errorf("manufacturing: operation seq %d already completed: %w", seq, platform.ErrValidation)
	}
	return MOOperation{}, err
}

func (s *PGStore) moOperationBySeq(ctx context.Context, db platform.DBTX, entityID int64, moID int64, seq int32) (MOOperation, error) {
	return scanMOOperation(db.QueryRow(ctx, `SELECT `+moOpCols+
		` FROM ferp_mo_operations WHERE mo_id=$1 AND seq=$2 AND entity_id=$3`, moID, seq, entityID))
}

func (s *PGStore) OperationsByWorkstation(ctx context.Context, db platform.DBTX, entityID int64, workstationID int64, from, to time.Time) ([]MOOperation, error) {
	rows, err := db.Query(ctx, `SELECT `+moOpCols+` FROM ferp_mo_operations
		WHERE entity_id=$1 AND workstation_id=$2 AND scheduled_start < $4 AND scheduled_end > $3
		ORDER BY scheduled_start, seq`, entityID, workstationID, midnightUTC(from), midnightUTC(to).AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MOOperation
	for rows.Next() {
		o, err := scanMOOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// MemoryStore extensions for workstations, routing and scheduled operations.

func (m *MemoryStore) CreateWorkstation(_ context.Context, _ platform.DBTX, w *Workstation) error {
	if err := w.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.workstations {
		if e.EntityID == w.EntityID && e.Code == w.Code {
			return fmt.Errorf("manufacturing: duplicate workstation code: %w", platform.ErrConflict)
		}
	}
	w.ID = m.next()
	w.RowVersion = 1
	m.workstations[w.ID] = *w
	return nil
}

func (m *MemoryStore) WorkstationByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (Workstation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.workstations[id]
	if !ok || w.EntityID != entityID {
		return Workstation{}, identity.ErrNotFound
	}
	return w, nil
}

func (m *MemoryStore) ListWorkstations(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]Workstation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Workstation
	for _, w := range m.workstations {
		if w.EntityID == entityID {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) AddBOMOperation(_ context.Context, _ platform.DBTX, o *BOMOperation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.boms[o.BOMID]
	if !ok || b.EntityID != o.EntityID {
		return fmt.Errorf("manufacturing: BOM not found: %w", platform.ErrNotFound)
	}
	ws, ok := m.workstations[o.WorkstationID]
	if !ok || ws.EntityID != o.EntityID {
		return fmt.Errorf("manufacturing: workstation not found: %w", platform.ErrNotFound)
	}
	if o.Seq <= 0 {
		var max int32
		for _, e := range m.bomOps {
			if e.BOMID == o.BOMID && e.Seq > max {
				max = e.Seq
			}
		}
		o.Seq = max + 1
	}
	if err := o.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	for _, e := range m.bomOps {
		if e.BOMID == o.BOMID && e.Seq == o.Seq {
			return fmt.Errorf("manufacturing: duplicate routing seq: %w", platform.ErrConflict)
		}
	}
	o.ID = m.next()
	m.bomOps[o.ID] = *o
	return nil
}

func (m *MemoryStore) BOMOperations(_ context.Context, _ platform.DBTX, bomID int64) ([]BOMOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BOMOperation
	for _, o := range m.bomOps {
		if o.BOMID == bomID {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func (m *MemoryStore) ReplaceMOOperations(_ context.Context, _ platform.DBTX, entityID int64, moID int64, ops []MOOperation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mo, ok := m.mos[moID]
	if !ok || mo.EntityID != entityID {
		return identity.ErrNotFound
	}
	for id, e := range m.moOps {
		if e.MOID == moID {
			delete(m.moOps, id)
		}
	}
	for i := range ops {
		ops[i].EntityID = entityID
		ops[i].MOID = moID
		ops[i].ID = m.next()
		ops[i].RowVersion = 1
		m.moOps[ops[i].ID] = ops[i]
	}
	return nil
}

func (m *MemoryStore) MOOperations(_ context.Context, _ platform.DBTX, moID int64) ([]MOOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MOOperation
	for _, o := range m.moOps {
		if o.MOID == moID {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func (m *MemoryStore) CompleteMOOperation(_ context.Context, _ platform.DBTX, entityID int64, moID int64, seq int32, actualMinutes int64) (MOOperation, error) {
	if actualMinutes < 0 {
		return MOOperation{}, fmt.Errorf("manufacturing: actual_minutes cannot be negative: %w", platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, o := range m.moOps {
		if o.MOID != moID || o.Seq != seq || o.EntityID != entityID {
			continue
		}
		if o.Status != MOOpPending {
			return MOOperation{}, fmt.Errorf("manufacturing: operation seq %d already completed: %w", seq, platform.ErrValidation)
		}
		o.Status = MOOpDone
		o.ActualMinutes = actualMinutes
		o.RowVersion++
		m.moOps[id] = o
		return o, nil
	}
	return MOOperation{}, identity.ErrNotFound
}

func (m *MemoryStore) OperationsByWorkstation(_ context.Context, _ platform.DBTX, entityID int64, workstationID int64, from, to time.Time) ([]MOOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lo := midnightUTC(from)
	hi := midnightUTC(to).AddDate(0, 0, 1)
	var out []MOOperation
	for _, o := range m.moOps {
		if o.EntityID != entityID || o.WorkstationID != workstationID {
			continue
		}
		if o.ScheduledStart.Before(hi) && o.ScheduledEnd.After(lo) {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ScheduledStart.Equal(out[j].ScheduledStart) {
			return out[i].Seq < out[j].Seq
		}
		return out[i].ScheduledStart.Before(out[j].ScheduledStart)
	})
	return out, nil
}
