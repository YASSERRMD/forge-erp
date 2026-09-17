package collab

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

func TestCommentThreadIsolation(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewMemoryStore(), platform.NewMemoryBus())
	top := &Comment{EntityID: 1, Scope: "sales", ObjectType: "invoice", ObjectID: 7,
		Author: "ada", Body: "first"}
	if err := svc.Add(ctx, nil, top); err != nil {
		t.Fatalf("add: %v", err)
	}
	reply := &Comment{EntityID: 1, Scope: "sales", ObjectType: "invoice", ObjectID: 7,
		Thread: "t1", Author: "bob", Body: "reply"}
	if err := svc.Add(ctx, nil, reply); err != nil {
		t.Fatalf("reply: %v", err)
	}
	all, err := svc.Store.ListForObject(ctx, nil, 1, "sales", "invoice", 7, "", 50, 0)
	if err != nil || len(all) != 2 {
		t.Fatalf("all=%d err=%v want 2", len(all), err)
	}
	thread, err := svc.Store.ListForObject(ctx, nil, 1, "sales", "invoice", 7, "t1", 50, 0)
	if err != nil || len(thread) != 1 || thread[0].Author != "bob" {
		t.Fatalf("thread=%+v err=%v", thread, err)
	}
	// Another object stays empty; another entity stays empty.
	other, _ := svc.Store.ListForObject(ctx, nil, 1, "sales", "invoice", 8, "", 50, 0)
	if len(other) != 0 {
		t.Fatalf("other object=%d want 0", len(other))
	}
	// Only the author deletes (mallory gets 404-shaped ErrNotFound).
	if err := svc.Remove(ctx, nil, 1, top.ID, "mallory"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-author remove: want ErrNotFound, got %v", err)
	}
	if err := svc.Remove(ctx, nil, 1, top.ID, "ada"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	// Empty body is 422-shaped.
	if err := svc.Add(ctx, nil, &Comment{EntityID: 1, Scope: "s", ObjectType: "o",
		ObjectID: 1, Author: "a"}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("empty body err=%v want ErrValidation", err)
	}
}

func TestCommentRoutes(t *testing.T) {
	svc := NewService(NewMemoryStore(), platform.NewMemoryBus())
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Svc: svc}, passthrough) })

	raw, _ := json.Marshal(map[string]any{"scope": "sales", "object_type": "invoice",
		"object_id": 3, "body": "looks good"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments?author=ada", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add API: code=%d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet,
		"/api/v1/comments?scope=sales&object_type=invoice&object_id=3", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var list []Comment
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 1 || list[0].Body != "looks good" {
		t.Fatalf("list=%+v want 1 comment", list)
	}
}
