// Ledger benchmarks (Phase 6 perf baseline): 100 balanced entries across 3
// accounts, then time TrialBalance (the hot reporting read). PG bench skips
// without TEST_DATABASE_URL.
package finance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

// BenchmarkPGTrialBalance aggregates 100 posted entries (200 legs).
func BenchmarkPGTrialBalance(b *testing.B) {
	pool := pgtest.Pool(b)
	ctx := context.Background()
	st := NewPGStore(pool)
	j := &Journal{EntityID: 1, Code: "VEN", Label: "Sales"}
	if err := st.CreateJournal(ctx, pool, j); err != nil {
		b.Fatal(err)
	}
	mkAcct := func(code, label, typ string) int64 {
		a := &Account{EntityID: 1, Code: code, Label: label, Type: typ}
		if err := st.CreateAccount(ctx, pool, a); err != nil {
			b.Fatal(err)
		}
		return a.ID
	}
	dr, cr := mkAcct("411", "Clients", "asset"), mkAcct("701", "Sales", "revenue")
	for i := 0; i < 100; i++ {
		e := &Entry{EntityID: 1, JournalID: j.ID, Ref: fmt.Sprintf("B-%03d", i),
			Date: time.Now().UTC(), Lines: []EntryLine{
				{AccountID: dr, Label: "dr", Debit: 100},
				{AccountID: cr, Label: "cr", Credit: 100},
			}}
		if err := st.PostEntry(ctx, pool, e); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		trial, err := st.TrialBalance(ctx, pool, 1)
		if err != nil || len(trial) != 2 {
			b.Fatalf("trial=%v err=%v", trial, err)
		}
	}
}
