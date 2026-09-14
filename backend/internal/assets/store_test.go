package assets

import (
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
	"github.com/go-chi/chi/v5"
)

func passthrough(_, _, _ string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(platform.ContextWithEntity(r.Context(), 1)))
		})
	}
}

func TestAssetLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	a := &Asset{EntityID: 1, Code: "CNC-1", Label: "CNC mill", Kind: "workstation", Status: AssetInService}
	if err := m.CreateAsset(ctx, nil, a); err != nil {
		t.Fatalf("asset: %v", err)
	}
	if err := m.CreateAsset(ctx, nil, &Asset{EntityID: 1, Code: "X", Label: "x", Kind: "spaceship"}); err == nil {
		t.Error("bad kind accepted")
	}
	upd, err := m.SetAssetStatus(ctx, nil, 1, a.ID, AssetMaintenance, a.RowVersion)
	if err != nil {
		t.Fatalf("maintenance: %v", err)
	}
	if _, err := m.SetAssetStatus(ctx, nil, 1, a.ID, AssetRetired, upd.RowVersion); err != nil {
		t.Fatalf("retire: %v", err)
	}

	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Routes(r, Deps{Store: NewMemoryStore()}, passthrough)
	})
	raw, _ := json.Marshal(map[string]any{"code": "SRV-1", "label": "Server", "kind": "it"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assets", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("API create: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var created Asset
	_ = json.NewDecoder(rec.Body).Decode(&created)
	raw, _ = json.Marshal(map[string]any{"status": 0, "row_version": created.RowVersion})
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/assets/%d/status", created.ID), bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("retire API: code=%d", rec.Code)
	}
}

func TestUpdateFrozenRetired(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	a := &Asset{EntityID: 1, Code: "A1", Label: "L", Kind: "it", Status: AssetInService}
	if err := m.CreateAsset(ctx, nil, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	upd, err := m.UpdateAsset(ctx, nil, 1, a.ID, "L2", "SN-1", nil, a.RowVersion)
	if err != nil || upd.Label != "L2" || upd.Serial != "SN-1" {
		t.Fatalf("update: %+v %v", upd, err)
	}
	ret, err := m.SetAssetStatus(ctx, nil, 1, a.ID, AssetRetired, upd.RowVersion)
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if _, err := m.UpdateAsset(ctx, nil, 1, a.ID, "L3", "", nil, ret.RowVersion); err == nil {
		t.Error("retired edit accepted")
	}
}

func TestPGAssetFlow(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	a := &Asset{EntityID: 1, Code: "PG-A", Label: "PG", Kind: "it", Status: AssetInService}
	if err := st.CreateAsset(ctx, pool, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := st.AssetByID(ctx, pool, 1, a.ID)
	if err != nil || got.Code != "PG-A" {
		t.Fatalf("by id: %+v %v", got, err)
	}
	if _, err := st.SetAssetStatus(ctx, pool, 1, a.ID, AssetMaintenance, a.RowVersion); err != nil {
		t.Fatalf("maintenance: %v", err)
	}
}

func TestCrossTenantIsolation(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	a := &Asset{EntityID: 1, Code: "X-1", Label: "X", Kind: "it", Status: AssetInService}
	if err := m.CreateAsset(ctx, nil, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := m.AssetByID(ctx, nil, 2, a.ID); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("cross-tenant AssetByID err=%v want ErrNotFound", err)
	}
	if _, err := m.SetAssetStatus(ctx, nil, 2, a.ID, AssetMaintenance, a.RowVersion); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("cross-tenant SetAssetStatus err=%v want ErrNotFound", err)
	}
	if _, err := m.UpdateAsset(ctx, nil, 2, a.ID, "Y", "", nil, a.RowVersion); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("cross-tenant UpdateAsset err=%v want ErrNotFound", err)
	}
}
