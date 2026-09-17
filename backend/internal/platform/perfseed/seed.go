// Package perfseed is a TEST-HELPER ONLY: deterministic generators that build
// a realistic-scale seeded dataset for performance baselining (latency
// targets: docs/OPERATIONS.md). It is pure data — no database, no handlers,
// no production wiring. Production code MUST NOT import this package; it
// exists so load tests and local profiling can materialize N orgs/products/
// docs without hand-writing fixtures.
package perfseed

import (
	"fmt"
	"math/rand"
)

// Scale describes how much data to generate.
type Scale struct {
	Orgs            int // tenants (ferp_entities beyond the seeded 'main')
	ProductsPerOrg  int
	DocsPerOrg      int // commercial headers (ferp_documents)
	LinesPerDoc     int
	UsersPerOrg     int
	StockMovesPerOrg int
}

// Small is a smoke scale for CI-adjacent runs (~seconds). Large mirrors the
// documented baseline target for p99 measurement.
func Small() Scale { return Scale{Orgs: 2, ProductsPerOrg: 50, DocsPerOrg: 20, LinesPerDoc: 3, UsersPerOrg: 5, StockMovesPerOrg: 50} }

// Baseline is the documented performance-baseline scale: 10 orgs x 1k
// products x 500 docs x 5 lines (see docs/OPERATIONS.md).
func Baseline() Scale {
	return Scale{Orgs: 10, ProductsPerOrg: 1000, DocsPerOrg: 500, LinesPerDoc: 5, UsersPerOrg: 25, StockMovesPerOrg: 2000}
}

// Counts reports the total rows per family a Scale materializes.
func (s Scale) Counts() map[string]int {
	return map[string]int{
		"orgs":        s.Orgs,
		"products":    s.Orgs * s.ProductsPerOrg,
		"docs":        s.Orgs * s.DocsPerOrg,
		"doc_lines":   s.Orgs * s.DocsPerOrg * s.LinesPerDoc,
		"users":       s.Orgs * s.UsersPerOrg,
		"stock_moves": s.Orgs * s.StockMovesPerOrg,
	}
}

// Org is one generated tenant's deterministic fixture identity.
type Org struct {
	Index int
	Code  string // e.g. perf-org-003
	Label string
}

// Product is one generated catalog row (money in minor units, never float).
type Product struct {
	OrgIndex  int
	SKU       string // deterministic: P-<org>-<seq>
	Label     string
	PriceMinor int64 // cents, deterministic pseudo-random 100..100000
	BaseQty   int    // integer base units (Dolibarr fractional quantities not ported)
}

// Doc is one generated commercial header (discriminated by Type).
type Doc struct {
	OrgIndex int
	Ref      string // deterministic: DOC-<org>-<seq>
	Type     string // quote|order|invoice (rotated)
	Status   string // draft|validated
	TotalMinor int64
	Lines    int
}

// Dataset is the generated fixture set. Generation is deterministic for a
// fixed Seed so baselines are comparable across runs.
type Dataset struct {
	Seed  int64
	Scale Scale
	Orgs  []Org
	Products []Product
	Docs  []Doc
}

// Generate builds a deterministic dataset: same (seed, scale) => same rows.
func Generate(seed int64, s Scale) Dataset {
	rng := rand.New(rand.NewSource(seed))
	types := []string{"quote", "order", "invoice"}
	d := Dataset{Seed: seed, Scale: s}
	for o := 0; o < s.Orgs; o++ {
		d.Orgs = append(d.Orgs, Org{Index: o,
			Code:  fmt.Sprintf("perf-org-%03d", o),
			Label: fmt.Sprintf("Perf Org %03d", o)})
		for p := 0; p < s.ProductsPerOrg; p++ {
			d.Products = append(d.Products, Product{
				OrgIndex:   o,
				SKU:        fmt.Sprintf("P-%03d-%06d", o, p),
				Label:      fmt.Sprintf("Perf Product %03d-%06d", o, p),
				PriceMinor: 100 + rng.Int63n(99900),
				BaseQty:    1 + rng.Intn(100),
			})
		}
		for g := 0; g < s.DocsPerOrg; g++ {
			ty := types[g%len(types)]
			status := "draft"
			if g%2 == 1 {
				status = "validated"
			}
			d.Docs = append(d.Docs, Doc{
				OrgIndex:   o,
				Ref:        fmt.Sprintf("DOC-%03d-%06d", o, g),
				Type:       ty,
				Status:     status,
				TotalMinor: 1000 + rng.Int63n(900000),
				Lines:      s.LinesPerDoc,
			})
		}
	}
	return d
}
