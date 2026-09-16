package dataio

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/members"
	"github.com/YASSERRMD/forge-erp/backend/internal/partners"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

func fullRouter() http.Handler {
	mst := members.NewMemoryStore()
	_ = mst.CreateType(context.Background(), nil, &members.MemberType{EntityID: 1, Code: "STD", Label: "Standard", AnnualFee: 1000})
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Orgs: partners.NewMemoryStore(), Products: catalog.NewMemoryStore(),
			Members: mst, Sales: sales.NewMemoryStore()}, passthrough)
	})
	return r
}

func postImport(t *testing.T, h http.Handler, body ImportRequest) ImportReport {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/imports", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import %s: code=%d body=%s", body.Context, rec.Code, rec.Body.String())
	}
	var rep ImportReport
	if err := json.NewDecoder(rec.Body).Decode(&rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	return rep
}

func TestGenericDryRunErrorVectors(t *testing.T) {
	h := fullRouter()
	// Members dry-run: one good row, bad type_id, missing name, missing ref.
	// (Email format is not validated by the owning members context, so the
	// mirror deliberately accepts any email string.)
	rep := postImport(t, h, ImportRequest{Context: "members", DryRun: true, CSV:
		"ref,type_id,first_name,last_name,company,email\n" +
			"M-1,1,John,Doe,,john@x.io\n" +
			"M-2,nope,Jane,Doe,,jane@x.io\n" +
			"M-3,1,,,,\n" +
			",1,Bob,Smith,,bob@x.io\n"})
	if rep.Created != 1 || rep.Skipped != 3 || len(rep.Rows) != 4 {
		t.Fatalf("members dry-run: %+v", rep)
	}
	// Dry-run wrote nothing: export is header-only.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/exports/members", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if strings.TrimSpace(rec.Body.String()) != "ref,type_id,first_name,last_name,company,email" {
		t.Fatalf("dry-run wrote rows: %q", rec.Body.String())
	}
	// Products dry-run vectors: bad type, negative price, service tracked.
	rep = postImport(t, h, ImportRequest{Context: "products", DryRun: true, CSV:
		"sku,name,type,unit,net_price,vat_rate_bps,stock_tracked\n" +
			"WID-1,Widget,0,unit,19.90,2000,true\n" +
			"BAD,Nope,3,unit,1.00,2000,true\n" +
			"NEG,Neg,0,unit,-5.00,2000,false\n" +
			"SRV,Svc,1,unit,10.00,2000,true\n"})
	if rep.Created != 1 || rep.Skipped != 3 {
		t.Fatalf("products dry-run: %+v", rep)
	}
	// Orgs dry-run vectors: missing name, no role, bad email.
	rep = postImport(t, h, ImportRequest{Context: "orgs", DryRun: true, CSV:
		"name,customer_code,supplier_code,email,phone,is_customer,is_supplier\n" +
			"Acme,ACME-1,,a@x.io,,true,false\n" +
			",BAD-1,,b@x.io,,true,false\n" +
			"Roleless,,,,,false,false\n" +
			"BadMail,,,not-an-email,,true,false\n"})
	if rep.Created != 1 || rep.Skipped != 3 {
		t.Fatalf("orgs dry-run: %+v", rep)
	}
	// Sales dry-run vectors: unknown type, bad org, bad qty, missing currency.
	rep = postImport(t, h, ImportRequest{Context: "sales", DryRun: true, CSV:
		"type,org_id,currency,label,qty,unit_net,vat_rate_bps\n" +
			"invoice,7,EUR,Widget,2,1990,2000\n" +
			"proforma,7,EUR,Widget,2,1990,2000\n" +
			"invoice,0,EUR,Widget,2,1990,2000\n" +
			"invoice,7,,Widget,2,1990,2000\n"})
	if rep.Created != 1 || rep.Skipped != 3 {
		t.Fatalf("sales dry-run: %+v", rep)
	}
	// Unknown context → 400.
	raw, _ := json.Marshal(ImportRequest{Context: "nope", CSV: "a\n1\n"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/imports", bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown context: code=%d want 400", rec.Code)
	}
	// Custom column mapping: renamed CSV headers.
	rep = postImport(t, h, ImportRequest{Context: "orgs", DryRun: true,
		Mapping: map[string]string{"raison_sociale": "name", "cli": "is_customer", "frn": "is_supplier"},
		CSV: "raison_sociale,cli,frn\nAcme,true,false\n"})
	if rep.Created != 1 {
		t.Fatalf("mapped import: %+v", rep)
	}
}

