package website

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(platform.ContextWithEntity(r.Context(), 1)))
		})
	}
}

func seedDict(t *testing.T) *DictStore {
	t.Helper()
	d := NewDictStore()
	at := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	if err := d.PutPage(context.Background(), 1, Page{Slug: "about",
		Title: "About", Body: "hello", UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := d.PutPage(context.Background(), 1, Page{Slug: "pricing",
		Title: "Pricing", Body: "prices", UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestWebsitePagesAndSitemap(t *testing.T) {
	ctx := context.Background()
	svc := NewService(seedDict(t), platform.NewMemoryBus(), nil)
	page, err := svc.Page(ctx, 1, "about")
	if err != nil || page.Title != "About" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if _, err := svc.Page(ctx, 1, "missing"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("missing err=%v want ErrNotFound", err)
	}
	if _, err := svc.Page(ctx, 2, "about"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-entity err=%v want ErrNotFound", err)
	}
	sm, err := svc.Sitemap(ctx, 1)
	if err != nil || len(sm) != 2 {
		t.Fatalf("sitemap=%+v err=%v", sm, err)
	}
	for _, e := range sm {
		if e.LastMod != "2026-09-17" {
			t.Fatalf("lastmod=%q want 2026-09-17", e.LastMod)
		}
	}
	// Unvalidated seeds never land in the dict.
	if err := seedDict(t).PutPage(ctx, 1, Page{Slug: "", Title: "x"}); err == nil {
		t.Fatal("slugless page accepted")
	}
}

func TestWebsiteRoutes(t *testing.T) {
	svc := NewService(seedDict(t), platform.NewMemoryBus(), nil)
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Svc: svc}, passthrough) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/website/pages/about", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("page API: code=%d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/website/sitemap", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sitemap API: code=%d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/website/pages/nope", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing API: code=%d want 404", rec.Code)
	}
}
