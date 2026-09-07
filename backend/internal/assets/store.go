// Package assets implements physical asset tracking (Dolibarr asset module
// with workstations covered as an asset kind): serialized equipment with a
// service lifecycle and warehouse assignment.
package assets

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
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
		return errors.New("assets: entity_id required")
	}
	if strings.TrimSpace(a.Code) == "" || strings.TrimSpace(a.Label) == "" {
		return errors.New("assets: code and label required")
	}
	if !assetKinds[a.Kind] {
		return errors.New("assets: unknown kind (equipment|workstation|vehicle|it)")
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
	CreateAsset(ctx context.Context, a *Asset) error
	AssetByID(ctx context.Context, id int64) (Asset, error)
	ListAssets(ctx context.Context, entityID int64, limit, offset int) ([]Asset, error)
	SetAssetStatus(ctx context.Context, id int64, to AssetStatus, rowVersion int64) (Asset, error)
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

func (s *PGStore) CreateAsset(ctx context.Context, a *Asset) error {
	if err := a.Validate(); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_assets
		(entity_id, code, label, kind, product_id, serial, warehouse_id, status, acquired_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id, row_version`,
		a.EntityID, a.Code, a.Label, a.Kind, a.ProductID, a.Serial,
		a.WarehouseID, a.Status, a.AcquiredAt,
	).Scan(&a.ID, &a.RowVersion)
}

func (s *PGStore) AssetByID(ctx context.Context, id int64) (Asset, error) {
	return scanAsset(s.pool.QueryRow(ctx, `SELECT `+assetCols+` FROM ferp_assets WHERE id=$1`, id))
}

func (s *PGStore) ListAssets(ctx context.Context, entityID int64, limit, offset int) ([]Asset, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+assetCols+` FROM ferp_assets
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

func (s *PGStore) SetAssetStatus(ctx context.Context, id int64, to AssetStatus, rowVersion int64) (Asset, error) {
	a, err := s.AssetByID(ctx, id)
	if err != nil {
		return Asset{}, err
	}
	if a.RowVersion != rowVersion {
		return Asset{}, identity.ErrVersionConflict
	}
	if !a.CanTransition(to) {
		return Asset{}, errors.New("assets: illegal transition")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_assets SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
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

func (m *MemoryStore) CreateAsset(_ context.Context, a *Asset) error {
	if err := a.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.assets {
		if e.EntityID == a.EntityID && e.Code == a.Code {
			return errors.New("assets: duplicate code")
		}
	}
	a.ID = m.next()
	a.RowVersion = 1
	m.assets[a.ID] = *a
	return nil
}

func (m *MemoryStore) AssetByID(_ context.Context, id int64) (Asset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.assets[id]
	if !ok {
		return Asset{}, identity.ErrNotFound
	}
	return a, nil
}

func (m *MemoryStore) ListAssets(_ context.Context, entityID int64, limit, offset int) ([]Asset, error) {
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

func (m *MemoryStore) SetAssetStatus(_ context.Context, id int64, to AssetStatus, rowVersion int64) (Asset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.assets[id]
	if !ok {
		return Asset{}, identity.ErrNotFound
	}
	if a.RowVersion != rowVersion {
		return Asset{}, identity.ErrVersionConflict
	}
	if !a.CanTransition(to) {
		return Asset{}, errors.New("assets: illegal transition")
	}
	a.Status = to
	a.RowVersion++
	m.assets[id] = a
	return a, nil
}