func TestGenericImportWritesAndExport(t *testing.T) {
	h := fullRouter()
	rep := postImport(t, h, ImportRequest{Context: "members", CSV:
		"ref,type_id,first_name,last_name,company,email\nM-9,1,Ada,Lovelace,,ada@x.io\n"})
	if rep.Created != 1 || rep.Skipped != 0 {
		t.Fatalf("members write: %+v", rep)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/exports/members", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "M-9,1,Ada,Lovelace,,ada@x.io") {
		t.Fatalf("members export=%q", rec.Body.String())
	}
	rep = postImport(t, h, ImportRequest{Context: "sales", CSV:
		"type,org_id,currency,label,qty,unit_net,vat_rate_bps\ninvoice,7,EUR,Widget,2,1990,2000\n"})
	if rep.Created != 1 {
		t.Fatalf("sales write: %+v", rep)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/exports/sales?type=invoice", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "invoice,7,EUR,Widget,2,1990,2000") {
		t.Fatalf("sales export=%q", rec.Body.String())
	}
}

func TestCSVQuotingRoundTrip(t *testing.T) {
	h := fullRouter()
	name := `Acme, "Global" Corp`
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write([]string{"name", "customer_code", "supplier_code", "email", "phone", "is_customer", "is_supplier"})
	_ = cw.Write([]string{name, "Q-1", "", "q@x.io", "", "true", "false"})
	cw.Flush()
	rep := postImport(t, h, ImportRequest{Context: "orgs", CSV: buf.String()})
	if rep.Created != 1 {
		t.Fatalf("quoted import: %+v", rep)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/exports/orgs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	rows, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil || len(rows) != 2 || rows[1][0] != name {
		t.Fatalf("quoting round-trip: rows=%v err=%v export=%q", rows, err, rec.Body.String())
	}
	// RenderCSV quotes commas and embedded quotes.
	out := RenderCSV([]string{"a", "b"}, [][]string{{`x,"y`, "z\nz"}})
	back, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil || back[1][0] != `x,"y` || back[1][1] != "z\nz" {
		t.Fatalf("RenderCSV: %q err=%v", out, err)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("content-type=%q", rec.Header().Get("Content-Type"))
	}
}

func TestPGGenericImportWrites(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	pst := partners.NewPGStore(pool)
	cst := catalog.NewPGStore(pool)
	mst := members.NewPGStore(pool)
	sst := sales.NewPGStore(pool)
	mt := &members.MemberType{EntityID: 1, Code: "PGSTD", Label: "PG standard", AnnualFee: 500}
	if err := mst.CreateType(ctx, pool, mt); err != nil {
		t.Fatalf("member type: %v", err)
	}
	orgAd := orgAdapter{store: pst}
	rep := RunImport(ctx, pool, 1, orgAd, []map[string]string{
		{"name": "PG Org", "customer_code": "PGORG", "is_customer": "true", "is_supplier": "false"},
		{"name": "", "is_customer": "true"},
	}, false)
	if rep.Created != 1 || rep.Skipped != 1 {
		t.Fatalf("pg orgs: %+v", rep)
	}
	prodAd := productAdapter{store: cst}
	rep = RunImport(ctx, pool, 1, prodAd, []map[string]string{
		{"sku": "PG-1", "name": "PG Widget", "type": "0", "unit": "unit",
			"net_price": "9.90", "vat_rate_bps": "2000", "stock_tracked": "false"},
	}, false)
	if rep.Created != 1 {
		t.Fatalf("pg products: %+v", rep)
	}
	memAd := memberAdapter{store: mst}
	rep = RunImport(ctx, pool, 1, memAd, []map[string]string{
		{"ref": "PGM-1", "type_id": strconv.FormatInt(mt.ID, 10), "first_name": "Grace", "last_name": "Hopper"},
	}, false)
	if rep.Created != 1 {
		t.Fatalf("pg members: %+v", rep)
	}
	salesAd := salesAdapter{store: sst, docType: documents.TypeInvoice}
	rep = RunImport(ctx, pool, 1, salesAd, []map[string]string{
		{"type": "invoice", "org_id": "3", "currency": "EUR", "label": "Widget",
			"qty": "1", "unit_net": "1000", "vat_rate_bps": "2000"},
		{"type": "invoice", "org_id": "0", "currency": "EUR", "label": "Bad",
			"qty": "1", "unit_net": "1000", "vat_rate_bps": "2000"},
	}, true)
	if rep.Created != 1 || rep.Skipped != 1 {
		t.Fatalf("pg sales dry-run: %+v", rep)
	}
	list, err := pst.ListOrgs(ctx, pool, 1, 50, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("pg orgs persisted=%v err=%v", list, err)
	}
}
