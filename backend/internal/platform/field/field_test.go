package field

import (
	"context"
	"errors"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func industryDef(entityID int64) Definition {
	return Definition{
		EntityID:   entityID,
		Scope:      ScopeOrganization,
		Key:        "industry",
		Type:       TypeSelect,
		Label:      "Industry",
		Required:   true,
		Options:    []string{"manufacturing", "retail", "tech"},
		Searchable: true,
	}
}

func TestMemoryDefinitionCRUD(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	d := industryDef(1)
	if err := st.CreateDefinition(ctx, nil, &d); err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.ID == 0 {
		t.Fatal("create did not assign an id")
	}
	// Duplicate (entity, scope, key) → conflict.
	dup := industryDef(1)
	if err := st.CreateDefinition(ctx, nil, &dup); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("duplicate: got %v, want conflict", err)
	}
	// Same key in another entity is fine.
	other := industryDef(2)
	if err := st.CreateDefinition(ctx, nil, &other); err != nil {
		t.Fatalf("cross-entity create: %v", err)
	}
	// Get is entity-scoped.
	got, err := st.GetDefinition(ctx, nil, 1, ScopeOrganization, "industry")
	if err != nil || got.Label != "Industry" || len(got.Options) != 3 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := st.GetDefinition(ctx, nil, 2, ScopeOrganization, "industry"); err != nil {
		// entity 2 has its own row — must succeed, proving scoping separates them.
		t.Fatalf("get entity 2: %v", err)
	}
	if _, err := st.GetDefinition(ctx, nil, 1, ScopeOrganization, "missing"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("get missing: got %v, want not found", err)
	}
	// List filters by scope; empty scope lists all.
	if err := st.CreateDefinition(ctx, nil, &Definition{
		EntityID: 1, Scope: ScopeProduct, Key: "color", Type: TypeText, Label: "Color",
	}); err != nil {
		t.Fatal(err)
	}
	scoped, err := st.ListDefinitions(ctx, nil, 1, ScopeOrganization)
	if err != nil || len(scoped) != 1 {
		t.Fatalf("scoped list: %v (len=%d)", scoped, len(scoped))
	}
	all, err := st.ListDefinitions(ctx, nil, 1, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("unscoped list: %v (len=%d)", all, len(all))
	}
	// Delete then get → not found; second delete → not found.
	if err := st.DeleteDefinition(ctx, nil, 1, ScopeOrganization, "industry"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetDefinition(ctx, nil, 1, ScopeOrganization, "industry"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("get after delete: got %v, want not found", err)
	}
	if err := st.DeleteDefinition(ctx, nil, 1, ScopeOrganization, "industry"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("delete missing: got %v, want not found", err)
	}
}

func TestCreateDefinitionRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	base := industryDef(1)
	cases := []struct {
		name string
		mut  func(*Definition)
	}{
		{"zero entity", func(d *Definition) { d.EntityID = 0 }},
		{"bad scope", func(d *Definition) { d.Scope = "has space" }},
		{"bad key", func(d *Definition) { d.Key = "9lives" }},
		{"bad key chars", func(d *Definition) { d.Key = "a-b" }},
		{"unknown type", func(d *Definition) { d.Type = "float" }},
		{"select without options", func(d *Definition) { d.Options = nil }},
		{"bad rule", func(d *Definition) { d.ValidationRule = "(unclosed" }},
	}
	for _, c := range cases {
		d := base
		c.mut(&d)
		if err := NewMemoryStore().CreateDefinition(ctx, nil, &d); !errors.Is(err, platform.ErrValidation) {
			t.Errorf("%s: got %v, want validation", c.name, err)
		}
	}
}

func orgValidator() *Validator {
	return NewValidator([]Definition{
		industryDef(1),
		{EntityID: 1, Scope: ScopeOrganization, Key: "employees", Type: TypeNumber, Label: "Employees"},
		{EntityID: 1, Scope: ScopeOrganization, Key: "founded", Type: TypeDate, Label: "Founded"},
		{EntityID: 1, Scope: ScopeOrganization, Key: "vat_registered", Type: TypeBoolean, Label: "VAT"},
		{EntityID: 1, Scope: ScopeOrganization, Key: "nickname", Type: TypeText, Label: "Nickname",
			ValidationRule: `^[A-Za-z]{1,12}$`},
	})
}

