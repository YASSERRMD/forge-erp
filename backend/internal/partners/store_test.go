package partners

import (
	"context"
	"testing"
)

func TestMemoryStoreOrgs(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	a := &Organization{EntityID: 1, Name: "Acme", IsCustomer: true, CustomerCode: "AC-1"}
	if err := st.CreateOrg(ctx, a); err != nil {
		t.Fatal(err)
	}
	dup := &Organization{EntityID: 1, Name: "Acme 2", IsCustomer: true, CustomerCode: "AC-1"}
	if err := st.CreateOrg(ctx, dup); err == nil {
		t.Fatal("duplicate customer code accepted")
	}
	// Same code in another entity is fine.
	other := &Organization{EntityID: 2, Name: "Acme", IsCustomer: true, CustomerCode: "AC-1"}
	if err := st.CreateOrg(ctx, other); err != nil {
		t.Fatal(err)
	}
	// Invalid org rejected.
	if err := st.CreateOrg(ctx, &Organization{EntityID: 1}); err == nil {
		t.Fatal("nameless role-less org accepted")
	}
	// Update with stale version conflicts.
	got, _ := st.OrgByID(ctx, a.ID)
	stale := got
	got.Name = "Acme Corp"
	if err := st.UpdateOrg(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrg(ctx, &stale); err == nil {
		t.Fatal("stale update accepted")
	}
	// List scoping.
	list, err := st.ListOrgs(ctx, 1, 10, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list entity 1: %v (len=%d)", list, len(list))
	}
}

func TestMemoryStoreContacts(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	o := &Organization{EntityID: 1, Name: "Beta", IsSupplier: true}
	if err := st.CreateOrg(ctx, o); err != nil {
		t.Fatal(err)
	}
	c := &Contact{EntityID: 1, OrgID: o.ID, FirstName: "Ada", LastName: "Lovelace", Role: "technical"}
	if err := st.CreateContact(ctx, c); err != nil {
		t.Fatal(err)
	}
	orphan := &Contact{EntityID: 1, OrgID: 9999, FirstName: "Ghost"}
	if err := st.CreateContact(ctx, orphan); err == nil {
		t.Fatal("orphan contact accepted")
	}
	list, err := st.ContactsOf(ctx, o.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("contacts: %v (len=%d)", list, len(list))
	}
}
