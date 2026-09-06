package catalog

import (
	"testing"
)

func TestProductValidate(t *testing.T) {
	ok := Product{EntityID: 1, SKU: "WID-001", Name: "Widget", Type: ProductGoods,
		NetPrice: 1000, VATRateBps: 2000, Status: ProductActive, StockTracked: true}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	// 1000 + 20% = 1200.
	if g := ok.GrossPrice(); g != 1200 {
		t.Fatalf("gross = %d want 1200", g)
	}
	// Half-up: 199 + 20% = 238.8 → 239.
	ok.NetPrice = 199
	if g := ok.GrossPrice(); g != 239 {
		t.Fatalf("gross rounding = %d want 239", g)
	}
	bads := []Product{
		{SKU: "bad sku!", Name: "x"},
		{SKU: "OK-1"},
		{SKU: "OK-1", Name: "x", Type: 9},
		{SKU: "OK-1", Name: "x", NetPrice: -1},
		{SKU: "OK-1", Name: "x", VATRateBps: 10001},
		{SKU: "OK-1", Name: "x", Type: ProductService, StockTracked: true},
	}
	for i, b := range bads {
		if err := b.Validate(); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestApplyReceiptShipment(t *testing.T) {
	var level StockLevel
	var err error
	// Receive 10 @ 100 → qty 10, value 1000.
	level, err = Apply(level, StockMovement{ProductID: 1, WarehouseID: 1, Qty: 10, UnitCost: 100, Reason: ReasonReceipt}, false)
	if err != nil {
		t.Fatal(err)
	}
	if level.Qty != 10 || level.TotalValue != 1000 || level.PMP() != 100 {
		t.Fatalf("after receipt: %+v", level)
	}
	// Receive 10 @ 200 → PMP 150.
	level, err = Apply(level, StockMovement{ProductID: 1, WarehouseID: 1, Qty: 10, UnitCost: 200, Reason: ReasonReceipt}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p := level.PMP(); p != 150 {
		t.Fatalf("PMP = %d want 150", p)
	}
	// Ship 5 → value relieved at PMP: 3000 - 750 = 2250, qty 15.
	level, err = Apply(level, StockMovement{ProductID: 1, WarehouseID: 1, Qty: -5, Reason: ReasonShipment}, false)
	if err != nil {
		t.Fatal(err)
	}
	if level.Qty != 15 || level.TotalValue != 2250 {
		t.Fatalf("after shipment: %+v", level)
	}
	// Oversell blocked.
	if _, err := Apply(level, StockMovement{ProductID: 1, WarehouseID: 1, Qty: -16, Reason: ReasonShipment}, false); err == nil {
		t.Fatal("oversell accepted with guard on")
	}
	// Allowed when configured (Dolibarr negative-stock option).
	neg, err := Apply(level, StockMovement{ProductID: 1, WarehouseID: 1, Qty: -16, Reason: ReasonShipment}, true)
	if err != nil {
		t.Fatal(err)
	}
	if neg.Qty != -1 {
		t.Fatalf("negative level: %+v", neg)
	}
	// Drain to zero resets value.
	zero, err := Apply(StockLevel{Qty: 2, TotalValue: 301}, StockMovement{ProductID: 1, WarehouseID: 1, Qty: -2, Reason: ReasonShipment}, false)
	if err != nil {
		t.Fatal(err)
	}
	if zero.Qty != 0 || zero.TotalValue != 0 {
		t.Fatalf("drain: %+v", zero)
	}
}

func TestMovementValidate(t *testing.T) {
	if err := (StockMovement{ProductID: 1, WarehouseID: 1, Qty: 1, Reason: ReasonReceipt}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (StockMovement{ProductID: 1, WarehouseID: 1, Reason: ReasonReceipt}).Validate(); err == nil {
		t.Error("zero qty accepted")
	}
	if err := (StockMovement{ProductID: 1, WarehouseID: 1, Qty: 1, Reason: "teleport"}).Validate(); err == nil {
		t.Error("unknown reason accepted")
	}
}

func TestLotValidate(t *testing.T) {
	if err := (Lot{EntityID: 1, ProductID: 3, Number: "LOT-42"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Lot{EntityID: 1, Number: "LOT-42"}).Validate(); err == nil {
		t.Error("product-less lot accepted")
	}
}