func TestValidatorMatrix(t *testing.T) {
	v := orgValidator()
	valid := map[string]any{
		"industry": "tech", "employees": 50, "founded": "2020-01-31",
		"vat_registered": true, "nickname": "Acme",
	}
	if err := v.Validate(ScopeOrganization, 1, valid); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	cases := []struct {
		name   string
		fields map[string]any
	}{
		{"missing required", map[string]any{"employees": 1}},
		{"required blank", map[string]any{"industry": "  "}},
		{"bad option", map[string]any{"industry": "mining"}},
		{"select wrong type", map[string]any{"industry": 7}},
		{"number as string", map[string]any{"industry": "tech", "employees": "many"}},
		{"fractional number", map[string]any{"industry": "tech", "employees": 2.5}},
		{"bool as string", map[string]any{"industry": "tech", "vat_registered": "yes"}},
		{"bad date", map[string]any{"industry": "tech", "founded": "31/01/2020"}},
		{"date wrong type", map[string]any{"industry": "tech", "founded": 20200131}},
		{"text wrong type", map[string]any{"industry": "tech", "nickname": 42}},
		{"rule violation", map[string]any{"industry": "tech", "nickname": "acme!!"}},
	}
	for _, c := range cases {
		if err := v.Validate(ScopeOrganization, 1, c.fields); !errors.Is(err, platform.ErrValidation) {
			t.Errorf("%s: got %v, want validation", c.name, err)
		}
	}
	// accepted variants: integral float (JSON artifact), RFC3339 date, empty optional.
	accepted := []map[string]any{
		{"industry": "retail", "employees": 50.0},
		{"industry": "retail", "founded": "2020-01-31T00:00:00Z"},
		{"industry": "retail"},
		{"industry": "retail", "nickname": ""}, // optional blank skips type check
	}
	for i, f := range accepted {
		if err := v.Validate(ScopeOrganization, 1, f); err != nil {
			t.Errorf("accepted[%d] rejected: %v", i, err)
		}
	}
	// Unknown keys ignored; other entity/scope untouched by entity-1 defs.
	if err := v.Validate(ScopeOrganization, 1, map[string]any{"industry": "tech", "whatever": 1}); err != nil {
		t.Errorf("unknown key rejected: %v", err)
	}
	if err := v.Validate(ScopeOrganization, 2, map[string]any{}); err != nil {
		t.Errorf("other entity rejected: %v", err)
	}
	if err := v.Validate(ScopeProduct, 1, map[string]any{}); err != nil {
		t.Errorf("other scope rejected: %v", err)
	}
	// Nil validator skips everything.
	var nilV *Validator
	if err := nilV.Validate(ScopeOrganization, 1, map[string]any{}); err != nil {
		t.Errorf("nil validator rejected: %v", err)
	}
}

func TestFieldEqualsClause(t *testing.T) {
	got, err := FieldEqualsClause("industry", 2)
	if err != nil || got != "(custom_fields->>'industry') = $2" {
		t.Fatalf("clause: %q %v", got, err)
	}
	if _, err := FieldEqualsClause("a-b", 2); !errors.Is(err, platform.ErrValidation) {
		t.Errorf("bad key: got %v, want validation", err)
	}
	if _, err := FieldEqualsClause("industry", 0); !errors.Is(err, platform.ErrValidation) {
		t.Errorf("bad arg: got %v, want validation", err)
	}
}

func TestTableForScope(t *testing.T) {
	for scope, want := range map[string]string{
		"organization": "ferp_organizations", "organizations": "ferp_organizations",
		"product": "ferp_products", "products": "ferp_products",
	} {
		got, ok := TableForScope(scope)
		if !ok || got != want {
			t.Errorf("scope %q: got (%q,%v), want (%q,true)", scope, got, ok, want)
		}
	}
	if _, ok := TableForScope("invoice"); ok {
		t.Error("unknown scope resolved")
	}
}

func TestPGDefinitionCRUD(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore()

	d := industryDef(1)
	if err := st.CreateDefinition(ctx, pool, &d); err != nil {
		t.Fatalf("create: %v", err)
	}
	dup := industryDef(1)
	if err := st.CreateDefinition(ctx, pool, &dup); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("duplicate: got %v, want conflict", err)
	}
	got, err := st.GetDefinition(ctx, pool, 1, ScopeOrganization, "industry")
	if err != nil || got.Label != "Industry" || len(got.Options) != 3 || !got.Required || !got.Searchable {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := st.GetDefinition(ctx, pool, 1, ScopeOrganization, "nope"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("get missing: got %v, want not found", err)
	}
	other := industryDef(pgtest.NewEntity(t, pool, "fieldco"))
	if err := st.CreateDefinition(ctx, pool, &other); err != nil {
		t.Fatalf("cross-entity create: %v", err)
	}
	list, err := st.ListDefinitions(ctx, pool, 1, ScopeOrganization)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v (len=%d)", list, len(list))
	}
	if err := st.DeleteDefinition(ctx, pool, 1, ScopeOrganization, "industry"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetDefinition(ctx, pool, 1, ScopeOrganization, "industry"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("get after delete: got %v, want not found", err)
	}
}

func TestPGEnsureSearchIndexIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)

	def := industryDef(1)
	for i := 0; i < 2; i++ {
		if err := EnsureSearchIndex(ctx, pool, def); err != nil {
			t.Fatalf("ensure[%d]: %v", i, err)
		}
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM pg_indexes WHERE indexname='ferp_organizations_cf_industry_idx')`).Scan(&exists); err != nil || !exists {
		t.Fatalf("index missing: exists=%v err=%v", exists, err)
	}
	// Non-searchable definitions are a no-op.
	plain := Definition{EntityID: 1, Scope: ScopeOrganization, Key: "notes", Type: TypeText}
	if err := EnsureSearchIndex(ctx, pool, plain); err != nil {
		t.Fatalf("non-searchable: %v", err)
	}
	// Unknown scopes are validation errors, never DDL.
	bad := Definition{EntityID: 1, Scope: "invoice", Key: "x", Type: TypeText, Searchable: true}
	if err := EnsureSearchIndex(ctx, pool, bad); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("unknown scope: got %v, want validation", err)
	}
}
