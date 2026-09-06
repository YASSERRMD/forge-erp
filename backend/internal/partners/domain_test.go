package partners

import (
	"testing"
)

func TestOrganizationValidate(t *testing.T) {
	ok := Organization{EntityID: 1, Name: "Acme", IsCustomer: true, CustomerCode: "AC-001"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		mut  func(*Organization)
	}{
		{"empty name", func(o *Organization) { o.Name = "  " }},
		{"no role", func(o *Organization) { o.IsCustomer = false }},
		{"bad customer code", func(o *Organization) { o.CustomerCode = "bad code!" }},
		{"bad email", func(o *Organization) { o.Email = "not-an-email" }},
	}
	for _, c := range cases {
		o := ok
		c.mut(&o)
		if err := o.Validate(); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
	// Self-parent.
	o := ok
	o.ID = 7
	o.ParentID = &o.ID
	if err := o.Validate(); err == nil {
		t.Error("self-parent: expected error")
	}
}

func TestCheckNoCycle(t *testing.T) {
	// Chain 1 -> 2 -> 3 (3 is root).
	parents := map[int64]*int64{i64(2): nil}
	_ = parents
	two := int64(2)
	three := int64(3)
	lookup := func(id int64) (*int64, bool) {
		switch id {
		case 1:
			return &two, true
		case 2:
			return &three, true
		case 3:
			return nil, true
		}
		return nil, false
	}
	if err := CheckNoCycle(1, &two, lookup); err != nil {
		t.Fatalf("valid chain rejected: %v", err)
	}
	// Cycle: 1 -> 2 -> 1.
	cyc := func(id int64) (*int64, bool) {
		if id == 1 {
			return &two, true
		}
		one := int64(1)
		return &one, true
	}
	if err := CheckNoCycle(1, &two, cyc); err == nil {
		t.Fatal("cycle not detected")
	}
	// Dangling parent fails closed.
	if err := CheckNoCycle(9, i64p(99), func(id int64) (*int64, bool) { return nil, false }); err == nil {
		t.Fatal("missing parent not rejected")
	}
}

func i64p(v int64) *int64 { return &v }
func i64(v int64) int64   { return v }

func TestContactValidate(t *testing.T) {
	ok := Contact{EntityID: 1, OrgID: 5, FirstName: "Ada", Role: "billing", Email: "ada@example.com"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.OrgID = 0
	if err := bad.Validate(); err == nil {
		t.Error("orphan contact accepted")
	}
	bad = ok
	bad.FirstName, bad.LastName = "", ""
	if err := bad.Validate(); err == nil {
		t.Error("nameless contact accepted")
	}
	bad = ok
	bad.Role = "janitor"
	if err := bad.Validate(); err == nil {
		t.Error("unknown role accepted")
	}
}

func TestCategoryValidate(t *testing.T) {
	if err := (Category{EntityID: 1, Code: "vip", Label: "VIP", Scope: "organization"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Category{EntityID: 1, Code: "", Label: "x", Scope: "organization"}).Validate(); err == nil {
		t.Error("empty code accepted")
	}
}
