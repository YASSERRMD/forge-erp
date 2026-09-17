package incoterm

import (
	"context"
	"sort"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Store is the persistence contract for the incoterm code table.
type Store interface {
	List(ctx context.Context, db platform.DBTX) ([]Term, error)
	Get(ctx context.Context, db platform.DBTX, code string) (Term, error)
}

// PGStore serves the seeded ferp_incoterms table.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (s *PGStore) List(ctx context.Context, db platform.DBTX) ([]Term, error) {
	rows, err := db.Query(ctx, `SELECT code, label, mode FROM ferp_incoterms ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Term
	for rows.Next() {
		var t Term
		if err := rows.Scan(&t.Code, &t.Label, &t.Mode); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PGStore) Get(ctx context.Context, db platform.DBTX, code string) (Term, error) {
	var t Term
	if err := db.QueryRow(ctx, `SELECT code, label, mode FROM ferp_incoterms WHERE code=$1`,
		Normalize(code)).Scan(&t.Code, &t.Label, &t.Mode); err != nil {
		return Term{}, platform.ErrNotFound
	}
	return t, nil
}

// MemoryStore serves the static Table (handler tests; no persistence).
type MemoryStore struct {
	mu    sync.Mutex
	terms []Term
}

// NewMemoryStore builds a fake pre-seeded with Table.
func NewMemoryStore() *MemoryStore {
	cp := append([]Term(nil), Table...)
	return &MemoryStore{terms: cp}
}

func (m *MemoryStore) List(_ context.Context, _ platform.DBTX) ([]Term, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]Term(nil), m.terms...)
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}

func (m *MemoryStore) Get(_ context.Context, _ platform.DBTX, code string) (Term, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.terms {
		if t.Code == Normalize(code) {
			return t, nil
		}
	}
	return Term{}, platform.ErrNotFound
}
