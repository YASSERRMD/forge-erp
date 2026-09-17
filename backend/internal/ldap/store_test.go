package ldap

import (
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

func TestSyncGatedWithoutURL(t *testing.T) {
	svc := NewService(NewMemoryStore(), StaticDialer{}, Config{}, platform.NewMemoryBus())
	if _, err := svc.Sync(context.Background(), nil, 1); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("ungated sync: want ErrValidation, got %v", err)
	}
}

func TestSyncFromFakeDialer(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewMemoryStore(),
		StaticDialer{Users: []LDAPUser{
			{Login: "ada", Email: "ada@example.com", FullName: "Ada L"},
			{Login: "bob", Email: "bob@example.com"},
		}},
		Config{URL: "ldap:// directory.test", BaseDN: "dc=example,dc=com"},
		platform.NewMemoryBus())
	res, err := svc.Sync(ctx, nil, 1)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Synced != 2 {
		t.Fatalf("synced=%d want 2", res.Synced)
	}
	got, err := svc.Store.UserByLogin(ctx, nil, 1, "ada")
	if err != nil || got.Email != "ada@example.com" {
		t.Fatalf("mirror: %+v err=%v", got, err)
	}
	// Other tenants see nothing (isolation).
	if list, _ := svc.Store.ListUsers(ctx, nil, 2); len(list) != 0 {
		t.Fatalf("cross-tenant leak: %d", len(list))
	}
	// Re-sync with a changed entry upserts.
	svc.Dialer = StaticDialer{Users: []LDAPUser{{Login: "ada", Email: "ada2@example.com"}}}
	if _, err := svc.Sync(ctx, nil, 1); err != nil {
		t.Fatalf("resync: %v", err)
	}
	got, _ = svc.Store.UserByLogin(ctx, nil, 1, "ada")
	if got.Email != "ada2@example.com" {
		t.Fatalf("upsert: %+v", got)
	}
}

func TestLDAPRoutes(t *testing.T) {
	svc := NewService(NewMemoryStore(),
		StaticDialer{Users: []LDAPUser{{Login: "ada"}}},
		Config{URL: "ldap://directory.test"}, platform.NewMemoryBus())
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Svc: svc}, passthrough) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ldap/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var status map[string]bool
	_ = json.NewDecoder(rec.Body).Decode(&status)
	if !status["enabled"] {
		t.Fatal("status should report enabled")
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/ldap/sync", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync API: code=%d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/ldap/users", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var list []LDAPUser
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("users=%d want 1", len(list))
	}
}
