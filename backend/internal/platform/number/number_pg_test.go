package number_test

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/number"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

// seqSuffix parses the trailing "-<digits>" sequence from either scheme.
func seqSuffix(t *testing.T, ref string) int64 {
	t.Helper()
	i := strings.LastIndex(ref, "-")
	if i < 0 {
		t.Fatalf("ref %q has no sequence suffix", ref)
	}
	n, err := strconv.ParseInt(ref[i+1:], 10, 64)
	if err != nil {
		t.Fatalf("ref %q seq parse: %v", ref, err)
	}
	return n
}

// runConcurrent allocates n refs with 50-way-style fan-out: every goroutine
// runs Next inside its OWN transaction (the caller's tx), then commits —
// proving allocation never begins its own tx and stays gap-free under commit.
func runConcurrent(t *testing.T, code, docType string, n int) []string {
	t.Helper()
	ctx := context.Background()
	pool := pgtest.Pool(t)
	entity := pgtest.NewEntity(t, pool, fmt.Sprintf("num_%s_%d", docType, time.Now().UnixNano()%1000000))
	m, err := number.ByCode(code)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	refs := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tx, err := pool.Begin(ctx)
			if err != nil {
				errs[i] = err
				return
			}
			ref, err := m.Next(ctx, tx, entity, docType, at)
			if err != nil {
				_ = tx.Rollback(ctx)
				errs[i] = err
				return
			}
			if err := tx.Commit(ctx); err != nil {
				errs[i] = err
				return
			}
			refs[i] = ref
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	return refs
}

func assertGapFree(t *testing.T, refs []string, n int) {
	t.Helper()
	if len(refs) != n {
		t.Fatalf("refs = %d want %d", len(refs), n)
	}
	seen := map[string]bool{}
	seqs := make([]int64, 0, n)
	for _, r := range refs {
		if r == "" {
			t.Fatal("empty ref allocated")
		}
		if seen[r] {
			t.Fatalf("duplicate ref %q", r)
		}
		seen[r] = true
		seqs = append(seqs, seqSuffix(t, r))
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for i, s := range seqs {
		if want := int64(i + 1); s != want {
			t.Fatalf("gap: seqs[%d] = %d want %d (all: %v)", i, s, want, seqs)
		}
	}
}

// TestConcurrentGapFreeStandard: 50 goroutines on one entity/type via the
// standard model — 50 distinct sequential refs, no gaps. PG-gated (skips
// locally when TEST_DATABASE_URL is unset).
func TestConcurrentGapFreeStandard(t *testing.T) {
	refs := runConcurrent(t, "standard", "invoice", 50)
	assertGapFree(t, refs, 50)
	for _, r := range refs {
		if !strings.HasPrefix(r, "INV-202609-") {
			t.Fatalf("standard ref %q must match PREFIX-YYYYMM-####", r)
		}
	}
}

// TestConcurrentGapFreeMercure: same guarantee for the mercure running
// sequence (separate doctype so counters never collide with the standard run).
func TestConcurrentGapFreeMercure(t *testing.T) {
	refs := runConcurrent(t, "mercure", "order", 50)
	assertGapFree(t, refs, 50)
	for _, r := range refs {
		if !strings.HasPrefix(r, "MC-ORD-") {
			t.Fatalf("mercure ref %q must match MC-<PREFIX>-<entity>-<seq>", r)
		}
	}
}
