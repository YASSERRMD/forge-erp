package dict

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(platform.ContextWithEntity(r.Context(), 1)))
		})
	}
}

func seedDict(t *testing.T) *MemoryStore {
	t.Helper()
	m := NewMemoryStore()
	ctx := context.Background()
	if err := m.CreateDictionary(ctx, nil, &Dictionary{Code: "vat_rate", Label: "VAT rates"}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateEntry(ctx, nil, &Entry{Dictionary: "vat_rate", Code: "2000",
		Label: "Sales tax 20%", Sort: 1, Active: true,
		LocaleOverrides: map[string]string{"fr": "TVA 20%"}}); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDictionaryRoutes(t *testing.T) {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Store: seedDict(t)}, passthrough) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dictionaries", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dictionaries: code=%d", rec.Code)
	}
	var dicts []Dictionary
	_ = json.NewDecoder(rec.Body).Decode(&dicts)
	if len(dicts) != 1 || dicts[0].Code != "vat_rate" {
		t.Fatalf("dicts=%+v", dicts)
	}

	// French override resolves; default serves the base label.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/dictionaries/vat_rate?locale=fr", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var fr []Entry
	_ = json.NewDecoder(rec.Body).Decode(&fr)
	if len(fr) != 1 || fr[0].Label != "TVA 20%" {
		t.Fatalf("fr=%+v", fr)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/dictionaries/vat_rate", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var en []Entry
	_ = json.NewDecoder(rec.Body).Decode(&en)
	if len(en) != 1 || en[0].Label != "Sales tax 20%" {
		t.Fatalf("en=%+v", en)
	}
}
