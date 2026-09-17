package stocktransfer

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestPGTransferPersist(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	cst := catalog.NewPGStore(pool)
	st := NewPGStore(pool)

	p := &catalog.Product{EntityID: 1, SKU: "TR-PG-001", Name: "Widget",
		Type: catalog.ProductGoods, Unit: "unit", NetPrice: 1000, VATRateBps: 0,
		Status: catalog.ProductActive, StockTracked: true}
	if err := cst.CreateProduct(ctx, pool, p); err != nil {
		t.Fatal(err)
	}
	mkWh := func(code string) int64 {
		w := &catalog.Warehouse{EntityID: 1, Code: code, Label: code, Status: 1}
		if err := cst.CreateWarehouse(ctx, pool, w); err != nil {
			t.Fatal(err)
		}
		return w.ID
	}
	src, dst := mkWh("TRPG-S"), mkWh("TRPG-D")

	tr := &Transfer{EntityID: 1, SourceWarehouseID: src, DestWarehouseID: dst, Note: "pg"}
	if err := st.Create(ctx, pool, tr, "202609"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if tr.ID == 0 || tr.Ref == "" {
		t.Fatalf("ref not minted: %+v", tr)
	}
	l := &TransferLine{ProductID: p.ID, Qty: 2, UnitCost: 50}
	if err := st.AddLine(ctx, pool, 1, tr.ID, l); err != nil {
		t.Fatalf("line: %v", err)
	}
	lines, err := st.LinesOf(ctx, pool, 1, tr.ID)
	if err != nil || len(lines) != 1 || lines[0].Qty != 2 {
		t.Fatalf("lines=%+v err=%v", lines, err)
	}
	done, err := st.SetStatus(ctx, pool, 1, tr.ID, StatusValidated, tr.RowVersion)
	if err != nil || done.Status != StatusValidated {
		t.Fatalf("status=%+v err=%v", done, err)
	}
}
