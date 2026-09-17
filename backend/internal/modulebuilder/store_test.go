package modulebuilder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/module"
	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(platform.ContextWithEntity(r.Context(), 1)))
		})
	}
}

func goodManifest() Manifest {
	return Manifest{
		Name:   "bookclub",
		Family: "tools",
		Rights: []RightDecl{{Module: "bookclub", Entity: "record", Action: "read"}},
		Routes: []string{"/bookclub"},
	}
}

func TestValidateManifest(t *testing.T) {
	if err := goodManifest().Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	cases := []Manifest{
		{Name: "Bad Name", Family: "tools",
			Rights: []RightDecl{{Module: "x", Entity: "y", Action: "read"}}, Routes: []string{"/x"}},
		{Name: "ok", Family: "",
			Rights: []RightDecl{{Module: "x", Entity: "y", Action: "read"}}, Routes: []string{"/x"}},
		{Name: "ok", Family: "tools", Routes: []string{"/x"}},
		{Name: "ok", Family: "tools",
			Rights: []RightDecl{{Module: "x", Entity: "y", Action: "read"}}, Routes: []string{"no-slash"}},
		{Name: "ok", Family: "tools",
			Rights: []RightDecl{{Module: "x", Entity: "bad.entity", Action: "read"}}, Routes: []string{"/x"}},
	}
	for i, m := range cases {
		if err := m.Validate(); !errors.Is(err, platform.ErrValidation) {
			t.Errorf("case %d: want ErrValidation, got %v", i, err)
		}
	}
}

func TestManifestAsBaseRegisters(t *testing.T) {
	// Compile-level proof the manifest covers the module.Module surface:
	// the adapted Base registers in a real registry read-only.
	reg := module.NewRegistry()
	base := goodManifest().AsBase()
	if err := reg.Register(struct {
		module.Base
	}{base}); err != nil {
		t.Fatalf("register adapted manifest: %v", err)
	}
	if _, ok := reg.Get("bookclub"); !ok {
		t.Fatal("adapted manifest not found in registry")
	}
}

func TestScaffoldGolden(t *testing.T) {
	svc := NewService(NewMemoryStore(), platform.NewMemoryBus())
	files, err := svc.Scaffold(context.Background(), ScaffoldSpec{Name: "bookclub", Family: "tools"})
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	for _, want := range []string{"domain.go", "store.go", "service.go", "handler.go", "migration.stub.sql", "routes.snippet.txt"} {
		if _, ok := files[want]; !ok {
			t.Fatalf("missing artifact %s (keys=%v)", want, ScaffoldKeys(files))
		}
	}
	if !strings.Contains(files["domain.go"], "package bookclub") {
		t.Error("domain.go missing package clause")
	}
	if !strings.Contains(files["handler.go"], "bookclub.Routes") && !strings.Contains(files["routes.snippet.txt"], "bookclub.Routes") {
		t.Error("routes snippet missing wiring line")
	}
	if !strings.Contains(files["migration.stub.sql"], "ferp_bookclub") {
		t.Error("migration stub missing table")
	}
	if _, err := svc.Scaffold(context.Background(), ScaffoldSpec{Name: "Bad Name", Family: "tools"}); !errors.Is(err, platform.ErrValidation) {
		t.Errorf("bad scaffold name: want ErrValidation, got %v", err)
	}
}

func TestInstallUninstallMemory(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewMemoryStore(), platform.NewMemoryBus())
	if err := svc.Install(ctx, nil, 1, goodManifest()); err != nil {
		t.Fatalf("install: %v", err)
	}
	got, err := svc.Store.Get(ctx, nil, 1, "bookclub")
	if err != nil || !got.Enabled {
		t.Fatalf("get after install: %+v err=%v", got, err)
	}
	if err := svc.Uninstall(ctx, nil, 1, "bookclub", "1.0"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got, _ = svc.Store.Get(ctx, nil, 1, "bookclub")
	if got.Enabled {
		t.Error("still enabled after uninstall")
	}
	if err := svc.Install(ctx, nil, 0, goodManifest()); !errors.Is(err, platform.ErrUnauthorized) {
		t.Errorf("zero entity: want ErrUnauthorized, got %v", err)
	}
}

func TestModuleBuilderRoutes(t *testing.T) {
	svc := NewService(NewMemoryStore(), platform.NewMemoryBus())
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, Deps{Svc: svc}, passthrough) })

	raw, _ := json.Marshal(ScaffoldSpec{Name: "bookclub", Family: "tools"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/modulebuilder/scaffold", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scaffold API: code=%d", rec.Code)
	}
	raw, _ = json.Marshal(map[string]any{"manifest": goodManifest(), "entity_id": 1})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/modulebuilder/install", bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("install API: code=%d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/modulebuilder/modules", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var list []Activation
	_ = json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 1 || list[0].Name != "bookclub" {
		t.Fatalf("list=%+v want 1 bookclub row", list)
	}
}
