package module

import (
	"context"
	"fmt"
	"sync"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Record is one row of ferp_modules: per-entity activation state.
type Record struct {
	EntityID int64
	Name     string
	Enabled  bool
	Version  string
}

// Store is the persistence contract for activation state.
// PGStore implements it against PostgreSQL; MemoryStore is the test fake.
// Every method takes entityID: activation state is tenant-scoped.
type Store interface {
	Get(ctx context.Context, db platform.DBTX, entityID int64, name string) (Record, error)
	List(ctx context.Context, db platform.DBTX, entityID int64) ([]Record, error)
	SetEnabled(ctx context.Context, db platform.DBTX, entityID int64, name string, enabled bool, version string) error
}

// PGStore is the PostgreSQL implementation (table: ferp_modules, 0025).
type PGStore struct{}

// NewPGStore builds a PGStore (stateless; the pool travels via DBTX args).
func NewPGStore() *PGStore { return &PGStore{} }

func scanRecord(row interface{ Scan(...any) error }) (Record, error) {
	var r Record
	if err := row.Scan(&r.EntityID, &r.Name, &r.Enabled, &r.Version); err != nil {
		return Record{}, err
	}
	return r, nil
}

// Get returns one module's activation row (ErrNotFound when never set).
func (s *PGStore) Get(ctx context.Context, db platform.DBTX, entityID int64, name string) (Record, error) {
	r, err := scanRecord(db.QueryRow(ctx,
		`SELECT entity_id, name, enabled, version FROM ferp_modules WHERE entity_id=$1 AND name=$2`,
		entityID, name))
	if err != nil {
		return Record{}, fmt.Errorf("module %q: %w", name, platform.ErrNotFound)
	}
	return r, nil
}

// List returns every activation row for one entity.
func (s *PGStore) List(ctx context.Context, db platform.DBTX, entityID int64) ([]Record, error) {
	rows, err := db.Query(ctx,
		`SELECT entity_id, name, enabled, version FROM ferp_modules WHERE entity_id=$1 ORDER BY name`,
		entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.EntityID, &r.Name, &r.Enabled, &r.Version); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetEnabled upserts one activation row (empty names fail with ErrValidation).
func (s *PGStore) SetEnabled(ctx context.Context, db platform.DBTX, entityID int64, name string, enabled bool, version string) error {
	if entityID == 0 || name == "" {
		return fmt.Errorf("module: entity and name required: %w", platform.ErrValidation)
	}
	_, err := db.Exec(ctx, `INSERT INTO ferp_modules (entity_id, name, enabled, version)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (entity_id, name) DO UPDATE SET enabled=EXCLUDED.enabled, version=EXCLUDED.version, updated_at=now()`,
		entityID, name, enabled, version)
	return err
}

// MemoryStore is an in-process fake for handler tests (no database required).
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Record // key: entityID + "\x00" + name
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore { return &MemoryStore{rows: map[string]Record{}} }

func memKey(entityID int64, name string) string { return fmt.Sprintf("%d\x00%s", entityID, name) }

// Get returns one row (ErrNotFound when never set).
func (m *MemoryStore) Get(_ context.Context, _ platform.DBTX, entityID int64, name string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[memKey(entityID, name)]
	if !ok {
		return Record{}, fmt.Errorf("module %q: %w", name, platform.ErrNotFound)
	}
	return r, nil
}

// List returns every row for one entity, sorted by name.
func (m *MemoryStore) List(_ context.Context, _ platform.DBTX, entityID int64) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
	for _, r := range m.rows {
		if r.EntityID == entityID {
			out = append(out, r)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Name < out[j-1].Name; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// SetEnabled upserts one row (empty names / zero entity fail with ErrValidation).
func (m *MemoryStore) SetEnabled(_ context.Context, _ platform.DBTX, entityID int64, name string, enabled bool, version string) error {
	if entityID == 0 || name == "" {
		return fmt.Errorf("module: entity and name required: %w", platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[memKey(entityID, name)] = Record{EntityID: entityID, Name: name, Enabled: enabled, Version: version}
	return nil
}

// SeedRights grants rights into the existing ferp_rights table (migration
// 0002 — reused as-is, never recreated) for one group. Inserts are idempotent
// (ON CONFLICT DO NOTHING) so re-activation is safe. entityID scopes every
// row; groupID selects the target group (typically the entity's admin group).
func SeedRights(ctx context.Context, db platform.DBTX, entityID int64, groupID int64, rights []Right) error {
	if entityID == 0 || groupID == 0 {
		return fmt.Errorf("module: entity and group required: %w", platform.ErrValidation)
	}
	for _, r := range rights {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("module: %w: %w", err, platform.ErrValidation)
		}
		if _, err := db.Exec(ctx, `INSERT INTO ferp_rights (entity_id, user_id, group_id, module, entity, action)
			VALUES ($1,NULL,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
			entityID, groupID, r.Module, r.Entity, r.Action); err != nil {
			return err
		}
	}
	return nil
}

// Activate enables a registered module for one entity and seeds its rights
// catalogue into ferp_rights for groupID (nil groupID skips seeding — useful
// for modules whose rights admins grant by hand). Unknown modules fail with
// ErrNotFound.
func Activate(ctx context.Context, db platform.DBTX, st Store, reg *Registry, entityID int64, name, version string, groupID *int64) error {
	if entityID == 0 {
		return fmt.Errorf("module: entity required: %w", platform.ErrUnauthorized)
	}
	m, ok := reg.Get(name)
	if !ok {
		return fmt.Errorf("module %q: %w", name, platform.ErrNotFound)
	}
	if err := st.SetEnabled(ctx, db, entityID, name, true, version); err != nil {
		return err
	}
	if groupID != nil {
		if err := SeedRights(ctx, db, entityID, *groupID, m.Rights()); err != nil {
			return err
		}
	}
	return nil
}

// Deactivate disables a module for one entity (rights grants are left intact
// so re-activation restores access without re-granting).
func Deactivate(ctx context.Context, db platform.DBTX, st Store, entityID int64, name, version string) error {
	if entityID == 0 {
		return fmt.Errorf("module: entity required: %w", platform.ErrUnauthorized)
	}
	return st.SetEnabled(ctx, db, entityID, name, false, version)
}

// ModuleInfo is the SPA-facing shape behind GET /api/v1/modules: name, family
// and enabled flag let the frontend hide disabled modules; rights let it hide
// individual buttons.
type ModuleInfo struct {
	Name    string  `json:"name"`
	Family  Family  `json:"family"`
	Enabled bool    `json:"enabled"`
	Rights  []Right `json:"rights"`
}

// ListModules merges the registry catalogue with persisted ferp_modules state
// in dependency order. Modules without a stored row default to enabled, which
// preserves today's hand-wired behaviour (everything main.go mounts is live)
// until operators explicitly disable something.
func ListModules(ctx context.Context, db platform.DBTX, st Store, reg *Registry, entityID int64) ([]ModuleInfo, error) {
	if entityID == 0 {
		return nil, fmt.Errorf("module: entity required: %w", platform.ErrUnauthorized)
	}
	ordered, err := reg.Ordered()
	if err != nil {
		return nil, err
	}
	recs, err := st.List(ctx, db, entityID)
	if err != nil {
		return nil, err
	}
	state := make(map[string]Record, len(recs))
	for _, r := range recs {
		state[r.Name] = r
	}
	out := make([]ModuleInfo, 0, len(ordered))
	for _, m := range ordered {
		enabled := true
		if r, ok := state[m.Name()]; ok {
			enabled = r.Enabled
		}
		out = append(out, ModuleInfo{
			Name:    m.Name(),
			Family:  m.Family(),
			Enabled: enabled,
			Rights:  m.Rights(),
		})
	}
	return out, nil
}
