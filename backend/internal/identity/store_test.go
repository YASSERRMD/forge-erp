package identity

import (
	"context"
	"testing"
	"time"
)

// TestMemoryStoreCRUD exercises the fake through the Store contract so handler
// tests (Task 3) can rely on identical semantics.
func TestMemoryStoreCRUD(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	u := &User{EntityID: 1, Login: "amina", Email: "amina@example.com", FirstName: "Amina", Status: UserActive}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if u.ID == 0 || u.RowVersion != 1 {
		t.Fatalf("bad identity columns: %+v", u)
	}
	got, err := st.UserByLogin(ctx, 1, "amina")
	if err != nil || got.ID != u.ID {
		t.Fatalf("login lookup: %+v %v", got, err)
	}
	if _, err := st.UserByLogin(ctx, 2, "amina"); err != ErrNotFound {
		t.Fatal("cross-entity lookup should miss (entity scoping)")
	}

	// Optimistic locking.
	stale := got
	got.Email = "new@example.com"
	if err := st.UpdateUser(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateUser(ctx, &stale); err != ErrVersionConflict {
		t.Fatal("stale write should conflict")
	}

	// Groups + rights resolution.
	g := &Group{EntityID: 1, Code: "sales", Label: "Sales"}
	if err := st.CreateGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(ctx, g.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	grant := Right{Module: "sales", Entity: "invoice", Action: "read"}
	if err := st.Grant(ctx, 1, nil, &g.ID, grant); err != nil {
		t.Fatal(err)
	}
	direct, inherited, err := st.ResolveRights(ctx, got)
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
	if err := st.CreateSession(ctx, u.ID, "tokhash", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	su, err := st.SessionUser(ctx, "tokhash", time.Now())
	if err != nil || su.ID != u.ID {
		t.Fatalf("session lookup: %+v %v", su, err)
	}
	if err := st.RevokeSession(ctx, "tokhash"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, "tokhash", time.Now()); err != ErrNotFound {
		t.Fatal("revoked session should miss")
	}
}
