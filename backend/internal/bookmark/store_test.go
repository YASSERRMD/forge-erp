package bookmark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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

func TestBookmarkCRUDIsolation(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewMemoryStore(), platform.NewMemoryBus())
	b := &Bookmark{EntityID: 1, UserLogin: "ada", Scope: "sales", ObjectType: "invoice", ObjectID: 7}
	if err := svc.Add(ctx, nil, b); err != nil {
		t.Fatalf("add: %v", err)
	}
	if b.ID == 0 {
		t.Fatal("ID unset")
	}
	dup := &Bookmark{EntityID: 1, UserLogin: "ada", Scope: "sales", ObjectType: "invoice", ObjectID: 7}
	if err := svc.Add(ctx, nil, dup); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("duplicate: want ErrConflict, got %v", err)
	}
	// Same object bookmarked by another user is independent.
	other := &Bookmark{EntityID: 1, UserLogin: "bob", Scope: "sales", ObjectType: "invoice", ObjectID: 7}
	if err := svc.Add(ctx, nil, other); err != nil {
		t.Fatalf("other user add: %v", err)
	}
	list, err := svc.Store.ListForUser(ctx, nil, 1, "ada")
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%d err=%v", len(list), err)
	}
	// Toggle removes, second toggle re-adds.
	added, err := svc.Toggle(ctx, nil, Bookmark{EntityID: 1, UserLogin: "ada", Scope: "sales", ObjectType: "invoice", ObjectID: 7})
	if err != nil || added {
		t.Fatalf("toggle-off: added=%v err=%v", added, err)
	}
	added, err = svc.Toggle(ctx, nil, Bookmark{EntityID: 1, UserLogin: "ada", Scope: "sales", ObjectType: "invoice", ObjectID: 7})
	if err != nil || !added {
		t.Fatalf("toggle-on: added=%v err=%v", added, err)
	}
	// Cross-user remove is not found (never 403-shaped here: 404).
	list, _ = svc.Store.ListForUser(ctx, nil, 1, "ada")
	if err := svc.Store.Remove(ctx, nil, 1, list[0].ID, "mallory"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-user remove: want ErrNotFound, got %v", err)
	}
}

func TestBookmarkRoutes(t *testing.T) {
	svc := NewService(NewMemoryStore(), platform.NewMemoryBus())
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Svc: svc}, passthrough) })

	raw, _ := json.Marshal(map[string]any{"scope": "sales", "object_type": "invoice", "object_id": 3})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bookmarks?user=ada", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add API: code=%d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/bookmarks?user=ada", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var list []Bookmark
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("list=%d want 1", len(list))
	}
}
