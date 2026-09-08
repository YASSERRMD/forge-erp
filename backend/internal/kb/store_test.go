package kb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func TestArticlePublishSearch(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	a := &Article{EntityID: 1, Slug: "reset-pw", Title: "Reset password",
		Body: "Click forgot password link", Tags: []string{"auth"}}
	if err := m.CreateArticle(ctx, a); err != nil {
		t.Fatalf("article: %v", err)
	}
	// Drafts are unsearchable.
	if hits, _ := m.SearchArticles(ctx, 1, "password", 10); len(hits) != 0 {
		t.Fatalf("draft searchable: %d", len(hits))
	}
	upd, err := m.SetArticleStatus(ctx, a.ID, ArticlePublished, a.RowVersion)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	_ = upd
	hits, err := m.SearchArticles(ctx, 1, "forgot", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits=%d err=%v", len(hits), err)
	}

	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: NewMemoryStore()}, passthrough)
	})
	raw, _ := json.Marshal(map[string]any{"slug": "vpn", "title": "VPN setup", "body": "Install client"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kb/articles", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("API create: code=%d", rec.Code)
	}
	var created Article
	_ = json.NewDecoder(rec.Body).Decode(&created)
	raw, _ = json.Marshal(map[string]any{"status": 1, "row_version": created.RowVersion})
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/kb/articles/%d/status", created.ID), bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish API: code=%d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/kb/search?q=vpn", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var found []Article
	_ = json.NewDecoder(rec.Body).Decode(&found)
	if len(found) != 1 {
		t.Fatalf("search=%d want 1", len(found))
	}
}

func TestUpdateDraftOnly(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	a := &Article{EntityID: 1, Slug: "s1", Title: "T", Body: "b"}
	if err := m.CreateArticle(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	upd, err := m.UpdateArticle(ctx, a.ID, "T2", "b2", []string{"x"}, a.RowVersion)
	if err != nil || upd.Title != "T2" {
		t.Fatalf("update: %+v %v", upd, err)
	}
	if _, err := m.SetArticleStatus(ctx, a.ID, ArticlePublished, upd.RowVersion); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := m.UpdateArticle(ctx, a.ID, "T3", "b", nil, upd.RowVersion+1); err == nil {
		t.Error("published edit accepted")
	}
}
