package assets

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

const scheduleCols = `id, entity_id, asset_id, method, cost, rate_bps, start_date, periods, posted_periods, accumulated, status, created_at, updated_at, row_version`

func scanSchedule(row pgx.Row) (AssetSchedule, error) {
	var s AssetSchedule
	err := row.Scan(&s.ID, &s.EntityID, &s.AssetID, &s.Method, &s.Cost, &s.RateBps,
		&s.StartDate, &s.Periods, &s.PostedPeriods, &s.Accumulated, &s.Status,
		&s.CreatedAt, &s.UpdatedAt, &s.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return AssetSchedule{}, identity.ErrNotFound
	}
	return s, err
}

func (s *PGStore) CreateSchedule(ctx context.Context, db platform.DBTX, sc *AssetSchedule) error {
	if err := sc.Validate(); err != nil {
		return err
	}
	if sc.PostedPeriods != 0 || sc.Accumulated != 0 {
		return fmt.Errorf("assets: new schedules start unposted: %w", platform.ErrValidation)
	}
	var exists bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ferp_assets WHERE id=$1 AND entity_id=$2)`,
		sc.AssetID, sc.EntityID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return identity.ErrNotFound
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_asset_schedules
		(entity_id, asset_id, method, cost, rate_bps, start_date, periods)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, row_version`,
		sc.EntityID, sc.AssetID, sc.Method, sc.Cost, sc.RateBps, sc.StartDate, sc.Periods,
	).Scan(&sc.ID, &sc.RowVersion)
}

func (s *PGStore) ScheduleByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (AssetSchedule, error) {
	return scanSchedule(db.QueryRow(ctx, `SELECT `+scheduleCols+` FROM ferp_asset_schedules WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) SchedulesOfAsset(ctx context.Context, db platform.DBTX, entityID int64, assetID int64) ([]AssetSchedule, error) {
	rows, err := db.Query(ctx, `SELECT `+scheduleCols+` FROM ferp_asset_schedules
		WHERE entity_id=$1 AND asset_id=$2 ORDER BY id`, entityID, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssetSchedule
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// MarkSchedulePosted advances a schedule by one posted period (amount must be
// the plan amount for the next period; the service computes it).
func (s *PGStore) MarkSchedulePosted(ctx context.Context, db platform.DBTX, entityID int64, id int64, amount int64, rowVersion int64) (AssetSchedule, error) {
	sc, err := s.ScheduleByID(ctx, db, entityID, id)
	if err != nil {
		return AssetSchedule{}, err
	}
	if sc.RowVersion != rowVersion {
		return AssetSchedule{}, identity.ErrVersionConflict
	}
	if sc.Status != ScheduleActive {
		return AssetSchedule{}, fmt.Errorf("assets: schedule already complete: %w", platform.ErrValidation)
	}
	if sc.PostedPeriods >= sc.Periods {
		return AssetSchedule{}, fmt.Errorf("assets: all periods posted: %w", platform.ErrValidation)
	}
	if amount <= 0 || sc.Accumulated+amount > sc.Cost {
		return AssetSchedule{}, fmt.Errorf("assets: bad posted amount: %w", platform.ErrValidation)
	}
	status := ScheduleActive
	if sc.PostedPeriods+1 == sc.Periods {
		status = ScheduleDone
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_asset_schedules
		SET posted_periods=posted_periods+1, accumulated=accumulated+$1, status=$2,
		    updated_at=now(), row_version=row_version+1
		WHERE id=$3 AND entity_id=$4 AND row_version=$5`,
		amount, status, id, entityID, rowVersion)
	if err != nil {
		return AssetSchedule{}, err
	}
	if tag.RowsAffected() == 0 {
		return AssetSchedule{}, identity.ErrVersionConflict
	}
	sc.PostedPeriods++
	sc.Accumulated += amount
	sc.Status = int16(status)
	sc.RowVersion++
	return sc, nil
}

func (m *MemoryStore) CreateSchedule(_ context.Context, _ platform.DBTX, sc *AssetSchedule) error {
	if err := sc.Validate(); err != nil {
		return err
	}
	if sc.PostedPeriods != 0 || sc.Accumulated != 0 {
		return fmt.Errorf("assets: new schedules start unposted: %w", platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.assets[sc.AssetID]
	if !ok || a.EntityID != sc.EntityID {
		return identity.ErrNotFound
	}
	for _, e := range m.schedules {
		if e.EntityID == sc.EntityID && e.AssetID == sc.AssetID {
			return fmt.Errorf("assets: schedule already exists for asset: %w", platform.ErrConflict)
		}
	}
	sc.ID = m.next()
	sc.RowVersion = 1
	m.schedules[sc.ID] = *sc
	return nil
}

func (m *MemoryStore) ScheduleByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (AssetSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sc, ok := m.schedules[id]
	if !ok || sc.EntityID != entityID {
		return AssetSchedule{}, identity.ErrNotFound
	}
	return sc, nil
}

func (m *MemoryStore) SchedulesOfAsset(_ context.Context, _ platform.DBTX, entityID int64, assetID int64) ([]AssetSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AssetSchedule
	for _, sc := range m.schedules {
		if sc.EntityID == entityID && sc.AssetID == assetID {
			out = append(out, sc)
		}
	}
	return out, nil
}

func (m *MemoryStore) MarkSchedulePosted(_ context.Context, _ platform.DBTX, entityID int64, id int64, amount int64, rowVersion int64) (AssetSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sc, ok := m.schedules[id]
	if !ok || sc.EntityID != entityID {
		return AssetSchedule{}, identity.ErrNotFound
	}
	if sc.RowVersion != rowVersion {
		return AssetSchedule{}, identity.ErrVersionConflict
	}
	if sc.Status != ScheduleActive {
		return AssetSchedule{}, fmt.Errorf("assets: schedule already complete: %w", platform.ErrValidation)
	}
	if sc.PostedPeriods >= sc.Periods {
		return AssetSchedule{}, fmt.Errorf("assets: all periods posted: %w", platform.ErrValidation)
	}
	if amount <= 0 || sc.Accumulated+amount > sc.Cost {
		return AssetSchedule{}, fmt.Errorf("assets: bad posted amount: %w", platform.ErrValidation)
	}
	sc.PostedPeriods++
	sc.Accumulated += amount
	if sc.PostedPeriods == sc.Periods {
		sc.Status = ScheduleDone
	}
	sc.RowVersion++
	m.schedules[id] = sc
	return sc, nil
}
