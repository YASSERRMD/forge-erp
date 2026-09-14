// Package assets implements physical asset tracking (Dolibarr asset module
// with workstations covered as an asset kind): serialized equipment with a
// service lifecycle and warehouse assignment.
package assets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Asset status.
type AssetStatus int16

const (
	AssetInService   AssetStatus = 1
	AssetMaintenance AssetStatus = 2
	AssetRetired     AssetStatus = 0
)

// Asset kinds (workstation included as a kind).
var assetKinds = map[string]bool{
	"equipment": true, "workstation": true, "vehicle": true, "it": true,
}

// Asset is one tracked item.
type Asset struct {
	ID          int64       `json:"id"`
	EntityID    int64       `json:"entity_id"`
	Code        string      `json:"code"` // unique per entity
	Label       string      `json:"label"`
	Kind        string      `json:"kind"`
	ProductID   *int64      `json:"product_id"`
	Serial      string      `json:"serial"`
	WarehouseID *int64      `json:"warehouse_id"`
	Status      AssetStatus `json:"status"`
	AcquiredAt  *time.Time  `json:"acquired_at"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	RowVersion  int64       `json:"row_version"`
}

// Validate checks asset invariants.
func (a Asset) Validate() error {
	if a.EntityID <= 0 {
		return fmt.Errorf("assets: entity_id required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(a.Code) == "" || strings.TrimSpace(a.Label) == "" {
		return fmt.Errorf("assets: code and label required: %w", platform.ErrValidation)
	}
	if !assetKinds[a.Kind] {
		return fmt.Errorf("assets: unknown kind (equipment|workstation|vehicle|it): %w", platform.ErrValidation)
	}
	return nil
}

// CanTransition reports whether an asset status change is legal.
func (a Asset) CanTransition(to AssetStatus) bool {
	switch a.Status {
	case AssetInService:
		return to == AssetMaintenance || to == AssetRetired
	case AssetMaintenance:
		return to == AssetInService || to == AssetRetired
	default:
		return false
	}
}

// Store is the persistence contract for assets.
type Store interface {
	CreateAsset(ctx context.Context, db platform.DBTX, a *Asset) error
	AssetByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Asset, error)
	UpdateAsset(ctx context.Context, db platform.DBTX, entityID int64, id int64, label, serial string, warehouseID *int64, rowVersion int64) (Asset, error)
	ListAssets(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Asset, error)
	SetAssetStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to AssetStatus, rowVersion int64) (Asset, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const assetCols = `id, entity_id, code, label, kind, product_id, serial, warehouse_id, status, acquired_at, created_at, updated_at, row_version`

func scanAsset(row pgx.Row) (Asset, error) {
	var a Asset
	err := row.Scan(&a.ID, &a.EntityID, &a.Code, &a.Label, &a.Kind, &a.ProductID,
		&a.Serial, &a.WarehouseID, &a.Status, &a.AcquiredAt,
		&a.CreatedAt, &a.UpdatedAt, &a.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, identity.ErrNotFound
	}
	return a, err
}

func (s *PGStore) CreateAsset(ctx context.Context, db platform.DBTX, a *Asset) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_assets
		(entity_id, code, label, kind, product_id, serial, warehouse_id, status, acquired_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id, row_version`,
		a.EntityID, a.Code, a.Label, a.Kind, a.ProductID, a.Serial,
		a.WarehouseID, a.Status, a.AcquiredAt,
	).Scan(&a.ID, &a.RowVersion)
}

func (s *PGStore) AssetByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Asset, error) {
	return scanAsset(db.QueryRow(ctx, `SELECT `+assetCols+` FROM ferp_assets WHERE id=$1 AND entity_id=$2`, id, entityID))
}

