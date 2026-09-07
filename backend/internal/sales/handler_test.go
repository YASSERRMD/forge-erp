package sales

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
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func cstContext() context.Context { return context.Background() }

func passthrough(_ string, _ string, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func testRouter() (http.Handler, *MemoryStore, *catalog.MemoryStore) {
	sst := NewMemoryStore()
	cst := catalog.NewMemoryStore()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: sst, Catalog: cst}, passthrough)
	})
	return r, sst, cst
}

func post(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func createDoc(t *testing.T, h http.Handler, dtype documents.DocType, org int64) Document {
	t.Helper()
	rec := post(t, h, "/api/v1/sales/documents", map[string]any{
		"type": dtype, "org_id": org, "currency": "USD",
		"lines": []map[string]any{
			{"product_id": 1, "label": "Widget", "qty": 2, "unit_net": 500, "vat_rate_bps": 2000},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %s: code=%d body=%s", dtype, rec.Code, rec.Body.String())
	}
	var d Document
	_ = json.NewDecoder(rec.Body).Decode(&d)
	if d.Ref == "" || d.Totals.Gross != 1200 {
		t.Fatalf("doc: %+v", d)
	}
	return d
}

func setStatus(t *testing.T, h http.Handler, id int64, to int16) Document {
	t.Helper()
	rec := post(t, h, "/api/v1/sales/documents/"+strconv.FormatInt(id, 10)+"/status",
		map[string]any{"to": to})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: code=%d body=%s", to, rec.Code, rec.Body.String())
	}
	var d Document
	_ = json.NewDecoder(rec.Body).Decode(&d)
	return d
}

func TestQuoteToCashChain(t *testing.T) {
	h, _, cst := testRouter()

	// Stock the widget first (catalog contexts share no state here; seed directly).
	ctx := cstContext()
	p := &catalog.Product{EntityID: 1, SKU: "W-1", Name: "Widget", NetPrice: 500,
		Status: catalog.ProductActive, StockTracked: true}
	_ = cst.CreateProduct(ctx, p)
	wh := &catalog.Warehouse{EntityID: 1, Code: "MAIN", Label: "Main", Status: 1}
	_ = cst.CreateWarehouse(ctx, wh)
	if _, err := cst.AppendMovement(ctx, &catalog.StockMovement{EntityID: 1,
		ProductID: p.ID, WarehouseID: wh.ID, Qty: 10, UnitCost: 300,
		Reason: catalog.ReasonReceipt}, false); err != nil {
		t.Fatal(err)
	}

	prop := createDoc(t, h, documents.TypeProposal, 7)
	setStatus(t, h, prop.ID, ProposalValidated)
	setStatus(t, h, prop.ID, ProposalSigned)

	// Illegal skip on order path: create order then jump validated→billed (must go via shipped).
	orec := post(t, h, "/api/v1/sales/documents/"+itoa(prop.ID)+"/convert",
		map[string]any{"to": documents.TypeOrder})
	if orec.Code != http.StatusCreated {
		t.Fatalf("convert: code=%d body=%s", orec.Code, orec.Body.String())
	}
	var ord Document
	_ = json.NewDecoder(orec.Body).Decode(&ord)
	if ord.SourceID != prop.ID {
		t.Fatalf("lineage: %+v", ord)
	}
	setStatus(t, h, ord.ID, OrderValidated)
	bad := post(t, h, "/api/v1/sales/documents/"+itoa(ord.ID)+"/status", map[string]any{"to": OrderBilled})
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("skip: code=%d want 422", bad.Code)
	}

	// Order → shipment → validate → fulfill (posts stock) → invoice → pay in full.
	srec := post(t, h, "/api/v1/sales/documents/"+itoa(ord.ID)+"/convert",
		map[string]any{"to": documents.TypeShipment})
	var shp Document
	_ = json.NewDecoder(srec.Body).Decode(&shp)
	setStatus(t, h, shp.ID, ShipmentValidated)
	frec := post(t, h, "/api/v1/sales/shipments/"+itoa(shp.ID)+"/fulfill", map[string]any{
		"lines": []map[string]any{{"product_id": p.ID, "warehouse_id": wh.ID, "qty": 2}},
	})
	if frec.Code != http.StatusOK {
		t.Fatalf("fulfill: code=%d body=%s", frec.Code, frec.Body.String())
	}
	lvl, _ := cst.Level(ctx, p.ID, wh.ID)
	if lvl.Qty != 8 {
		t.Fatalf("stock after fulfill: %+v", lvl)
	}

	irec := post(t, h, "/api/v1/sales/documents/"+itoa(shp.ID)+"/convert",
		map[string]any{"to": documents.TypeInvoice})
	var inv Document
	_ = json.NewDecoder(irec.Body).Decode(&inv)
	setStatus(t, h, inv.ID, InvoiceValidated)
	pay := post(t, h, "/api/v1/sales/payments", map[string]any{
		"org_id": 7, "amount": 1200, "currency": "USD", "method": "transfer",
		"invoice_ids": []int64{inv.ID},
	})
	if pay.Code != http.StatusCreated {
		t.Fatalf("pay: code=%d body=%s", pay.Code, pay.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sales/documents/"+itoa(inv.ID), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var gotInv Document
	_ = json.NewDecoder(rec.Body).Decode(&gotInv)
	if gotInv.Status != InvoicePaid {
		t.Fatalf("invoice status = %d want paid", gotInv.Status)
	}
}

func TestUpdateDraftLines(t *testing.T) {
	h, _, _ := testRouter()
	d := createDoc(t, h, documents.TypeProposal, 7)
	raw, _ := json.Marshal(map[string]any{
		"lines": []map[string]any{
			{"product_id": 1, "label": "Widget", "qty": 3, "unit_net": 500, "vat_rate_bps": 2000},
		},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/sales/documents/"+itoa(d.ID), bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update draft: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var upd Document
	_ = json.NewDecoder(rec.Body).Decode(&upd)
	if upd.Totals.Gross != 1800 || len(upd.Lines) != 1 || upd.Lines[0].Qty != 3 {
		t.Fatalf("updated: %+v", upd)
	}
	setStatus(t, h, d.ID, ProposalValidated)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/sales/documents/"+itoa(d.ID), bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("update validated: code=%d want 422", rec.Code)
	}
}

func TestCreditNoteApplyFlow(t *testing.T) {
	h, _, _ := testRouter()
	d := createDoc(t, h, documents.TypeInvoice, 7)
	setStatus(t, h, d.ID, InvoiceValidated)
	rec := post(t, h, "/api/v1/sales/credit-notes", map[string]any{"invoice_id": d.ID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("credit: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var cn Document
	_ = json.NewDecoder(rec.Body).Decode(&cn)
	if cn.Totals.Gross != 1200 {
		t.Fatalf("credit totals=%+v", cn.Totals)
	}
	setStatus(t, h, cn.ID, 1)
	// Over-credit rejected.
	rec = post(t, h, "/api/v1/sales/credit-notes/"+itoa(cn.ID)+"/apply",
		map[string]any{"invoice_id": d.ID, "amount": 99999})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-credit: code=%d want 422", rec.Code)
	}
	// Partial then full.
	rec = post(t, h, "/api/v1/sales/credit-notes/"+itoa(cn.ID)+"/apply",
		map[string]any{"invoice_id": d.ID, "amount": 200})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]int64
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if out["balance"] != 1000 {
		t.Fatalf("balance=%d want 1000", out["balance"])
	}
}
