package catalog

import (
	"context"
	"testing"
)

func TestVariantBarcodeRules(t *testing.T) {
	if err := CheckBarcode("5901234123457"); err != nil { // valid EAN-13
		t.Fatalf("valid EAN-13 rejected: %v", err)
	}
	if err := CheckBarcode("5901234123450"); err == nil {
		t.Error("bad check digit accepted")
	}
	if err := CheckBarcode("CODE128-XYZ"); err != nil {
		t.Fatalf("opaque symbology rejected: %v", err)
	}
	ctx := context.Background()
	m := NewMemoryStore()
	p := &Product{EntityID: 1, SKU: "BASE-1", Name: "Base", Type: ProductGoods,
		Status: ProductActive}
	if err := m.CreateProduct(ctx, p); err != nil {
		t.Fatalf("product: %v", err)
	}
	v := &Variant{EntityID: 1, ProductID: p.ID, SKU: "BASE-1-L",
		Attributes: map[string]any{"size": "L"}, PriceDelta: 100, Barcode: "5901234123457"}
	if err := m.CreateVariant(ctx, v); err != nil {
		t.Fatalf("variant: %v", err)
	}
	if err := m.CreateVariant(ctx, &Variant{EntityID: 1, ProductID: p.ID,
		SKU: "BASE-1-L"}); err == nil {
		t.Error("duplicate variant SKU accepted")
	}
	if err := m.CreateVariant(ctx, &Variant{EntityID: 1, ProductID: p.ID,
		SKU: "BASE-1-X", Barcode: "5901234123450"}); err == nil {
		t.Error("bad barcode accepted")
	}
	if err := m.CreateVariant(ctx, &Variant{EntityID: 1, ProductID: p.ID,
		SKU: "BASE-1-S", Barcode: ""}); err != nil {
		t.Fatalf("barcode-less variant: %v", err)
	}
	list, err := m.VariantsOf(ctx, p.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("variants=%d err=%v", len(list), err)
	}
}