// UpdateAsset edits a non-retired asset's label, serial and warehouse.
func (s *PGStore) UpdateAsset(ctx context.Context, db platform.DBTX, entityID int64, id int64, label, serial string, warehouseID *int64, rowVersion int64) (Asset, error) {
	a, err := s.AssetByID(ctx, db, entityID, id)
	if err != nil {
		return Asset{}, err
	}
	if a.RowVersion != rowVersion {
		return Asset{}, identity.ErrVersionConflict
	}
	if a.Status == AssetRetired {
		return Asset{}, fmt.Errorf("assets: retired assets are frozen: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(label) == "" {
		return Asset{}, fmt.Errorf("assets: label required: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_assets SET label=$1, serial=$2, warehouse_id=$3,
		updated_at=now(), row_version=row_version+1 WHERE id=$4 AND entity_id=$5 AND row_version=$6`,
		label, serial, warehouseID, id, entityID, rowVersion)
	if err != nil {
		return Asset{}, err
	}
	if tag.RowsAffected() == 0 {
		return Asset{}, identity.ErrVersionConflict
	}
	a.Label = label
	a.Serial = serial
	a.WarehouseID = warehouseID
	a.RowVersion++
	return a, nil
}

func (s *PGStore) ListAssets(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Asset, error) {
	rows, err := db.Query(ctx, `SELECT `+assetCols+` FROM ferp_assets
		WHERE entity_id=$1 ORDER BY code LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PGStore) SetAssetStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to AssetStatus, rowVersion int64) (Asset, error) {
	a, err := s.AssetByID(ctx, db, entityID, id)
	if err != nil {
		return Asset{}, err
	}
	if a.RowVersion != rowVersion {
		return Asset{}, identity.ErrVersionConflict
	}
	if !a.CanTransition(to) {
		return Asset{}, fmt.Errorf("assets: illegal transition: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_assets SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND entity_id=$3 AND row_version=$4`, to, id, entityID, rowVersion)
	if err != nil {
		return Asset{}, err
	}
	if tag.RowsAffected() == 0 {
		return Asset{}, identity.ErrVersionConflict
	}
	a.Status = to
	a.RowVersion++
	return a, nil
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu     sync.Mutex
	seq    int64
	assets map[int64]Asset
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{assets: map[int64]Asset{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateAsset(_ context.Context, _ platform.DBTX, a *Asset) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.assets {
		if e.EntityID == a.EntityID && e.Code == a.Code {
			return fmt.Errorf("assets: duplicate code: %w", platform.ErrConflict)
		}
	}
	a.ID = m.next()
	a.RowVersion = 1
	m.assets[a.ID] = *a
	return nil
}

func (m *MemoryStore) AssetByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (Asset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.assets[id]
	if !ok || a.EntityID != entityID {
		return Asset{}, identity.ErrNotFound
	}
	return a, nil
}

func (m *MemoryStore) UpdateAsset(_ context.Context, _ platform.DBTX, entityID int64, id int64, label, serial string, warehouseID *int64, rowVersion int64) (Asset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.assets[id]
	if !ok || a.EntityID != entityID {
		return Asset{}, identity.ErrNotFound
	}
	if a.RowVersion != rowVersion {
		return Asset{}, identity.ErrVersionConflict
	}
	if a.Status == AssetRetired {
		return Asset{}, fmt.Errorf("assets: retired assets are frozen: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(label) == "" {
		return Asset{}, fmt.Errorf("assets: label required: %w", platform.ErrValidation)
	}
	a.Label = label
	a.Serial = serial
	a.WarehouseID = warehouseID
	a.RowVersion++
	m.assets[id] = a
	return a, nil
}

func (m *MemoryStore) ListAssets(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]Asset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Asset
	for _, a := range m.assets {
		if a.EntityID == entityID {
			out = append(out, a)
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

func (m *MemoryStore) SetAssetStatus(_ context.Context, _ platform.DBTX, entityID int64, id int64, to AssetStatus, rowVersion int64) (Asset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.assets[id]
	if !ok || a.EntityID != entityID {
		return Asset{}, identity.ErrNotFound
	}
	if a.RowVersion != rowVersion {
		return Asset{}, identity.ErrVersionConflict
	}
	if !a.CanTransition(to) {
		return Asset{}, fmt.Errorf("assets: illegal transition: %w", platform.ErrValidation)
	}
	a.Status = to
	a.RowVersion++
	m.assets[id] = a
	return a, nil
}
