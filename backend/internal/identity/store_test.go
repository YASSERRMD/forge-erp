package identity

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

// TestMemoryStoreCRUD exercises the fake through the Store contract so handler
// tests (Task 3) can rely on identical semantics.
func TestMemoryStoreCRUD(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	u := &User{EntityID: 1, Login: "amina", Email: "amina@example.com", FirstName: "Amina", Status: UserActive}
	if err := st.CreateUser(ctx, nil, u); err != nil {
		t.Fatal(err)
	}
	if u.ID == 0 || u.RowVersion != 1 {
		t.Fatalf("bad identity columns: %+v", u)
	}
	got, err := st.UserByLogin(ctx, nil, 1, "amina")
	if err != nil || got.ID != u.ID {
		t.Fatalf("login lookup: %+v %v", got, err)
	}
	if _, err := st.UserByLogin(ctx, nil, 2, "amina"); err != ErrNotFound {
		t.Fatal("cross-entity lookup should miss (entity scoping)")
	}

	// Optimistic locking.
	stale := got
	got.Email = "new@example.com"
	if err := st.UpdateUser(ctx, nil, 1, &got); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateUser(ctx, nil, 1, &stale); err != ErrVersionConflict {
		t.Fatal("stale write should conflict")
	}

	// Groups + rights resolution.
	g := &Group{EntityID: 1, Code: "sales", Label: "Sales"}
	if err := st.CreateGroup(ctx, nil, g); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(ctx, nil, g.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	grant := Right{Module: "sales", Entity: "invoice", Action: "read"}
	if err := st.Grant(ctx, nil, 1, nil, &g.ID, grant); err != nil {
		t.Fatal(err)
	}
	direct, inherited, err := st.ResolveRights(ctx, nil, got)
	if err != nil {
		t.Fatal(err)
	}
	if len(direct) != 0 || len(inherited) != 1 {
		t.Fatalf("rights: direct=%v inherited=%v", direct, inherited)
	}
	if !Can(got, direct, inherited, "sales", "invoice", "read") {
		t.Fatal("inherited grant not honored by Can()")
	}

	// Sessions.
	if err := st.CreateSession(ctx, nil, u.ID, "tokhash", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	su, err := st.SessionUser(ctx, nil, "tokhash", time.Now())
	if err != nil || su.ID != u.ID {
		t.Fatalf("session lookup: %+v %v", su, err)
	}
	if err := st.RevokeSession(ctx, nil, "tokhash"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, nil, "tokhash", time.Now()); err != ErrNotFound {
		t.Fatal("revoked session should miss")
	}
}

func TestPGStoreUsers(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	u := &User{EntityID: 1, Login: "pgamina", Email: "pgamina@example.com", Status: UserActive}
	if err := st.CreateUser(ctx, pool, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.ID == 0 || u.RowVersion == 0 {
		t.Fatalf("not populated: %+v", u)
	}
	got, err := st.UserByLogin(ctx, pool, 1, "pgamina")
	if err != nil || got.Email != "pgamina@example.com" {
		t.Fatalf("by login: %+v %v", got, err)
	}
	if _, err := st.UserByEmail(ctx, pool, 1, "pgamina@example.com"); err != nil {
		t.Fatalf("by email: %v", err)
	}
	list, err := st.ListUsers(ctx, pool, 1, 50, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%d err=%v", len(list), err)
	}
	dup := &User{EntityID: 1, Login: "pgamina", Email: "other@example.com", Status: UserActive}
	if err := st.CreateUser(ctx, pool, dup); err == nil {
		t.Error("duplicate login accepted on PG")
	}
}

func TestPGCrossTenantLookup(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	other := pgtest.NewEntity(t, pool, "otherco")
	u := &User{EntityID: 1, Login: "tenant-a", Email: "a@example.com", Status: UserActive}
	if err := st.CreateUser(ctx, pool, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.UserByID(ctx, pool, other, u.ID); err == nil {
		t.Error("cross-tenant lookup succeeded on PG")
	}
	if _, err := st.UserByID(ctx, pool, 1, u.ID); err != nil {
		t.Fatalf("own-tenant lookup failed: %v", err)
	}
}
