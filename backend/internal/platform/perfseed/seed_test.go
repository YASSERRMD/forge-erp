package perfseed

import (
	"testing"
)

func TestCounts(t *testing.T) {
	s := Scale{Orgs: 2, ProductsPerOrg: 3, DocsPerOrg: 4, LinesPerDoc: 5, UsersPerOrg: 6, StockMovesPerOrg: 7}
	c := s.Counts()
	want := map[string]int{"orgs": 2, "products": 6, "docs": 8, "doc_lines": 40, "users": 12, "stock_moves": 14}
	for k, v := range want {
		if c[k] != v {
			t.Errorf("counts[%s] = %d, want %d", k, c[k], v)
		}
	}
}

func TestGenerateDeterministic(t *testing.T) {
	a := Generate(42, Small())
	b := Generate(42, Small())
	if len(a.Products) != len(b.Products) || len(a.Docs) != len(b.Docs) {
		t.Fatal("same seed must produce same sizes")
	}
	for i := range a.Products {
		if a.Products[i] != b.Products[i] {
			t.Fatalf("product %d differs across runs", i)
		}
	}
	c := Generate(43, Small())
	if len(c.Products) > 0 && a.Products[0].PriceMinor == c.Products[0].PriceMinor &&
		a.Products[1].PriceMinor == c.Products[1].PriceMinor {
		t.Fatal("different seeds should (almost surely) differ")
	}
}

func TestGenerateInvariants(t *testing.T) {
	d := Generate(7, Small())
	c := Small().Counts()
	if len(d.Orgs) != c["orgs"] || len(d.Products) != c["products"] || len(d.Docs) != c["docs"] {
		t.Fatalf("size mismatch: %+v", c)
	}
	seenSKU := map[string]bool{}
	for _, p := range d.Products {
		if seenSKU[p.SKU] {
			t.Fatalf("duplicate SKU %s", p.SKU)
		}
		seenSKU[p.SKU] = true
		if p.PriceMinor < 0 {
			t.Fatalf("negative price %s", p.SKU)
		}
	}
	seenRef := map[string]bool{}
	for _, g := range d.Docs {
		if seenRef[g.Ref] {
			t.Fatalf("duplicate ref %s", g.Ref)
		}
		seenRef[g.Ref] = true
		switch g.Type {
		case "quote", "order", "invoice":
		default:
			t.Fatalf("bad doc type %s", g.Type)
		}
	}
}

func TestBaselineScale(t *testing.T) {
	c := Baseline().Counts()
	if c["products"] != 10000 || c["docs"] != 5000 || c["doc_lines"] != 25000 {
		t.Fatalf("baseline counts drifted: %+v", c)
	}
	d := Generate(1, Scale{Orgs: 1, ProductsPerOrg: 2, DocsPerOrg: 2, LinesPerDoc: 1})
	if len(d.Products) != 2 || len(d.Docs) != 2 {
		t.Fatalf("tiny generate failed: %+v", d)
	}
}
