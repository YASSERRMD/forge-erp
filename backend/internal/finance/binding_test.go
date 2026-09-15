package finance

import (
	"context"
	"errors"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func TestBindingValidate(t *testing.T) {
	ok := Binding{EntityID: 1, Kind: BindingProduct, Key: "SKU-1", AccountID: 7}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Binding{
		{Kind: BindingProduct, Key: "x", AccountID: 1},               // no entity
		{EntityID: 1, Kind: "bogus", Key: "x", AccountID: 1},         // bad kind
		{EntityID: 1, Kind: BindingVAT, AccountID: 1},                // empty key
		{EntityID: 1, Kind: BindingBank, Key: "BNK"},                 // no account
		{EntityID: 1, Kind: BindingPartner, Key: "p", AccountID: -2}, // negative account
		{EntityID: 1, Kind: BindingExpense, Key: "  ", AccountID: 1}, // blank key
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

// exerciseBindingStore runs the shared CRUD+resolution contract against any
// BindingStore. acctA/B/C are writable account ids (arbitrary on memory,
// real seeded rows on PG where the FK holds).
func exerciseBindingStore(t *testing.T, st BindingStore, ctx context.Context, db platform.DBTX, acctA, acctB, acctC int64) {
	t.Helper()
	// Missing binding resolves to ErrNotFound.
	if _, err := st.ResolveAccount(ctx, db, 1, BindingProduct, "NOPE"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("missing resolve: %v", err)
	}
	b := &Binding{EntityID: 1, Kind: BindingProduct, Key: "SKU-1", AccountID: acctA}
	if err := st.SetBinding(ctx, db, b); err != nil {
		t.Fatalf("set: %v", err)
	}
	if b.ID == 0 {
		t.Fatal("set did not populate ID")
	}
	got, err := st.Binding(ctx, db, 1, BindingProduct, "SKU-1")
	if err != nil || got.AccountID != acctA {
		t.Fatalf("get=%+v err=%v", got, err)
	}
	// Cross-tenant isolation.
	if _, err := st.Binding(ctx, db, 2, BindingProduct, "SKU-1"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("cross-tenant binding: %v", err)
	}
	// Upsert replaces the account, keeping one row.
	if err := st.SetBinding(ctx, db, &Binding{EntityID: 1, Kind: BindingProduct, Key: "SKU-1", AccountID: acctB}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	acct, err := st.ResolveAccount(ctx, db, 1, BindingProduct, "SKU-1")
	if err != nil || acct != acctB {
		t.Fatalf("resolve=%d err=%v", acct, err)
	}
	if err := st.SetBinding(ctx, db, &Binding{EntityID: 1, Kind: BindingVAT, Key: "2000", AccountID: acctC}); err != nil {
		t.Fatalf("vat set: %v", err)
	}
	all, err := st.ListBindings(ctx, db, 1, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("list all=%d err=%v", len(all), err)
	}
	vat, err := st.ListBindings(ctx, db, 1, BindingVAT)
	if err != nil || len(vat) != 1 || vat[0].AccountID != acctC {
		t.Fatalf("list vat=%+v err=%v", vat, err)
	}
	other, err := st.ListBindings(ctx, db, 2, "")
	if err != nil || len(other) != 0 {
		t.Fatalf("list other entity=%d err=%v", len(other), err)
	}
	// Invalid binding rejected with validation sentinel.
	if err := st.SetBinding(ctx, db, &Binding{EntityID: 1, Kind: "bogus", Key: "x", AccountID: 1}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad kind: %v", err)
	}
}

func TestMemoryBindingStore(t *testing.T) {
	exerciseBindingStore(t, NewMemoryBindingStore(), context.Background(), nil, 101, 202, 303)
}

func TestPGBindingStore(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	// Seed real accounts so the FK holds.
	var ids []int64
	for _, c := range []struct{ code, typ string }{
		{"707B", "revenue"}, {"445B", "liability"}, {"512B", "asset"},
	} {
		a := &Account{EntityID: 1, Code: c.code, Label: c.code, Type: c.typ}
		if err := st.CreateAccount(ctx, pool, a); err != nil {
			t.Fatalf("account: %v", err)
		}
		ids = append(ids, a.ID)
	}
	exerciseBindingStore(t, BindingStore(st), ctx, pool, ids[0], ids[1], ids[2])
}
