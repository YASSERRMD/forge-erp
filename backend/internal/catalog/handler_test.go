package catalog

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func passthrough(_ string, _ string, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func testRouter() http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: NewMemoryStore()}, passthrough)
	})
	return r
}

func post(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestReceiveShipFlow(t *testing.T) {
	h := testRouter()

	prec := post(t, h, "/api/v1/products", map[string]any{
		"name": "Gadget", "sku": "GAD-1", "type": 0, "net_price": 500,
		"vat_rate_bps": 2000, "stock_tracked": true,
	})
	if prec.Code != http.StatusCreated {
		t.Fatalf("product: code=%d body=%s", prec.Code, prec.Body.String())
	}
	var p Product
	_ = json.NewDecoder(prec.Body).Decode(&p)

	wrec := post(t, h, "/api/v1/warehouses", map[string]any{"code": "MAIN", "label": "Main"})
	if wrec.Code != http.StatusCreated {
		t.Fatalf("warehouse: code=%d body=%s", wrec.Code, wrec.Body.String())
	}
	var wh Warehouse
	_ = json.NewDecoder(wrec.Body).Decode(&wh)

	// Duplicate SKU → 409.
	if dup := post(t, h, "/api/v1/products", map[string]any{"name": "Dupe", "sku": "GAD-1"}); dup.Code != http.StatusConflict {
		t.Fatalf("dupe sku: code=%d want 409", dup.Code)
	}

	// Receive 10 @ 400.
	mrec := post(t, h, "/api/v1/stock-movements", map[string]any{
		"product_id": p.ID, "warehouse_id": wh.ID, "qty": 10, "unit_cost": 400,
		"reason": "receipt", "ref": "RC-1",
	})
	if mrec.Code != http.StatusCreated {
		t.Fatalf("receipt: code=%d body=%s", mrec.Code, mrec.Body.String())
	}
	// Oversell → 422.
	over := post(t, h, "/api/v1/stock-movements", map[string]any{
		"product_id": p.ID, "warehouse_id": wh.ID, "qty": -11, "reason": "shipment", "ref": "SH-X",
	})
	if over.Code != http.StatusUnprocessableEntity {
		t.Fatalf("oversell: code=%d want 422 body=%s", over.Code, over.Body.String())
	}
	// Ship 4 → 200.
	ship := post(t, h, "/api/v1/stock-movements", map[string]any{
		"product_id": p.ID, "warehouse_id": wh.ID, "qty": -4, "reason": "shipment", "ref": "SH-1",
	})
	if ship.Code != http.StatusCreated {
		t.Fatalf("shipment: code=%d body=%s", ship.Code, ship.Body.String())
	}
	// Valuation: qty 6, value 2400, pmp 400.
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/stock-levels?product_id="+itoa(p.ID)+"&warehouse_id="+itoa(wh.ID), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("level: code=%d", rec.Code)
	}
	var out struct {
		Level StockLevel `json:"level"`
		PMP   int64      `json:"pmp"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if out.Level.Qty != 6 || out.Level.TotalValue != 2400 || out.PMP != 400 {
		t.Fatalf("valuation: %+v", out)
	}
}

func TestInventoryAdjust(t *testing.T) {
	h := testRouter()
	post(t, h, "/api/v1/products", map[string]any{"name": "Bolt", "sku": "BLT-1"})
	post(t, h, "/api/v1/warehouses", map[string]any{"code": "W2", "label": "Annex"})
	adj := post(t, h, "/api/v1/inventory-adjust", map[string]any{
		"product_id": 1, "warehouse_id": 1, "qty": 25, "unit_cost": 10, "ref": "COUNT-1",
	})
	if adj.Code != http.StatusCreated {
		t.Fatalf("adjust: code=%d body=%s", adj.Code, adj.Body.String())
	}
}
