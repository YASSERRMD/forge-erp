package assets

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

func TestAssetLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	a := &Asset{EntityID: 1, Code: "CNC-1", Label: "CNC mill", Kind: "workstation", Status: AssetInService}
	if err := m.CreateAsset(ctx, a); err != nil {
		t.Fatalf("asset: %v", err)
	}
	if err := m.CreateAsset(ctx, &Asset{EntityID: 1, Code: "X", Label: "x", Kind: "spaceship"}); err == nil {
		t.Error("bad kind accepted")
	}
	upd, err := m.SetAssetStatus(ctx, a.ID, AssetMaintenance, a.RowVersion)
	if err != nil {
		t.Fatalf("maintenance: %v", err)
	}
	if _, err := m.SetAssetStatus(ctx, a.ID, AssetRetired, upd.RowVersion); err != nil {
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
	if err := m.CreateAsset(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	upd, err := m.UpdateAsset(ctx, a.ID, "L2", "SN-1", nil, a.RowVersion)
	if err != nil || upd.Label != "L2" || upd.Serial != "SN-1" {
		t.Fatalf("update: %+v %v", upd, err)
	}
	ret, err := m.SetAssetStatus(ctx, a.ID, AssetRetired, upd.RowVersion)
	if err != nil {
		t.Fatalf("retire: %v", err)
	}
	if _, err := m.UpdateAsset(ctx, a.ID, "L3", "", nil, ret.RowVersion); err == nil {
		t.Error("retired edit accepted")
	}
}
