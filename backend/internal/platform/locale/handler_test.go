package locale

import (
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

func TestLocaleList(t *testing.T) {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{}, passthrough) })
	req := httptest.NewRequest(http.MethodGet, "/api/v1/locales", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: code=%d", rec.Code)
	}
	var list []localeInfo
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 3 {
		t.Fatalf("locales=%d want 3 (ar/en/fr)", len(list))
	}
	for _, l := range list {
		if l.Code == "ar" && l.Direction != "rtl" {
			t.Fatalf("ar direction=%q want rtl", l.Direction)
		}
		if l.Keys == 0 {
			t.Fatalf("%s has no keys", l.Code)
		}
	}
}

func TestLocaleGetFallback(t *testing.T) {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{}, passthrough) })
	get := func(path, accept string) catalogueOut {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if accept != "" {
			req.Header.Set("Accept-Language", accept)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("get %s: code=%d", path, rec.Code)
		}
		var out catalogueOut
		_ = json.NewDecoder(rec.Body).Decode(&out)
		return out
	}
	fr := get("/api/v1/locales/fr", "")
	if fr.Matched != "fr" || fr.Strings["doc.invoice"] != "Facture" {
		t.Fatalf("fr=%+v", fr)
	}
	// Unknown tag falls back to English, visibly (Requested echoes xx).
	xx := get("/api/v1/locales/xx", "")
	if xx.Matched != "en" || xx.Requested != "xx" {
		t.Fatalf("xx=%+v want requested xx matched en", xx)
	}
	// Accept-Language negotiates when no code preference is usable.
	en := get("/api/v1/locales/xx", "fr-FR, fr;q=0.9")
	if en.Matched != "fr" {
		t.Fatalf("negotiated=%+v want fr", en)
	}
	// Arabic serves RTL with English fallback for untranslated keys.
	ar := get("/api/v1/locales/ar", "")
	if ar.Direction != "rtl" {
		t.Fatalf("ar direction=%q want rtl", ar.Direction)
	}
}
