package module

import (
	"errors"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

func testModule(name string, deps ...string) Base {
	return Base{ModName: name, ModFamily: FamilyTools, ModDeps: deps,
		ModRights: []Right{{Module: name, Entity: "thing", Action: "read"}}}
}

func orderOf(t *testing.T, reg *Registry) []string {
	t.Helper()
	ordered, err := reg.Ordered()
	if err != nil {
		t.Fatalf("Ordered: %v", err)
	}
	names := make([]string, 0, len(ordered))
	for _, m := range ordered {
		names = append(names, m.Name())
	}
	return names
}

func indexOf(names []string, want string) int {
	for i, n := range names {
		if n == want {
			return i
		}
	}
	return -1
}

func TestRegisterDuplicateConflicts(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(testModule("sales")); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(testModule("sales")); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("duplicate register should conflict, got %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(Base{}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("empty name should validate, got %v", err)
	}
	bad := testModule("bad")
	bad.ModRights = []Right{{Module: "bad", Entity: "", Action: "read"}}
	if err := reg.Register(bad); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad right should validate, got %v", err)
	}
}

func TestOrderedDependencies(t *testing.T) {
	reg := NewRegistry()
	// Register out of order on purpose: partners <- catalog <- sales.
	for _, m := range []Base{
		testModule("sales", "catalog", "partners"),
		testModule("catalog", "partners"),
		testModule("partners"),
	} {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	names := orderOf(t, reg)
	if !(indexOf(names, "partners") < indexOf(names, "catalog") &&
		indexOf(names, "catalog") < indexOf(names, "sales")) {
		t.Fatalf("dependency order violated: %v", names)
	}
}

func TestOrderedUnknownDep(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(testModule("sales", "ghost")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Ordered(); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("unknown dep should validate, got %v", err)
	}
}

func TestOrderedCycle(t *testing.T) {
	reg := NewRegistry()
	for _, m := range []Base{testModule("a", "b"), testModule("b", "a")} {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reg.Ordered(); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("cycle should validate, got %v", err)
	}
}

func TestRightValidate(t *testing.T) {
	if err := (Right{Module: "sales", Entity: "invoice", Action: "validate"}).Validate(); err != nil {
		t.Fatalf("valid triple rejected: %v", err)
	}
	for _, bad := range []Right{
		{Entity: "invoice", Action: "read"},
		{Module: "sales", Action: "read"},
		{Module: "sales", Entity: "invoice"},
		{Module: "sales x", Entity: "invoice", Action: "read"},
		{Module: "sales", Entity: "invo.ice", Action: "read"},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("bad triple accepted: %+v", bad)
		}
	}
}

func TestStarterRightsCoverRequiredModules(t *testing.T) {
	all := StarterRights()
	seen := map[string]bool{}
	for _, r := range all {
		if err := r.Validate(); err != nil {
			t.Fatalf("starter right invalid: %+v: %v", r, err)
		}
		seen[r.Module] = true
	}
	for _, mod := range []string{"sales", "partners", "catalog"} {
		if !seen[mod] {
			t.Fatalf("starter set missing module %q", mod)
		}
		if len(RightsFor(mod)) == 0 {
			t.Fatalf("RightsFor(%q) empty", mod)
		}
	}
	// Dolibarr-style validate triple must exist (sales/invoice/validate).
	found := false
	for _, r := range all {
		if r == (Right{Module: "sales", Entity: "invoice", Action: "validate"}) {
			found = true
		}
	}
	if !found {
		t.Fatal("starter set missing sales/invoice/validate")
	}
}

func TestBaseEnabledKillSwitch(t *testing.T) {
	b := testModule("sales")
	if !b.Enabled(Config{}) {
		t.Fatal("default should be enabled")
	}
	if b.Enabled(Config{Disabled: map[string]bool{"sales": true}}) {
		t.Fatal("kill switch should disable")
	}
}
