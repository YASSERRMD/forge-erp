// Store benchmarks (Phase 6 perf baseline): dataset sizes are fixed and
// recorded so numbers stay comparable across runs. PG benches skip without
// TEST_DATABASE_URL (same convention as pgtest); CI runs them.
package partners

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

// BenchmarkPGListOrgs pages 50 of 200 seeded orgs (hot master-data read).
func BenchmarkPGListOrgs(b *testing.B) {
	pool := pgtest.Pool(b)
	ctx := context.Background()
	st := NewPGStore(pool)
	for i := 0; i < 200; i++ {
		o := &Organization{EntityID: 1, Name: fmt.Sprintf("Bench %03d", i),
			IsCustomer: true, CustomerCode: fmt.Sprintf("B-%03d", i)}
		if err := st.CreateOrg(ctx, pool, o); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		list, err := st.ListOrgs(ctx, pool, 1, 50, 0)
		if err != nil || len(list) != 50 {
			b.Fatalf("list=%d err=%v", len(list), err)
		}
	}
}

// BenchmarkOrgListHTTP serves GET /organizations (50 of 200) through the
// chi router on the memory store: handler + JSON cost, no database.
func BenchmarkOrgListHTTP(b *testing.B) {
	st := NewMemoryStore()
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		o := &Organization{EntityID: 1,
			Name:         fmt.Sprintf("Bench HTTP Org Number %03d With A Longish Name For Payload Weight", i),
			IsCustomer:   true,
			CustomerCode: fmt.Sprintf("H-%03d", i)}
		if err := st.CreateOrg(ctx, nil, o); err != nil {
			b.Fatal(err)
		}
	}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Store: st}, passthrough) })
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations?limit=50", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("code=%d", rec.Code)
		}
	}
}
