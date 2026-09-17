package incoterm

import (
	"context"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Service is the thin orchestration over the code table: reads delegate to
// the store (PG seed), while Validate is static so document validation
// (sales, later procurement) never needs a database round-trip.
type Service struct {
	Store Store
	DB    platform.DBTX
}

// NewService wires the service (DB may be nil in memory-only use).
func NewService(store Store, db platform.DBTX) *Service {
	return &Service{Store: store, DB: db}
}

// List returns the seeded terms.
func (s *Service) List(ctx context.Context) ([]Term, error) {
	return s.Store.List(ctx, s.DB)
}

// Get fetches one term by code (case-insensitive).
func (s *Service) Get(ctx context.Context, code string) (Term, error) {
	return s.Store.Get(ctx, s.DB, code)
}

// Validate checks code against the static Incoterms 2020 set.
func (s *Service) Validate(code string) error {
	return Validate(code)
}
