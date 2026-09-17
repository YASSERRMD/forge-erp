package modulebuilder

import (
	"context"
	"fmt"
	"sync"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Activation is one ferp_modules row. It mirrors module.Record as a local
// seam: this package persists the same table through its own store so the
// platform/module package stays untouched.
type Activation struct {
	EntityID int64  `json:"entity_id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Version  string `json:"version"`
}

// Store is the persistence contract for activation state.
type Store interface {
	Get(ctx context.Context, db platform.DBTX, entityID int64, name string) (Activation, error)
	List(ctx context.Context, db platform.DBTX, entityID int64) ([]Activation, error)
	SetEnabled(ctx context.Context, db platform.DBTX, entityID int64, name string, enabled bool, version string) error
}

// PGStore implements Store against ferp_modules (migration 0025).
type PGStore struct{}

// NewPGStore builds a PGStore (stateless; the pool travels via DBTX args).
func NewPGStore() *PGStore { return &PGStore{} }

// Get returns one activation row (ErrNotFound when never set).
func (s *PGStore) Get(ctx context.Context, db platform.DBTX, entityID int64, name string) (Activation, error) {
	var a Activation
	if err := db.QueryRow(ctx,
		`SELECT entity_id, name, enabled, version FROM ferp_modules WHERE entity_id=$1 AND name=$2`,
		entityID, name).Scan(&a.EntityID, &a.Name, &a.Enabled, &a.Version); err != nil {
		return Activation{}, fmt.Errorf("modulebuilder: module %q: %w", name, platform.ErrNotFound)
	}
	return a, nil
}

// List returns every activation row for one entity.
func (s *PGStore) List(ctx context.Context, db platform.DBTX, entityID int64) ([]Activation, error) {
	rows, err := db.Query(ctx,
		`SELECT entity_id, name, enabled, version FROM ferp_modules WHERE entity_id=$1 ORDER BY name`,
		entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Activation
	for rows.Next() {
		var a Activation
		if err := rows.Scan(&a.EntityID, &a.Name, &a.Enabled, &a.Version); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetEnabled upserts one activation row.
func (s *PGStore) SetEnabled(ctx context.Context, db platform.DBTX, entityID int64, name string, enabled bool, version string) error {
	if entityID == 0 || name == "" {
		return fmt.Errorf("modulebuilder: entity and name required: %w", platform.ErrValidation)
	}
	_, err := db.Exec(ctx, `INSERT INTO ferp_modules (entity_id, name, enabled, version)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (entity_id, name) DO UPDATE SET enabled=EXCLUDED.enabled, version=EXCLUDED.version, updated_at=now()`,
		entityID, name, enabled, version)
	return err
}

// MemoryStore is an in-process fake for tests (no database required).
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Activation
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore { return &MemoryStore{rows: map[string]Activation{}} }

func memKey(entityID int64, name string) string { return fmt.Sprintf("%d\x00%s", entityID, name) }

// Get returns one row (ErrNotFound when never set).
func (m *MemoryStore) Get(_ context.Context, _ platform.DBTX, entityID int64, name string) (Activation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.rows[memKey(entityID, name)]
	if !ok {
		return Activation{}, fmt.Errorf("modulebuilder: module %q: %w", name, platform.ErrNotFound)
	}
	return a, nil
}

// List returns every row for one entity, sorted by name.
func (m *MemoryStore) List(_ context.Context, _ platform.DBTX, entityID int64) ([]Activation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Activation
	for _, a := range m.rows {
		if a.EntityID == entityID {
			out = append(out, a)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Name < out[j-1].Name; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// SetEnabled upserts one row.
func (m *MemoryStore) SetEnabled(_ context.Context, _ platform.DBTX, entityID int64, name string, enabled bool, version string) error {
	if entityID == 0 || name == "" {
		return fmt.Errorf("modulebuilder: entity and name required: %w", platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[memKey(entityID, name)] = Activation{EntityID: entityID, Name: name, Enabled: enabled, Version: version}
	return nil
}
