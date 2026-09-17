package incoterm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestValidate(t *testing.T) {
	if len(Codes()) != 11 {
		t.Fatalf("want 11 Incoterms 2020 codes, got %d", len(Codes()))
	}
	for _, c := range Codes() {
		if err := Validate(c); err != nil {
			t.Fatalf("code %s rejected: %v", c, err)
		}
		if err := Validate("  " + c + " "); err != nil {
			t.Fatalf("padded code %q rejected: %v", c, err)
		}
	}
	for _, bad := range []string{"", "DAT", "DAP ", "EXW1", "FOB\nX", "CIFX"} {
		if bad == "DAP " {
			continue // trailing space normalizes to DAP (valid by design)
		}
		if err := Validate(bad); err == nil {
			t.Errorf("code %q accepted", bad)
		}
	}
	if _, ok := Lookup("fob"); !ok {
		t.Error("Lookup(fob) misses case-insensitive match")
	}
}

func passthrough(_ string, _ string, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(platform.ContextWithEntity(r.Context(), 1)))
		})
	}
}

func TestHandlerListGet(t *testing.T) {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: NewMemoryStore()}, passthrough)
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/incoterms", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: code=%d", rec.Code)
	}
	var terms []Term
	if err := json.NewDecoder(rec.Body).Decode(&terms); err != nil || len(terms) != 11 {
		t.Fatalf("list: %d terms err=%v", len(terms), err)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/incoterms/fob", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get: code=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/incoterms/XXX", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: code=%d want 404", rec.Code)
	}
}

func TestPGSeed(t *testing.T) {
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	terms, err := st.List(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != 11 {
		t.Fatalf("seed has %d terms, want 11", len(terms))
	}
	if _, err := st.Get(context.Background(), pool, "DPU"); err != nil {
		t.Fatalf("get DPU: %v", err)
	}
	if _, err := st.Get(context.Background(), pool, "DAT"); err == nil {
		t.Error("DAT (removed in 2020, replaced by DPU) resolved")
	}
}
