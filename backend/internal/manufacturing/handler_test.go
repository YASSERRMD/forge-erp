package manufacturing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func testRouter() (http.Handler, *catalog.MemoryStore) {
	ledger := catalog.NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: NewMemoryStore(), Ledger: ledger}, passthrough)
	})
	return r, ledger
}

func doReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBOMMOProduceFlow(t *testing.T) {
	h, ledger := testRouter()
	ctx := t.Context()
	// Seed component stock: product 2 x100, product 3 x50 in warehouse 1.
	for _, mv := range []catalog.StockMovement{
		{EntityID: 1, ProductID: 2, WarehouseID: 1, Qty: 100, Reason: catalog.ReasonReceipt, Ref: "OPEN"},
		{EntityID: 1, ProductID: 3, WarehouseID: 1, Qty: 50, Reason: catalog.ReasonReceipt, Ref: "OPEN"},
	} {
		m := mv
		if _, err := ledger.AppendMovement(ctx, &m, false); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	rec := doReq(t, h, http.MethodPost, "/api/v1/manufacturing/boms",
		map[string]any{"ref": "BOM-1", "product_id": 1, "label": "Widget"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create BOM: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var bom BOM
	_ = json.NewDecoder(rec.Body).Decode(&bom)

	// Self-reference → 422.
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/manufacturing/boms/%d/lines", bom.ID),
		map[string]any{"component_id": 1, "qty": 1})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("self-ref: code=%d want 422", rec.Code)
	}
	for _, l := range []map[string]any{
		{"component_id": 2, "qty": 3},
		{"component_id": 3, "qty": 1},
	} {
		rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/manufacturing/boms/%d/lines", bom.ID), l)
		if rec.Code != http.StatusCreated {
			t.Fatalf("add line: code=%d body=%s", rec.Code, rec.Body.String())
		}
	}

	// MO for 10 units; walk to in-progress.
	rec = doReq(t, h, http.MethodPost, "/api/v1/manufacturing/mos",
		map[string]any{"ref": "MO-1", "bom_id": bom.ID, "product_id": 1, "warehouse_id": 1, "qty": 10})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create MO: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var mo ManufacturingOrder
	_ = json.NewDecoder(rec.Body).Decode(&mo)
	for _, st := range []int16{1, 2} {
		rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/manufacturing/mos/%d/status", mo.ID),
			map[string]any{"status": st, "row_version": mo.RowVersion})
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: code=%d body=%s", st, rec.Code, rec.Body.String())
		}
		_ = json.NewDecoder(rec.Body).Decode(&mo)
	}

	// Produce.
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/manufacturing/mos/%d/produce", mo.ID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("produce: code=%d body=%s", rec.Code, rec.Body.String())
	}
	lvl2, _ := ledger.Level(ctx, 2, 1)
	lvl3, _ := ledger.Level(ctx, 3, 1)
	lvl1, _ := ledger.Level(ctx, 1, 1)
	if lvl2.Qty != 70 || lvl3.Qty != 40 || lvl1.Qty != 10 {
		t.Fatalf("levels=%d/%d/%d want 70/40/10", lvl2.Qty, lvl3.Qty, lvl1.Qty)
	}

	// Second produce → 422 (already produced).
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/manufacturing/mos/%d/produce", mo.ID), nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("double produce: code=%d want 422", rec.Code)
	}

	// Oversized MO → 422 on insufficient stock.
	rec = doReq(t, h, http.MethodPost, "/api/v1/manufacturing/mos",
		map[string]any{"ref": "MO-2", "bom_id": bom.ID, "product_id": 1, "warehouse_id": 1, "qty": 1000})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create MO-2: code=%d", rec.Code)
	}
	var mo2 ManufacturingOrder
	_ = json.NewDecoder(rec.Body).Decode(&mo2)
	for _, st := range []int16{1, 2} {
		rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/manufacturing/mos/%d/status", mo2.ID),
			map[string]any{"status": st, "row_version": mo2.RowVersion})
		_ = json.NewDecoder(rec.Body).Decode(&mo2)
	}
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/manufacturing/mos/%d/produce", mo2.ID), nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-consume: code=%d want 422", rec.Code)
	}
}
