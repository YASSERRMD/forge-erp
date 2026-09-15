package field

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Store is the persistence contract for field definitions.
type Store interface {
	CreateDefinition(ctx context.Context, db platform.DBTX, d *Definition) error
	GetDefinition(ctx context.Context, db platform.DBTX, entityID int64, scope, key string) (Definition, error)
	ListDefinitions(ctx context.Context, db platform.DBTX, entityID int64, scope string) ([]Definition, error)
	DeleteDefinition(ctx context.Context, db platform.DBTX, entityID int64, scope, key string) error
}

// PGStore implements Store against PostgreSQL (methods run on db so callers
// can join ambient transactions; no pool is retained).
type PGStore struct{}

// NewPGStore builds a PG-backed definition store.
func NewPGStore() *PGStore { return &PGStore{} }

const defCols = `id, entity_id, scope, key, type, label, required, options,
	validation_rule, display_order, searchable`

func scanDefinition(row pgx.Row) (Definition, error) {
	var d Definition
	var opts []byte
	err := row.Scan(&d.ID, &d.EntityID, &d.Scope, &d.Key, &d.Type, &d.Label,
		&d.Required, &opts, &d.ValidationRule, &d.DisplayOrder, &d.Searchable)
	if errors.Is(err, pgx.ErrNoRows) {
		return Definition{}, platform.ErrNotFound
	}
	if err != nil {
		return Definition{}, err
	}
	d.Options = []string{}
	if err := json.Unmarshal(opts, &d.Options); err != nil {
		return Definition{}, fmt.Errorf("field: decode options: %w", err)
	}
	return d, nil
}

// CreateDefinition inserts a definition (validates rules; duplicates wrap
// platform.ErrConflict).
func (s *PGStore) CreateDefinition(ctx context.Context, db platform.DBTX, d *Definition) error {
	if err := ValidateDefinition(*d); err != nil {
		return err
	}
	opts, _ := json.Marshal(nullableOptions(d.Options))
	err := db.QueryRow(ctx, `INSERT INTO ferp_field_defs
		(entity_id, scope, key, type, label, required, options, validation_rule, display_order, searchable)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		d.EntityID, d.Scope, d.Key, string(d.Type), d.Label, d.Required,
		opts, d.ValidationRule, d.DisplayOrder, d.Searchable,
	).Scan(&d.ID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("field: duplicate definition %s.%s: %w", d.Scope, d.Key, platform.ErrConflict)
		}
		return err
	}
	return nil
}

func nullableOptions(opts []string) []string {
	if opts == nil {
		return []string{}
	}
	return opts
}

// GetDefinition fetches one definition (entity-scoped; misses wrap
// platform.ErrNotFound).
func (s *PGStore) GetDefinition(ctx context.Context, db platform.DBTX, entityID int64, scope, key string) (Definition, error) {
	return scanDefinition(db.QueryRow(ctx, `SELECT `+defCols+` FROM ferp_field_defs
		WHERE entity_id=$1 AND scope=$2 AND key=$3`, entityID, scope, key))
}

// ListDefinitions pages an entity's definitions; empty scope lists all scopes,
// ordered for stable display.
func (s *PGStore) ListDefinitions(ctx context.Context, db platform.DBTX, entityID int64, scope string) ([]Definition, error) {
	var rows pgx.Rows
	var err error
	if scope == "" {
		rows, err = db.Query(ctx, `SELECT `+defCols+` FROM ferp_field_defs
			WHERE entity_id=$1 ORDER BY scope, display_order, key`, entityID)
	} else {
		rows, err = db.Query(ctx, `SELECT `+defCols+` FROM ferp_field_defs
			WHERE entity_id=$1 AND scope=$2 ORDER BY display_order, key`, entityID, scope)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Definition
	for rows.Next() {
		d, err := scanDefinition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDefinition removes one definition (misses wrap platform.ErrNotFound).
func (s *PGStore) DeleteDefinition(ctx context.Context, db platform.DBTX, entityID int64, scope, key string) error {
	tag, err := db.Exec(ctx, `DELETE FROM ferp_field_defs
		WHERE entity_id=$1 AND scope=$2 AND key=$3`, entityID, scope, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("field: definition %s.%s: %w", scope, key, platform.ErrNotFound)
	}
	return nil
}

// MemoryStore is the in-process fake for offline tests.
type MemoryStore struct {
	mu   sync.Mutex
	seq  int64
	defs map[string]Definition // entityID + scope + key
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore { return &MemoryStore{defs: map[string]Definition{}} }

func defMapKey(entityID int64, scope, key string) string {
	return fmt.Sprintf("%d\x00%s\x00%s", entityID, scope, key)
}

// CreateDefinition inserts a definition (same validation/conflict rules as PG).
func (m *MemoryStore) CreateDefinition(_ context.Context, _ platform.DBTX, d *Definition) error {
	if err := ValidateDefinition(*d); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := defMapKey(d.EntityID, d.Scope, d.Key)
	if _, ok := m.defs[k]; ok {
		return fmt.Errorf("field: duplicate definition %s.%s: %w", d.Scope, d.Key, platform.ErrConflict)
	}
	m.seq++
	d.ID = m.seq
	d.Options = append([]string(nil), d.Options...)
	m.defs[k] = *d
	return nil
}

// GetDefinition fetches one definition (entity-scoped).
func (m *MemoryStore) GetDefinition(_ context.Context, _ platform.DBTX, entityID int64, scope, key string) (Definition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.defs[defMapKey(entityID, scope, key)]
	if !ok {
		return Definition{}, fmt.Errorf("field: definition %s.%s: %w", scope, key, platform.ErrNotFound)
	}
	return d, nil
}

// ListDefinitions pages an entity's definitions; empty scope lists all.
func (m *MemoryStore) ListDefinitions(_ context.Context, _ platform.DBTX, entityID int64, scope string) ([]Definition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Definition
	for _, d := range m.defs {
		if d.EntityID != entityID {
			continue
		}
		if scope != "" && d.Scope != scope {
			continue
		}
		out = append(out, d)
	}
	// Stable order: scope, display_order, key (insertion sort; lists are tiny).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && defLess(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

func defLess(a, b Definition) bool {
	if a.Scope != b.Scope {
		return a.Scope < b.Scope
	}
	if a.DisplayOrder != b.DisplayOrder {
		return a.DisplayOrder < b.DisplayOrder
	}
	return a.Key < b.Key
}

// DeleteDefinition removes one definition.
func (m *MemoryStore) DeleteDefinition(_ context.Context, _ platform.DBTX, entityID int64, scope, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := defMapKey(entityID, scope, key)
	if _, ok := m.defs[k]; !ok {
		return fmt.Errorf("field: definition %s.%s: %w", scope, key, platform.ErrNotFound)
	}
	delete(m.defs, k)
	return nil
}
