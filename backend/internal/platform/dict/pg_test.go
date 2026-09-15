package dict

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestPGCRUDLocaleAndCore(t *testing.T) {
	pool := pgtest.Pool(t)
	testStore(t, NewPGStore(pool), pool)
}

// TestPGSeedIdempotence applies the committed 0029 seed file twice against
// the migrated test schema: row counts must be identical (ON CONFLICT DO
// NOTHING) and the priority dictionaries must be present with core flags.
func TestPGSeedIdempotence(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "0029_dictionary_seed.up.sql"))
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	count := func() int64 {
		var n int64
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM ferp_dictionary_entries`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	before := count()
	if before == 0 {
		t.Fatal("migrated schema has no seed rows; 0029 did not apply")
	}
	if _, err := pool.Exec(ctx, string(raw)); err != nil {
		t.Fatalf("re-apply seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(raw)); err != nil {
		t.Fatalf("re-apply seed again: %v", err)
	}
	if after := count(); after != before {
		t.Fatalf("seed not idempotent: before=%d after=%d", before, after)
	}
	st := NewPGStore(pool)
	for _, dict := range []string{"country", "region", "currency", "payment_term",
		"payment_method", "vat_rate", "unit", "civility", "incoterm",
		"shipping_mode", "transport_mode"} {
		rows, err := st.List(ctx, pool, dict, "", false)
		if err != nil {
			t.Fatalf("list %s: %v", dict, err)
		}
		if len(rows) == 0 {
			t.Fatalf("seed dictionary %q is empty", dict)
		}
		for _, e := range rows {
			if !e.IsCore {
				t.Fatalf("seed row %s/%s is not core", dict, e.Code)
			}
		}
	}
	// Spot checks: locale-merge path works on seed data and bps are integers.
	fr, err := st.GetEntry(ctx, pool, "country", "FR")
	if err != nil {
		t.Fatalf("country FR: %v", err)
	}
	if fr.Label != "France" {
		t.Fatalf("country FR label: got %q", fr.Label)
	}
	vatRows, err := st.List(ctx, pool, "vat_rate", "", true)
	if err != nil || len(vatRows) == 0 {
		t.Fatalf("vat list: n=%d err=%v", len(vatRows), err)
	}
	seenBps := false
	for _, e := range vatRows {
		if bps, ok := e.Extra["rate_bps"].(float64); ok {
			if bps != float64(int64(bps)) {
				t.Fatalf("non-integral rate_bps on %s: %v", e.Code, bps)
			}
			seenBps = true
		}
	}
	if !seenBps {
		t.Fatal("no vat row carries rate_bps")
	}
	// Core seed rows are deactivate-only on PG too.
	if err := st.HardDeleteEntry(ctx, pool, "country", "FR"); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("hard-delete seeded core: got %v, want ErrValidation", err)
	}
}
