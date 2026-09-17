package dynprice

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestPGRulePersist(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	cst := catalog.NewPGStore(pool)
	st := NewPGStore(pool)
	p := &catalog.Product{EntityID: 1, SKU: "DP-PG-001", Name: "Widget",
		Type: catalog.ProductGoods, Unit: "unit", NetPrice: 1000, VATRateBps: 0,
		Status: catalog.ProductActive, StockTracked: true}
	if err := cst.CreateProduct(ctx, pool, p); err != nil {
		t.Fatal(err)
	}
	r := &Rule{EntityID: 1, Code: "PG-TEN", Label: "pg", Expression: "max(base * 0.9, cost)"}
	if err := st.CreateRule(ctx, pool, r); err != nil {
		t.Fatalf("create: %v", err)
	}
	if r.ID == 0 {
		t.Fatal("ID unset")
	}
	got, err := st.RuleByCode(ctx, pool, 1, "PG-TEN")
	if err != nil || got.Expression != r.Expression {
		t.Fatalf("by-code=%+v err=%v", got, err)
	}
	a := &Assignment{EntityID: 1, RuleID: r.ID, ProductID: p.ID, OrgID: 0}
	if err := st.Assign(ctx, pool, a); err != nil {
		t.Fatalf("assign: %v", err)
	}
	list, err := st.AssignmentsFor(ctx, pool, 1, p.ID, 9)
	if err != nil || len(list) != 1 {
		t.Fatalf("assignments=%+v err=%v", list, err)
	}
}
