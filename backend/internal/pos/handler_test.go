package pos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func testRouter() (http.Handler, *catalog.MemoryStore) {
	ledger := catalog.NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: NewMemoryStore(), Catalog: ledger, Sales: sales.NewMemoryStore()}, passthrough)
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

func seedGoods(t *testing.T, ledger *catalog.MemoryStore) {
	t.Helper()
	ctx := context.Background()
	p := &catalog.Product{EntityID: 1, SKU: "WID-001", Name: "Widget",
		Type: catalog.ProductGoods, Unit: "unit", NetPrice: 1000, VATRateBps: 2000,
		Status: catalog.ProductActive, StockTracked: true}
	if err := ledger.CreateProduct(ctx, p); err != nil {
		t.Fatalf("create product: %v", err)
	}
	mv := &catalog.StockMovement{EntityID: 1, ProductID: p.ID, WarehouseID: 1,
		Qty: 10, Reason: catalog.ReasonReceipt, Ref: "OPEN"}
	if _, err := ledger.AppendMovement(ctx, mv, false); err != nil {
		t.Fatalf("seed stock: %v", err)
	}
}

func openTill(t *testing.T, h http.Handler) Session {
	t.Helper()
	rec := doReq(t, h, http.MethodPost, "/api/v1/pos/terminals",
		map[string]any{"code": "TILL-1", "label": "Main till", "warehouse_id": 1})
	if rec.Code != http.StatusCreated {
		t.Fatalf("terminal: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var term Terminal
	_ = json.NewDecoder(rec.Body).Decode(&term)
	rec = doReq(t, h, http.MethodPost, "/api/v1/pos/sessions",
		map[string]any{"terminal_id": term.ID, "cashier": "ada", "opening_float": 5000})
	if rec.Code != http.StatusCreated {
		t.Fatalf("session: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var se Session
	_ = json.NewDecoder(rec.Body).Decode(&se)
	return se
}

func TestCheckoutFlow(t *testing.T) {
	h, ledger := testRouter()
	seedGoods(t, ledger)
	se := openTill(t, h)

	// 2 x 1000 net + 20% VAT = 2400 gross; tender 3000 → change 600.
	rec := doReq(t, h, http.MethodPost, "/api/v1/pos/checkout", map[string]any{
		"session_id": se.ID, "org_id": 7, "method": "cash", "tendered": 3000,
		"lines": []map[string]any{{"product_id": 1, "qty": 2}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("checkout: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var sa Sale
	_ = json.NewDecoder(rec.Body).Decode(&sa)
	if sa.TotalGross != 2400 || sa.Change != 600 || sa.InvoiceID == 0 {
		t.Fatalf("sale=%+v want gross 2400 change 600 + invoice", sa)
	}
	lvl, _ := ledger.Level(context.Background(), 1, 1)
	if lvl.Qty != 8 {
		t.Fatalf("stock=%d want 8", lvl.Qty)
	}

	// Under-tender → 422.
	rec = doReq(t, h, http.MethodPost, "/api/v1/pos/checkout", map[string]any{
		"session_id": se.ID, "org_id": 7, "method": "cash", "tendered": 100,
		"lines": []map[string]any{{"product_id": 1, "qty": 1}},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("under-tender: code=%d want 422", rec.Code)
	}

	// Over-stock → 422.
	rec = doReq(t, h, http.MethodPost, "/api/v1/pos/checkout", map[string]any{
		"session_id": se.ID, "org_id": 7, "method": "card", "tendered": 999999,
		"lines": []map[string]any{{"product_id": 1, "qty": 999}},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-stock: code=%d want 422", rec.Code)
	}

	// Anonymous sale → 422.
	rec = doReq(t, h, http.MethodPost, "/api/v1/pos/checkout", map[string]any{
		"session_id": se.ID, "method": "cash", "tendered": 5000,
		"lines": []map[string]any{{"product_id": 1, "qty": 1}},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("anonymous: code=%d want 422", rec.Code)
	}

	// Void then double-void.
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/pos/sales/%d/void", sa.ID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("void: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/pos/sales/%d/void", sa.ID), nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("double void: code=%d want 422", rec.Code)
	}

	// Close session, then checkout must fail.
	rec = doReq(t, h, http.MethodPost, fmt.Sprintf("/api/v1/pos/sessions/%d/close", se.ID),
		map[string]any{"row_version": se.RowVersion})
	if rec.Code != http.StatusOK {
		t.Fatalf("close: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/pos/checkout", map[string]any{
		"session_id": se.ID, "org_id": 7, "method": "cash", "tendered": 5000,
		"lines": []map[string]any{{"product_id": 1, "qty": 1}},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("closed session: code=%d want 422", rec.Code)
	}
}

func TestWalkinCheckout(t *testing.T) {
	ledger := catalog.NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: NewMemoryStore(), Catalog: ledger, Sales: sales.NewMemoryStore(), WalkinOrg: 9}, passthrough)
	})
	seedGoods(t, ledger)
	se := openTillOn(t, r)
	rec := doReq(t, r, http.MethodPost, "/api/v1/pos/checkout", map[string]any{
		"session_id": se.ID, "method": "cash", "tendered": 5000,
		"lines": []map[string]any{{"product_id": 1, "qty": 1}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("walk-in: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var sa Sale
	_ = json.NewDecoder(rec.Body).Decode(&sa)
	if sa.OrgID != 9 {
		t.Fatalf("walk-in org=%d want 9", sa.OrgID)
	}
}

func openTillOn(t *testing.T, h http.Handler) Session {
	t.Helper()
	rec := doReq(t, h, http.MethodPost, "/api/v1/pos/terminals",
		map[string]any{"code": "TILL-W", "label": "Walk-in till", "warehouse_id": 1})
	var term Terminal
	_ = json.NewDecoder(rec.Body).Decode(&term)
	rec = doReq(t, h, http.MethodPost, "/api/v1/pos/sessions",
		map[string]any{"terminal_id": term.ID, "cashier": "ada"})
	var se Session
	_ = json.NewDecoder(rec.Body).Decode(&se)
	return se
}

func TestMultiTenderCheckout(t *testing.T) {
	h, ledger := testRouter()
	seedGoods(t, ledger)
	se := openTill(t, h)
	// 1 x 1000 net + 20% = 1200 gross; cash 1000 + card 500 → change 300.
	rec := doReq(t, h, http.MethodPost, "/api/v1/pos/checkout", map[string]any{
		"session_id": se.ID, "org_id": 7,
		"lines":      []map[string]any{{"product_id": 1, "qty": 1}},
		"payments": []map[string]any{
			{"method": "cash", "amount": 1000},
			{"method": "card", "amount": 500},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("multi-tender: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var sa Sale
	_ = json.NewDecoder(rec.Body).Decode(&sa)
	if sa.TotalGross != 1200 || sa.Change != 300 || sa.Method != "mixed" {
		t.Fatalf("sale=%+v", sa)
	}
	// Bad leg rejected.
	rec = doReq(t, h, http.MethodPost, "/api/v1/pos/checkout", map[string]any{
		"session_id": se.ID, "org_id": 7,
		"lines":   []map[string]any{{"product_id": 1, "qty": 1}},
		"payments": []map[string]any{{"method": "crypto", "amount": 5000}},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad leg: code=%d want 422", rec.Code)
	}
}

func TestReturnSaleFlow(t *testing.T) {
	h, ledger := testRouter()
	seedGoods(t, ledger)
	se := openTill(t, h)
	// Checkout 2 units: 2400 gross, fully paid.
	rec := doReq(t, h, http.MethodPost, "/api/v1/pos/checkout", map[string]any{
		"session_id": se.ID, "org_id": 7, "method": "cash", "tendered": 5000,
		"lines": []map[string]any{{"product_id": 1, "qty": 2}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("checkout: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var sa Sale
	_ = json.NewDecoder(rec.Body).Decode(&sa)
	lvl, _ := ledger.Level(context.Background(), 1, 1)
	if lvl.Qty != 8 {
		t.Fatalf("after sale stock=%d want 8", lvl.Qty)
	}
	// Return in full: credit note + restock + returned marker.
	rec = doReq(t, h, http.MethodPost, "/api/v1/pos/returns", map[string]any{"sale_id": sa.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("return: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Sale       Sale `json:"sale"`
		CreditNote struct {
			Totals struct {
				Gross int64 `json:"gross"`
			} `json:"totals"`
		} `json:"credit_note"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if out.Sale.Status != SaleReturned {
		t.Fatalf("sale status=%d want returned", out.Sale.Status)
	}
	if out.CreditNote.Totals.Gross != 2400 {
		t.Fatalf("credit gross=%d want 2400", out.CreditNote.Totals.Gross)
	}
	lvl, _ = ledger.Level(context.Background(), 1, 1)
	if lvl.Qty != 10 {
		t.Fatalf("after return stock=%d want 10", lvl.Qty)
	}
	// Second return rejected.
	rec = doReq(t, h, http.MethodPost, "/api/v1/pos/returns", map[string]any{"sale_id": sa.ID})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("double return: code=%d want 422", rec.Code)
	}
}
