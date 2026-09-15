package partners

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/field"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

// orgFieldValidator returns the searchable-industry demo validator for entity 1.
func orgFieldValidator() *field.Validator {
	return field.NewValidator([]field.Definition{
		{
			EntityID: 1, Scope: OrgFieldScope, Key: "industry", Type: field.TypeSelect,
			Label: "Industry", Required: true,
			Options:    []string{"manufacturing", "retail", "tech"},
			Searchable: true,
		},
		{EntityID: 1, Scope: OrgFieldScope, Key: "employees", Type: field.TypeNumber, Label: "Employees"},
	})
}

func TestMemoryStoreOrgCustomFieldValidation(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	st.Validator = orgFieldValidator()

	// Missing required industry → 422.
	missing := &Organization{EntityID: 1, Name: "NoInd", IsCustomer: true}
	if err := st.CreateOrg(ctx, nil, missing); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("missing required: got %v, want validation", err)
	}
	// Bad option → 422.
	badOpt := &Organization{EntityID: 1, Name: "Bad", IsCustomer: true,
		CustomFields: map[string]any{"industry": "mining"}}
	if err := st.CreateOrg(ctx, nil, badOpt); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad option: got %v, want validation", err)
	}
	// Wrong number type → 422.
	badNum := &Organization{EntityID: 1, Name: "Bad", IsCustomer: true,
		CustomFields: map[string]any{"industry": "tech", "employees": "many"}}
	if err := st.CreateOrg(ctx, nil, badNum); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad number: got %v, want validation", err)
	}
	// Valid → ok.
	ok := &Organization{EntityID: 1, Name: "Good", IsCustomer: true,
		CustomFields: map[string]any{"industry": "tech", "employees": 50}}
	if err := st.CreateOrg(ctx, nil, ok); err != nil {
		t.Fatalf("valid: %v", err)
	}
	// Update violating the definitions → 422; valid update → ok.
	got, _ := st.OrgByID(ctx, nil, 1, ok.ID)
	got.CustomFields = map[string]any{"industry": "mining"}
	if err := st.UpdateOrg(ctx, nil, &got); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad update: got %v, want validation", err)
	}
	got.CustomFields = map[string]any{"industry": "retail", "employees": 60}
	if err := st.UpdateOrg(ctx, nil, &got); err != nil {
		t.Fatalf("valid update: %v", err)
	}
	// Definitions are entity-scoped: entity 2 needs no industry.
	foreign := &Organization{EntityID: 2, Name: "Foreign", IsCustomer: true}
	if err := st.CreateOrg(ctx, nil, foreign); err != nil {
		t.Fatalf("other entity: %v", err)
	}
	// Nil validator skips custom-field checks entirely.
	plain := NewMemoryStore()
	anything := &Organization{EntityID: 1, Name: "Free", IsCustomer: true,
		CustomFields: map[string]any{"industry": 12345}}
	if err := plain.CreateOrg(ctx, nil, anything); err != nil {
		t.Fatalf("nil validator: %v", err)
	}
}

func TestMemoryStoreListOrgsByField(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	st.Validator = orgFieldValidator()

	seed := []struct {
		name     string
		industry string
	}{
		{"Forge", "tech"}, {"Mill", "manufacturing"}, {"Shop", "retail"}, {"Lab", "tech"},
	}
	for _, s := range seed {
		o := &Organization{EntityID: 1, Name: s.name, IsCustomer: true,
			CustomFields: map[string]any{"industry": s.industry}}
		if err := st.CreateOrg(ctx, nil, o); err != nil {
			t.Fatal(err)
		}
	}
	// Entity 2 with the same field value must not leak in.
	other := &Organization{EntityID: 2, Name: "Away", IsCustomer: true,
		CustomFields: map[string]any{"industry": "tech"}}
	if err := st.CreateOrg(ctx, nil, other); err != nil {
		t.Fatal(err)
	}

	tech, err := st.ListOrgsByField(ctx, nil, 1, "industry", "tech", 50, 0)
	if err != nil || len(tech) != 2 {
		t.Fatalf("tech filter: %v (len=%d)", tech, len(tech))
	}
	names := []string{tech[0].Name, tech[1].Name}
	sort.Strings(names)
	if names[0] != "Forge" || names[1] != "Lab" {
		t.Fatalf("tech filter names: %v", names)
	}
	none, err := st.ListOrgsByField(ctx, nil, 1, "industry", "mining", 50, 0)
	if err != nil || len(none) != 0 {
		t.Fatalf("empty filter: %v (len=%d)", none, len(none))
	}
	// Pagination applies.
	page, err := st.ListOrgsByField(ctx, nil, 1, "industry", "tech", 1, 1)
	if err != nil || len(page) != 1 {
		t.Fatalf("paged filter: %v (len=%d)", page, len(page))
	}
	// Invalid keys are validation errors (never string-spliced SQL).
	if _, err := st.ListOrgsByField(ctx, nil, 1, "a-b", "tech", 50, 0); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad key: got %v, want validation", err)
	}
}

func TestPGStoreOrgsByField(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	st.Validator = orgFieldValidator()

	// Searchable-field demo: define industry, ensure the expression index
	// (twice — DDL is idempotent), then filter the list by it.
	fst := field.NewPGStore()
	industry := field.Definition{
		EntityID: 1, Scope: OrgFieldScope, Key: "industry", Type: field.TypeSelect,
		Label: "Industry", Required: true,
		Options:    []string{"manufacturing", "retail", "tech"},
		Searchable: true,
	}
	if err := fst.CreateDefinition(ctx, pool, &industry); err != nil {
		t.Fatalf("define industry: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := field.EnsureSearchIndex(ctx, pool, industry); err != nil {
			t.Fatalf("ensure index[%d]: %v", i, err)
		}
	}

	for _, s := range []struct {
		name     string
		industry string
	}{{"PG Forge", "tech"}, {"PG Mill", "manufacturing"}} {
		o := &Organization{EntityID: 1, Name: s.name, IsCustomer: true,
			CustomFields: map[string]any{"industry": s.industry}}
		if err := st.CreateOrg(ctx, pool, o); err != nil {
			t.Fatalf("create %s: %v", s.name, err)
		}
	}
	// Store-level validation also fires on PG: bad option rejected.
	bad := &Organization{EntityID: 1, Name: "PG Bad", IsCustomer: true,
		CustomFields: map[string]any{"industry": "mining"}}
	if err := st.CreateOrg(ctx, pool, bad); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad option on PG: got %v, want validation", err)
	}

	got, err := st.ListOrgsByField(ctx, pool, 1, "industry", "tech", 50, 0)
	if err != nil || len(got) != 1 || got[0].Name != "PG Forge" {
		t.Fatalf("filter tech: %+v %v", got, err)
	}
}
