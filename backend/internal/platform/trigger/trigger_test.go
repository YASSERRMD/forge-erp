package trigger

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

func TestCatalogueSizeAndCoverage(t *testing.T) {
	defs := Catalogue()
	if len(defs) != 20 {
		t.Fatalf("catalogue=%d want 20", len(defs))
	}
	seen := map[string]bool{}
	for _, d := range defs {
		if err := d.Validate(); err != nil {
			t.Fatalf("def %s invalid: %v", d.Name, err)
		}
		if seen[d.Name] {
			t.Fatalf("duplicate %s", d.Name)
		}
		seen[d.Name] = true
		if d.Version != 1 {
			t.Errorf("%s version=%d want 1", d.Name, d.Version)
		}
	}
	for _, want := range []string{"BILL_VALIDATE", "MEMBER_SUBSCRIPTION", "EXPENSE_PAID"} {
		if !seen[want] {
			t.Errorf("catalogue missing %s", want)
		}
	}
}

func TestDefaultRegistryList(t *testing.T) {
	names := List()
	if len(names) != 20 {
		t.Fatalf("list=%d want 20", len(names))
	}
	if !sort.StringsAreSorted(names) {
		t.Fatal("List not sorted")
	}
	if got := Default().List(); len(got) != 20 {
		t.Fatalf("default list=%d want 20", len(got))
	}
	def, err := ByName("EXPENSE_PAID")
	if err != nil {
		t.Fatalf("ByName: %v", err)
	}
	if def.Subject != "forgeerp.hr.expense.paid.v1" {
		t.Fatalf("subject=%q want legacy compat", def.Subject)
	}
	if _, err := ByName("NOPE_MISSING"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("unknown ByName err=%v want ErrNotFound", err)
	}
}

func TestRegistryRegisterConflicts(t *testing.T) {
	r := NewRegistry()
	def := Definition{Name: "X_ONE", Subject: "forgeerp.x.one.v1", Entity: "x",
		Schema: map[string]string{"entity_id": "int64"}, Version: 1}
	if err := r.Register(def); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register(def); !errors.Is(err, platform.ErrAlreadyExists) {
		t.Fatalf("duplicate err=%v want ErrAlreadyExists", err)
	}
	if err := r.Register(Definition{}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("empty def err=%v want ErrValidation", err)
	}
	noEntity := Definition{Name: "X_TWO", Subject: "s", Schema: map[string]string{}, Version: 1}
	if err := r.Register(noEntity); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("schema without entity_id err=%v want ErrValidation", err)
	}
	if _, err := r.ByName("X_MISSING"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("missing err=%v want ErrNotFound", err)
	}
}

func TestTypedEventsMatchCatalogue(t *testing.T) {
	for _, e := range Samples() {
		def, err := ByName(e.EventName())
		if err != nil {
			t.Fatalf("sample %T not in catalogue: %v", e, err)
		}
		got := e.Def()
		if got.Name != def.Name || got.Subject != def.Subject || got.Entity != def.Entity || got.Version != def.Version {
			t.Errorf("%s Def() != catalogue def", e.EventName())
		}
		p := e.Payload()
		if _, ok := p["id"]; !ok {
			t.Errorf("%s payload missing id", e.EventName())
		}
		if e.EntityID() <= 0 || e.ObjectID() <= 0 {
			t.Errorf("%s sample ids must be positive", e.EventName())
		}
		for k := range p {
			if k == "entity_id" {
				continue
			}
			if _, ok := def.Schema[k]; !ok {
				t.Errorf("%s payload field %q undocumented in schema", e.EventName(), k)
			}
		}
	}
	if len(Samples()) != len(Catalogue()) {
		t.Fatalf("samples=%d catalogue=%d: keep 1:1", len(Samples()), len(Catalogue()))
	}
}

func TestSubscribeByName(t *testing.T) {
	bus := platform.NewMemoryBus()
	var got []platform.Event
	unsub, err := Subscribe(nil, bus, "BILL_VALIDATE", func(_ context.Context, e platform.Event) {
		got = append(got, e)
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	_ = bus.Publish(context.Background(), platform.Event{
		Subject: "forgeerp.sales.invoice.validated.v1", Entity: "invoice", EntityID: 1, ID: 9})
	if len(got) != 1 || got[0].ID != 9 {
		t.Fatalf("got=%v want 1 delivery", got)
	}
	unsub()
	_ = bus.Publish(context.Background(), platform.Event{
		Subject: "forgeerp.sales.invoice.validated.v1", EntityID: 1, ID: 10})
	if len(got) != 1 {
		t.Fatal("unsubscribe did not detach")
	}
	if _, err := Subscribe(nil, bus, "BOGUS_EVENT", func(context.Context, platform.Event) {}); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("bogus subscribe err=%v want ErrNotFound", err)
	}
	if _, err := Subscribe(nil, nil, "BILL_VALIDATE", func(context.Context, platform.Event) {}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("nil bus err=%v want ErrValidation", err)
	}
}

func TestEmitValidation(t *testing.T) {
	ctx := context.Background()
	def, _ := ByName("EXPENSE_PAID")
	if err := Emit(ctx, nil, def, 1, map[string]any{"id": int64(1)}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("nil db err=%v want ErrValidation", err)
	}
	db := newFakeDB()
	if err := Emit(ctx, db, def, 0, map[string]any{"id": int64(1)}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("entity 0 err=%v want ErrValidation", err)
	}
	if err := Emit(ctx, db, Definition{}, 1, nil); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("empty def err=%v want ErrValidation", err)
	}
	if err := EmitEvent(ctx, db, nil); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("nil event err=%v want ErrValidation", err)
	}
	if n := db.pending(); n != 0 {
		t.Fatalf("pending=%d want 0 after rejected emits", n)
	}
}
