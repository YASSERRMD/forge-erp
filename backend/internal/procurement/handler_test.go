package procurement

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func contextBackground() context.Context { return context.Background() }

func passthrough(_ string, _ string, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func testRouter() (http.Handler, *catalog.MemoryStore) {
	sst := NewMemoryStore()
	cst := catalog.NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: sst, Catalog: cst}, passthrough)
	})
	return r, cst
}

func post(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestProcureToPayChain(t *testing.T) {
	h, cst := testRouter()
	ctx := contextBackground()

	// Seed supplier price + stock scaffolding.
	p := &catalog.Product{EntityID: 1, SKU: "STL-1", Name: "Steel", NetPrice: 6000,
		Status: catalog.ProductActive, StockTracked: true}
	_ = cst.CreateProduct(ctx, p)
	wh := &catalog.Warehouse{EntityID: 1, Code: "MAIN", Label: "Main", Status: 1}
	_ = cst.CreateWarehouse(ctx, wh)
	if rec := post(t, h, "/api/v1/purchase/prices", map[string]any{
		"product_id": p.ID, "org_id": 9, "unit_net": 6000, "currency": "USD",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("price: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Off-contract order line → 422.
	off := post(t, h, "/api/v1/purchase/documents", map[string]any{
		"type": "supplier_order", "org_id": 9, "currency": "USD",
		"lines": []map[string]any{{"product_id": p.ID, "label": "Steel", "qty": 10, "unit_net": 6100}},
	})
	if off.Code != http.StatusUnprocessableEntity {
		t.Fatalf("off-contract: code=%d want 422", off.Code)
	}

	// On-contract order → approve → validate → reception → receive → invoice → pay.
	orec := post(t, h, "/api/v1/purchase/documents", map[string]any{
		"type": "supplier_order", "org_id": 9, "currency": "USD",
		"lines": []map[string]any{{"product_id": p.ID, "label": "Steel", "qty": 10, "unit_net": 6000, "vat_rate_bps": 2000}},
	})
	if orec.Code != http.StatusCreated {
		t.Fatalf("order: code=%d body=%s", orec.Code, orec.Body.String())
	}
	var ord Document
	_ = json.NewDecoder(orec.Body).Decode(&ord)
	// Above threshold (72000 > 50000): validation without approval → 422.
	if bad := post(t, h, "/api/v1/purchase/documents/"+itoa(ord.ID)+"/status", map[string]any{"to": Validated}); bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unapproved: code=%d want 422", bad.Code)
	}
	if ap := post(t, h, "/api/v1/purchase/documents/"+itoa(ord.ID)+"/approve", map[string]any{}); ap.Code != http.StatusOK {
		t.Fatalf("approve: code=%d body=%s", ap.Code, ap.Body.String())
	}
	if ok := post(t, h, "/api/v1/purchase/documents/"+itoa(ord.ID)+"/status", map[string]any{"to": Validated}); ok.Code != http.StatusOK {
		t.Fatalf("validate: code=%d body=%s", ok.Code, ok.Body.String())
	}

	rrec := post(t, h, "/api/v1/purchase/documents/"+itoa(ord.ID)+"/convert", map[string]any{"to": "reception"})
	var rcv Document
	_ = json.NewDecoder(rrec.Body).Decode(&rcv)
	if st := post(t, h, "/api/v1/purchase/documents/"+itoa(rcv.ID)+"/status", map[string]any{"to": Validated}); st.Code != http.StatusOK {
		t.Fatalf("reception validate: code=%d", st.Code)
	}
	recv := post(t, h, "/api/v1/purchase/receptions/"+itoa(rcv.ID)+"/receive", map[string]any{
		"lines": []map[string]any{{"product_id": p.ID, "warehouse_id": wh.ID, "qty": 10, "unit_cost": 6000}},
	})
	if recv.Code != http.StatusOK {
		t.Fatalf("receive: code=%d body=%s", recv.Code, recv.Body.String())
	}
	lvl, _ := cst.Level(ctx, p.ID, wh.ID)
	if lvl.Qty != 10 || lvl.TotalValue != 60000 {
		t.Fatalf("stock: %+v", lvl)
	}

	irec := post(t, h, "/api/v1/purchase/documents/"+itoa(rcv.ID)+"/convert", map[string]any{"to": "supplier_invoice"})
	var inv Document
	_ = json.NewDecoder(irec.Body).Decode(&inv)
	if st := post(t, h, "/api/v1/purchase/documents/"+itoa(inv.ID)+"/status", map[string]any{"to": Validated}); st.Code != http.StatusOK {
		t.Fatalf("invoice validate: code=%d", st.Code)
	}
	pay := post(t, h, "/api/v1/purchase/payments", map[string]any{
		"org_id": 9, "amount": inv.Totals.Gross, "currency": "USD", "invoice_ids": []int64{inv.ID},
	})
	if pay.Code != http.StatusCreated {
		t.Fatalf("pay: code=%d body=%s", pay.Code, pay.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/purchase/documents/"+itoa(inv.ID), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var got Document
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got.Status != Paid {
		t.Fatalf("supplier invoice status = %d want paid", got.Status)
	}
}
