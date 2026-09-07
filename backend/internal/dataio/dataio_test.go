package dataio

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/partners"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func testRouter() http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Orgs: partners.NewMemoryStore(), Products: catalog.NewMemoryStore()}, passthrough)
	})
	return r
}

func TestOrgRoundTrip(t *testing.T) {
	h := testRouter()
	body := "name,customer_code,supplier_code,email,phone,is_customer,is_supplier\n" +
		"Acme,ACME-1,,a@x.io,,true,false\n" +
		",BAD-1,,b@x.io,,true,false\n" + // missing name → skipped
		"Acme,ACME-1,,a@x.io,,true,false\n" // duplicate → skipped
	req := httptest.NewRequest(http.MethodPost, "/api/v1/imports/organizations", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"created":1`) || !strings.Contains(rec.Body.String(), `"skipped":2`) {
		t.Fatalf("result=%s", rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/exports/organizations.csv", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := rec.Body.String()
	if !strings.HasPrefix(out, "name,customer_code,supplier_code,email,phone,is_customer,is_supplier\n") {
		t.Fatalf("header=%q", out)
	}
	if !strings.Contains(out, "Acme,ACME-1,,a@x.io,,true,false") {
		t.Fatalf("export=%q", out)
	}
	// Wrong columns → 400.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/imports/organizations", strings.NewReader("a,b\n1,2\n"))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad header: code=%d want 400", rec.Code)
	}
}

func TestProductRoundTrip(t *testing.T) {
	h := testRouter()
	body := "sku,name,type,unit,net_price,vat_rate_bps,stock_tracked\n" +
		"WID-1,Widget,0,unit,19.90,2000,true\n" +
		"BAD,Nope,3,unit,1.00,2000,true\n" // bad type → skipped
	req := httptest.NewRequest(http.MethodPost, "/api/v1/imports/products", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"created":1`) || !strings.Contains(rec.Body.String(), `"skipped":1`) {
		t.Fatalf("result=%s", rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/exports/products.csv", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "WID-1,Widget,0,unit,19.90,2000,true") {
		t.Fatalf("export=%q", rec.Body.String())
	}
}
