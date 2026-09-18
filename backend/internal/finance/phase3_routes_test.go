package finance

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

func testPhase3Router() (http.Handler, *MemoryStore, *Poster) {
	st := NewMemoryStore()
	_ = st.CreateJournal(context.Background(), nil, &Journal{EntityID: 1, Code: "VEN", Label: "Sales"})
	_ = st.CreateAccount(context.Background(), nil, &Account{EntityID: 1, Code: "411", Label: "C", Type: "asset"})
	_ = st.CreateAccount(context.Background(), nil, &Account{EntityID: 1, Code: "701", Label: "R", Type: "revenue"})
	bindings := NewMemoryBindingStore()
	poster := &Poster{Store: st, Postings: NewMemoryPostingStore(), Bindings: bindings,
		Loaders: map[string]InvoiceLoader{DocSalesInvoice: fakeLoader{doc: InvoiceDoc{ID: 3,
			Ref: "INV-R", Date: time.Now().UTC(), OrgRef: "ACME", Net: 500, VAT: 0}}},
		Bus: platform.NewMemoryBus()}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: st, Poster: poster, CloseLog: NewMemoryCloseLogStore(),
			Bindings: bindings}, passthrough)
	})
	return r, st, poster
}

func TestPhase3Routes(t *testing.T) {
	r, st, poster := testPhase3Router()

	// Bindings: create then list (memory store implements BindingStore).
	rec := post(t, r, "/api/v1/finance/bindings", map[string]any{
		"kind": "partner", "key": "ACME", "account_id": 1})
	if rec.Code != http.StatusCreated {
		t.Fatalf("binding: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = post(t, r, "/api/v1/finance/bindings", map[string]any{
		"kind": "product", "key": "default", "account_id": 2})
	if rec.Code != http.StatusCreated {
		t.Fatalf("binding default: code=%d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/finance/bindings?kind=partner", nil)
	grec := httptest.NewRecorder()
	r.ServeHTTP(grec, req)
	var list []Binding
	_ = json.NewDecoder(grec.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("bindings=%d want 1", len(list))
	}

	// Chart load on memory answers 501 (PostgreSQL-only).
	rec = post(t, r, "/api/v1/finance/charts/load", map[string]any{"pack": "FR"})
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("chart load: code=%d want 501", rec.Code)
	}

	// Manual post trigger idempotently posts the canned invoice.
	rec = post(t, r, "/api/v1/finance/postings/sales-invoices/3", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("manual post: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var res PostResult
	_ = json.NewDecoder(rec.Body).Decode(&res)
	if !res.Created || res.EntryID == 0 {
		t.Fatalf("result=%+v", res)
	}
	rec = post(t, r, "/api/v1/finance/postings/sales-invoices/3", nil)
	_ = json.NewDecoder(rec.Body).Decode(&res)
	if res.Created {
		t.Fatal("replay created a second entry")
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/finance/postings", nil)
	grec = httptest.NewRecorder()
	r.ServeHTTP(grec, req)
	var postings []Posting
	_ = json.NewDecoder(grec.Body).Decode(&postings)
	if len(postings) != 1 {
		t.Fatalf("postings=%d want 1", len(postings))
	}
	_ = st
	_ = poster

	// Reopen on an unlocked year is 422 (close log wired, year open).
	rec = post(t, r, "/api/v1/finance/fiscal-years/1/reopen", map[string]any{"note": "x"})
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reopen missing year: code=%d want 404/422", rec.Code)
	}

	// FEC exports header-only with no entries (200 text/plain).
	req = httptest.NewRequest(http.MethodGet, "/api/v1/finance/exports/fec", nil)
	grec = httptest.NewRecorder()
	r.ServeHTTP(grec, req)
	if grec.Code != http.StatusOK {
		t.Fatalf("fec: code=%d", grec.Code)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(grec.Body)
	if !bytes.HasPrefix(buf.Bytes(), []byte("JournalCode\t")) {
		t.Fatalf("fec header missing: %q", buf.String())
	}
}
