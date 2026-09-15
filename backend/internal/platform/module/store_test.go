package module

import (
	"context"
	"errors"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

// TestMemoryStoreCRUD exercises the fake through the Store contract so handler
// tests can rely on identical semantics (entity scoping included).
func TestMemoryStoreCRUD(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	if err := st.SetEnabled(ctx, nil, 1, "sales", true, "1.0"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, nil, 1, "sales")
	if err != nil || !got.Enabled || got.Version != "1.0" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := st.Get(ctx, nil, 2, "sales"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-entity lookup should miss, got %v", err)
	}
	if err := st.SetEnabled(ctx, nil, 1, "sales", false, "1.1"); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Get(ctx, nil, 1, "sales")
	if got.Enabled || got.Version != "1.1" {
		t.Fatalf("upsert should overwrite: %+v", got)
	}
	if err := st.SetEnabled(ctx, nil, 0, "sales", true, ""); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("zero entity should validate, got %v", err)
	}
	if err := st.SetEnabled(ctx, nil, 1, "", true, ""); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("empty name should validate, got %v", err)
	}
}

// TestListModulesDefaultsEnabled checks the SPA contract: registered modules
// without a stored row report enabled (today's hand-wired behaviour), stored
// rows override, and output follows dependency order.
func TestListModulesDefaultsEnabled(t *testing.T) {
	ctx := context.Background()
	reg := NewRegistry()
	for _, m := range []Base{testModule("sales", "catalog"), testModule("catalog")} {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	st := NewMemoryStore()
	if err := st.SetEnabled(ctx, nil, 1, "catalog", false, "1.0"); err != nil {
		t.Fatal(err)
	}
	infos, err := ListModules(ctx, nil, st, reg, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 || infos[0].Name != "catalog" || infos[1].Name != "sales" {
		t.Fatalf("order/names: %+v", infos)
	}
	if infos[0].Enabled {
		t.Fatal("catalog should report disabled")
	}
	if !infos[1].Enabled {
		t.Fatal("sales (no row) should default enabled")
	}
	if len(infos[1].Rights) == 0 {
		t.Fatal("infos should carry rights for the SPA")
	}
	if _, err := ListModules(ctx, nil, st, reg, 0); !errors.Is(err, platform.ErrUnauthorized) {
		t.Fatalf("zero entity should be unauthorized, got %v", err)
	}
}

// TestPGModuleStore runs ferp_modules CRUD + rights seeding against real
// Postgres (skips without TEST_DATABASE_URL; CI sets it).
func TestPGModuleStore(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore()

	if err := st.SetEnabled(ctx, pool, 1, "sales", true, "1.0"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, pool, 1, "sales")
	if err != nil || !got.Enabled || got.Version != "1.0" || got.EntityID != 1 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := st.Get(ctx, pool, 1, "ghost"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("missing row should be not found, got %v", err)
	}
	if err := st.SetEnabled(ctx, pool, 1, "sales", false, "1.1"); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Get(ctx, pool, 1, "sales")
	if got.Enabled || got.Version != "1.1" {
		t.Fatalf("upsert should overwrite: %+v", got)
	}
	rows, err := st.List(ctx, pool, 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: %+v %v", rows, err)
	}

	// Activation: enable + seed the module's rights into ferp_rights for a group.
	var groupID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO ferp_groups (entity_id, code, label) VALUES (1,'admins','Admins') RETURNING id`,
	).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	sales := Base{ModName: "sales", ModFamily: FamilyCRM, ModRights: RightsFor("sales")}
	if err := reg.Register(sales); err != nil {
		t.Fatal(err)
	}
	if err := Activate(ctx, pool, st, reg, 1, "sales", "1.2", &groupID); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Get(ctx, pool, 1, "sales")
	if !got.Enabled || got.Version != "1.2" {
		t.Fatalf("activate should enable: %+v", got)
	}
	var seeded int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ferp_rights WHERE entity_id=1 AND group_id=$1 AND module='sales'`,
		groupID).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if seeded != len(RightsFor("sales")) {
		t.Fatalf("seeded %d rights, want %d", seeded, len(RightsFor("sales")))
	}
	// Re-activation is idempotent (ON CONFLICT DO NOTHING).
	if err := Activate(ctx, pool, st, reg, 1, "sales", "1.2", &groupID); err != nil {
		t.Fatal(err)
	}
	var reseeded int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ferp_rights WHERE entity_id=1 AND group_id=$1 AND module='sales'`,
		groupID).Scan(&reseeded); err != nil {
		t.Fatal(err)
	}
	if reseeded != seeded {
		t.Fatalf("re-activation duplicated grants: %d -> %d", seeded, reseeded)
	}
	// Deactivation keeps grants (re-activation restores access silently).
	if err := Deactivate(ctx, pool, st, 1, "sales", "1.2"); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Get(ctx, pool, 1, "sales")
	if got.Enabled {
		t.Fatal("deactivate should disable")
	}
	if err := Activate(ctx, pool, st, reg, 1, "ghost", "1.0", nil); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("unknown module should be not found, got %v", err)
	}
}
