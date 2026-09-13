package partners

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestMemoryStoreOrgs(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	a := &Organization{EntityID: 1, Name: "Acme", IsCustomer: true, CustomerCode: "AC-1"}
	if err := st.CreateOrg(ctx, nil, a); err != nil {
		t.Fatal(err)
	}
	dup := &Organization{EntityID: 1, Name: "Acme 2", IsCustomer: true, CustomerCode: "AC-1"}
	if err := st.CreateOrg(ctx, nil, dup); err == nil {
		t.Fatal("duplicate customer code accepted")
	}
	// Same code in another entity is fine.
	other := &Organization{EntityID: 2, Name: "Acme", IsCustomer: true, CustomerCode: "AC-1"}
	if err := st.CreateOrg(ctx, nil, other); err != nil {
		t.Fatal(err)
	}
	// Invalid org rejected.
	if err := st.CreateOrg(ctx, nil, &Organization{EntityID: 1}); err == nil {
		t.Fatal("nameless role-less org accepted")
	}
	// Update with stale version conflicts.
	got, _ := st.OrgByID(ctx, nil, a.EntityID, a.ID)
	stale := got
	got.Name = "Acme Corp"
	if err := st.UpdateOrg(ctx, nil, &got); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrg(ctx, nil, &stale); err == nil {
		t.Fatal("stale update accepted")
	}
	// List scoping.
	list, err := st.ListOrgs(ctx, nil, 1, 10, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list entity 1: %v (len=%d)", list, len(list))
	}
}

func TestMemoryStoreContacts(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	o := &Organization{EntityID: 1, Name: "Beta", IsSupplier: true}
	if err := st.CreateOrg(ctx, nil, o); err != nil {
		t.Fatal(err)
	}
	c := &Contact{EntityID: 1, OrgID: o.ID, FirstName: "Ada", LastName: "Lovelace", Role: "technical"}
	if err := st.CreateContact(ctx, nil, c); err != nil {
		t.Fatal(err)
	}
	orphan := &Contact{EntityID: 1, OrgID: 9999, FirstName: "Ghost"}
	if err := st.CreateContact(ctx, nil, orphan); err == nil {
		t.Fatal("orphan contact accepted")
	}
	list, err := st.ContactsOf(ctx, nil, o.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("contacts: %v (len=%d)", list, len(list))
	}
}

func TestPGStoreOrgs(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	a := &Organization{EntityID: 1, Name: "PG Acme", IsCustomer: true, CustomerCode: "PG-1"}
	if err := st.CreateOrg(ctx, pool, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := st.OrgByID(ctx, pool, a.EntityID, a.ID)
	if err != nil || got.Name != "PG Acme" {
		t.Fatalf("by id: %+v %v", got, err)
	}
	other := pgtest.NewEntity(t, pool, "otherco")
	if _, err := st.OrgByID(ctx, pool, other, a.ID); err == nil {
		t.Error("cross-tenant lookup succeeded on PG")
	}
	list, err := st.ListOrgs(ctx, pool, 1, 50, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%d err=%v", len(list), err)
	}
	c := &Contact{EntityID: 1, OrgID: a.ID, FirstName: "Ada", LastName: "L", Role: "billing"}
	if err := st.CreateContact(ctx, pool, c); err != nil {
		t.Fatalf("contact: %v", err)
	}
	contacts, err := st.ContactsOf(ctx, pool, a.ID)
	if err != nil || len(contacts) != 1 {
		t.Fatalf("contacts=%d err=%v", len(contacts), err)
	}
	a.Name = "PG Acme II"
	if err := st.UpdateOrg(ctx, pool, a); err != nil {
		t.Fatalf("update: %v", err)
	}
	stale := *a
	stale.RowVersion--
	if err := st.UpdateOrg(ctx, pool, &stale); err == nil {
		t.Error("stale version accepted on PG")
	}
}
