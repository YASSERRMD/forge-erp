package catalog

import (
	"context"
	"testing"
)

func TestMemoryStoreProductAndStock(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	p := &Product{EntityID: 1, SKU: "WID-1", Name: "Widget", Type: ProductGoods,
		NetPrice: 100, VATRateBps: 2000, Status: ProductActive, StockTracked: true}
	if err := st.CreateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateProduct(ctx, &Product{EntityID: 1, SKU: "WID-1", Name: "Dupe"}); err == nil {
		t.Fatal("duplicate SKU accepted")
	}
	w := &Warehouse{EntityID: 1, Code: "MAIN", Label: "Main", Status: 1}
	if err := st.CreateWarehouse(ctx, w); err != nil {
		t.Fatal(err)
	}

	// Receive then ship; oversell blocked.
	lvl, err := st.AppendMovement(ctx, &StockMovement{EntityID: 1, ProductID: p.ID,
		WarehouseID: w.ID, Qty: 10, UnitCost: 80, Reason: ReasonReceipt, Ref: "RC-1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if lvl.Qty != 10 || lvl.TotalValue != 800 {
		t.Fatalf("level: %+v", lvl)
	}
	if _, err := st.AppendMovement(ctx, &StockMovement{EntityID: 1, ProductID: p.ID,
		WarehouseID: w.ID, Qty: -4, Reason: ReasonShipment, Ref: "SH-1"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMovement(ctx, &StockMovement{EntityID: 1, ProductID: p.ID,
		WarehouseID: w.ID, Qty: -7, Reason: ReasonShipment, Ref: "SH-2"}, false); err == nil {
		t.Fatal("oversell accepted")
	}
	got, err := st.Level(ctx, p.ID, w.ID)
	if err != nil || got.Qty != 6 || got.TotalValue != 480 {
		t.Fatalf("level: %+v %v", got, err)
	}
	// Lot-tracked receipt.
	l := &Lot{EntityID: 1, ProductID: p.ID, Number: "LOT-7"}
	if err := st.CreateLot(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMovement(ctx, &StockMovement{EntityID: 1, ProductID: p.ID,
		WarehouseID: w.ID, LotID: &l.ID, Qty: 2, UnitCost: 80, Reason: ReasonReceipt}, false); err != nil {
		t.Fatal(err)
	}
}
