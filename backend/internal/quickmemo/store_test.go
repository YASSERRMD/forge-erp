package quickmemo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestMemoCRUDIsolation(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewMemoryStore(), platform.NewMemoryBus())
	m := &Memo{EntityID: 1, UserLogin: "ada", Title: "Call supplier", Body: "Ask about lead time"}
	if err := svc.Create(ctx, nil, m); err != nil {
		t.Fatalf("create: %v", err)
	}
	upd, err := svc.Update(ctx, nil, 1, m.ID, "ada", "Call supplier ASAP", "Ask now", m.RowVersion)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.RowVersion != m.RowVersion+1 {
		t.Fatalf("row version not bumped: %+v", upd)
	}
	// Stale version conflicts.
	if _, err := svc.Update(ctx, nil, 1, m.ID, "ada", "x", "y", m.RowVersion); !errors.Is(err, platform.ErrVersionConflict) {
		t.Fatalf("stale update: want ErrVersionConflict, got %v", err)
	}
	// Other users cannot read / update / delete (404, never leak).
	if _, err := svc.Store.MemoByID(ctx, nil, 1, m.ID, "bob"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-user read: want ErrNotFound, got %v", err)
	}
	if _, err := svc.Update(ctx, nil, 1, m.ID, "bob", "x", "y", upd.RowVersion); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-user update: want ErrNotFound, got %v", err)
	}
	if err := svc.Delete(ctx, nil, 1, m.ID, "bob"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-user delete: want ErrNotFound, got %v", err)
	}
	if err := svc.Delete(ctx, nil, 1, m.ID, "ada"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestMemoRoutes(t *testing.T) {
	svc := NewService(NewMemoryStore(), platform.NewMemoryBus())
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Svc: svc}, passthrough) })

	raw, _ := json.Marshal(map[string]any{"title": "Todo", "body": "Ship it"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/memos?user=ada", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create API: code=%d", rec.Code)
	}
	var created Memo
	_ = json.NewDecoder(rec.Body).Decode(&created)
	raw, _ = json.Marshal(map[string]any{"title": "Todo v2", "body": "Ship it now", "row_version": created.RowVersion})
	req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/memos/%d?user=ada", created.ID), bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update API: code=%d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/memos?user=bob", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var list []Memo
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 0 {
		t.Fatalf("cross-user list leak: %d", len(list))
	}
}
