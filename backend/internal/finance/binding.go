// Accounting bindings (Phase 2): explicit (kind, key) → account map that
// future auto-posting resolves through instead of hardcoded accounts.
// Dolibarr scatters these defaults per module (product/customer/supplier/VAT/
// bank screens); here they live in one table (ferp_account_bindings) behind
// ResolveAccount. This file is purely additive: it extends PGStore with new
// methods and adds a standalone memory fake, touching no existing finance code.
package finance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Binding kinds (stored verbatim in ferp_account_bindings.kind).
const (
	BindingProduct = "product" // key: product SKU
	BindingVAT     = "vat"     // key: VAT rate in bps, e.g. "2000"
	BindingBank    = "bank"    // key: bank account code
	BindingPartner = "partner" // key: partner/org reference
	BindingExpense = "expense" // key: expense category code
)

// Binding maps one (kind, key) pair to a chart-of-accounts row.
type Binding struct {
	ID        int64  `json:"id"`
	EntityID  int64  `json:"entity_id"`
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	AccountID int64  `json:"account_id"`
}

// Validate checks binding invariants.
func (b Binding) Validate() error {
	if b.EntityID <= 0 {
		return errors.New("finance: binding requires an entity")
	}
	switch b.Kind {
	case BindingProduct, BindingVAT, BindingBank, BindingPartner, BindingExpense:
	default:
		return fmt.Errorf("finance: bad binding kind %q", b.Kind)
	}
	if strings.TrimSpace(b.Key) == "" {
		return errors.New("finance: binding key required")
	}
	if b.AccountID <= 0 {
		return errors.New("finance: binding requires an account")
	}
	return nil
}

// BindingStore is the persistence contract for account bindings, kept
// separate from Store so Phase 3 auto-posting can depend on this narrow
// seam. *PGStore implements it (below); MemoryBindingStore is the fake.
type BindingStore interface {
	// SetBinding creates or replaces the binding for (entity, kind, key).
	SetBinding(ctx context.Context, db platform.DBTX, b *Binding) error
	// Binding fetches one binding (ErrNotFound outside entity / missing).
	Binding(ctx context.Context, db platform.DBTX, entityID int64, kind, key string) (Binding, error)
	// ListBindings lists an entity's bindings, optionally filtered by kind (""
	// = all).
	ListBindings(ctx context.Context, db platform.DBTX, entityID int64, kind string) ([]Binding, error)
	// ResolveAccount returns the account for (entity, kind, key), or an
	// ErrNotFound-wrapping error when unmapped. Phase 3 auto-posting calls
	// this instead of hardcoding accounts.
	ResolveAccount(ctx context.Context, db platform.DBTX, entityID int64, kind, key string) (int64, error)
}

func scanBinding(row pgx.Row) (Binding, error) {
	var b Binding
	err := row.Scan(&b.ID, &b.EntityID, &b.Kind, &b.Key, &b.AccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Binding{}, ErrNotFound
	}
	return b, err
}

// SetBinding upserts a binding on the PG store.
func (s *PGStore) SetBinding(ctx context.Context, db platform.DBTX, b *Binding) error {
	if err := b.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_account_bindings (entity_id, kind, key, account_id)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (entity_id, kind, key) DO UPDATE SET account_id=EXCLUDED.account_id
		RETURNING id`, b.EntityID, b.Kind, b.Key, b.AccountID).Scan(&b.ID)
}

// Binding fetches one binding on the PG store.
func (s *PGStore) Binding(ctx context.Context, db platform.DBTX, entityID int64, kind, key string) (Binding, error) {
	return scanBinding(db.QueryRow(ctx, `SELECT id, entity_id, kind, key, account_id
		FROM ferp_account_bindings WHERE entity_id=$1 AND kind=$2 AND key=$3`,
		entityID, kind, key))
}

// ListBindings lists bindings on the PG store.
func (s *PGStore) ListBindings(ctx context.Context, db platform.DBTX, entityID int64, kind string) ([]Binding, error) {
	var rows pgx.Rows
	var err error
	if kind == "" {
		rows, err = db.Query(ctx, `SELECT id, entity_id, kind, key, account_id
			FROM ferp_account_bindings WHERE entity_id=$1 ORDER BY kind, key`, entityID)
	} else {
		rows, err = db.Query(ctx, `SELECT id, entity_id, kind, key, account_id
			FROM ferp_account_bindings WHERE entity_id=$1 AND kind=$2 ORDER BY key`, entityID, kind)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Binding
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ResolveAccount resolves (entity, kind, key) to an account id on the PG store.
func (s *PGStore) ResolveAccount(ctx context.Context, db platform.DBTX, entityID int64, kind, key string) (int64, error) {
	b, err := s.Binding(ctx, db, entityID, kind, key)
	if err != nil {
		return 0, fmt.Errorf("finance: no account bound for %s/%s: %w", kind, key, err)
	}
	return b.AccountID, nil
}

// MemoryBindingStore is the in-process fake for binding tests and handler
// tests. It is a separate struct (rather than fields on MemoryStore) so this
// file stays additive over the existing finance store.
type MemoryBindingStore struct {
	mu   sync.Mutex
	seq  int64
	byID map[int64]Binding
}

// NewMemoryBindingStore builds an empty fake.
func NewMemoryBindingStore() *MemoryBindingStore {
	return &MemoryBindingStore{byID: map[int64]Binding{}}
}

// SetBinding creates or replaces a binding on the memory fake.
func (m *MemoryBindingStore) SetBinding(_ context.Context, _ platform.DBTX, b *Binding) error {
	if err := b.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.byID {
		if e.EntityID == b.EntityID && e.Kind == b.Kind && e.Key == b.Key {
			b.ID = id
			m.byID[id] = *b
			return nil
		}
	}
	m.seq++
	b.ID = m.seq
	m.byID[b.ID] = *b
	return nil
}

// Binding fetches one binding on the memory fake.
func (m *MemoryBindingStore) Binding(_ context.Context, _ platform.DBTX, entityID int64, kind, key string) (Binding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.byID {
		if b.EntityID == entityID && b.Kind == kind && b.Key == key {
			return b, nil
		}
	}
	return Binding{}, ErrNotFound
}

// ListBindings lists bindings on the memory fake.
func (m *MemoryBindingStore) ListBindings(_ context.Context, _ platform.DBTX, entityID int64, kind string) ([]Binding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Binding
	for _, b := range m.byID {
		if b.EntityID == entityID && (kind == "" || b.Kind == kind) {
			out = append(out, b)
		}
	}
	return out, nil
}

// ResolveAccount resolves (entity, kind, key) on the memory fake.
func (m *MemoryBindingStore) ResolveAccount(ctx context.Context, db platform.DBTX, entityID int64, kind, key string) (int64, error) {
	b, err := m.Binding(ctx, db, entityID, kind, key)
	if err != nil {
		return 0, fmt.Errorf("finance: no account bound for %s/%s: %w", kind, key, err)
	}
	return b.AccountID, nil
}
