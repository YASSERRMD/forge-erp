package dict

import (
	"context"
	"errors"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

func testStore(t *testing.T, st Store, db platform.DBTX) {
	t.Helper()
	ctx := context.Background()
	nilDB := db
	if err := st.CreateDictionary(ctx, nilDB, &Dictionary{Code: "ut_civility", Label: "Civilities"}); err != nil {
		t.Fatalf("create dict: %v", err)
	}
	if err := st.CreateDictionary(ctx, nilDB, &Dictionary{Code: "ut_civility", Label: "Dup"}); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("duplicate dict: got %v, want ErrConflict", err)
	}
	if err := st.CreateDictionary(ctx, nilDB, &Dictionary{Code: "", Label: "x"}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("empty dict code: got %v, want ErrValidation", err)
	}
	if _, err := st.GetDictionary(ctx, nilDB, "missing"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("get missing dict: got %v, want ErrNotFound", err)
	}

	core := &Entry{Dictionary: "ut_civility", Code: "MR", Label: "Mister", Sort: 1,
		Active: true, IsCore: true,
		LocaleOverrides: map[string]string{"fr": "Monsieur", "ar": "سيد"},
		Extra:           map[string]any{"core": "true"}}
	if err := st.CreateEntry(ctx, nilDB, core); err != nil {
		t.Fatalf("create core entry: %v", err)
	}
	if err := st.CreateEntry(ctx, nilDB, &Entry{Dictionary: "nope", Code: "X", Label: "x"}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("unknown dictionary: got %v, want ErrValidation", err)
	}
	if err := st.CreateEntry(ctx, nilDB, &Entry{Dictionary: "ut_civility", Code: "MR", Label: "dup"}); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("duplicate entry: got %v, want ErrConflict", err)
	}
	if err := st.CreateEntry(ctx, nilDB, &Entry{Dictionary: "ut_civility", Code: " ", Label: "x"}); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("blank entry code: got %v, want ErrValidation", err)
	}

	// Locale-override merge.
	got, err := st.List(ctx, nilDB, "ut_civility", "fr", false)
	if err != nil || len(got) != 1 || got[0].Label != "Monsieur" {
		t.Fatalf("locale fr merge: got %+v err %v", got, err)
	}
	got, err = st.List(ctx, nilDB, "ut_civility", "de", false)
	if err != nil || len(got) != 1 || got[0].Label != "Mister" {
		t.Fatalf("locale fallback: got %+v err %v", got, err)
	}
	// Raw row keeps the base label.
	raw, err := st.GetEntry(ctx, nilDB, "ut_civility", "MR")
	if err != nil || raw.Label != "Mister" || raw.LocaleOverrides["ar"] != "سيد" {
		t.Fatalf("get raw: got %+v err %v", raw, err)
	}

	// is_core is immutable.
	flip := raw
	flip.IsCore = false
	if err := st.UpdateEntry(ctx, nilDB, &flip); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("is_core flip: got %v, want ErrValidation", err)
	}
	// Active filter.
	plain := &Entry{Dictionary: "ut_civility", Code: "DR", Label: "Doctor", Sort: 2}
	if err := st.CreateEntry(ctx, nilDB, plain); err != nil {
		t.Fatalf("create plain: %v", err)
	}
	if err := st.DeleteEntry(ctx, nilDB, "ut_civility", "DR"); err != nil {
		t.Fatalf("deactivate plain: %v", err)
	}
	active, err := st.List(ctx, nilDB, "ut_civility", "", true)
	if err != nil || len(active) != 1 || active[0].Code != "MR" {
		t.Fatalf("active filter: got %+v err %v", active, err)
	}
	all, err := st.List(ctx, nilDB, "ut_civility", "", false)
	if err != nil || len(all) != 2 {
		t.Fatalf("unfiltered list: got %+v err %v", all, err)
	}

	// Core deactivation: Delete deactivates, HardDelete refuses.
	if err := st.DeleteEntry(ctx, nilDB, "ut_civility", "MR"); err != nil {
		t.Fatalf("deactivate core: %v", err)
	}
	after, err := st.GetEntry(ctx, nilDB, "ut_civility", "MR")
	if err != nil || after.Active || !after.IsCore {
		t.Fatalf("core after delete: got %+v err %v", after, err)
	}
	if err := st.HardDeleteEntry(ctx, nilDB, "ut_civility", "MR"); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("hard-delete core: got %v, want ErrValidation", err)
	}
	if _, err := st.GetEntry(ctx, nilDB, "ut_civility", "MR"); err != nil {
		t.Fatalf("core must survive hard-delete attempt: %v", err)
	}
	// Non-core hard delete succeeds.
	if err := st.HardDeleteEntry(ctx, nilDB, "ut_civility", "DR"); err != nil {
		t.Fatalf("hard-delete plain: %v", err)
	}
	if _, err := st.GetEntry(ctx, nilDB, "ut_civility", "DR"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("plain after hard delete: got %v, want ErrNotFound", err)
	}
	if err := st.DeleteEntry(ctx, nilDB, "ut_civility", "GHOST"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("delete missing: got %v, want ErrNotFound", err)
	}
	if err := st.HardDeleteEntry(ctx, nilDB, "ut_civility", "GHOST"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatalf("hard-delete missing: got %v, want ErrNotFound", err)
	}

	// VAT bps convention: integer basis points, no floats.
	if err := st.CreateDictionary(ctx, nilDB, &Dictionary{Code: "ut_vat_rate", Label: "VAT"}); err != nil {
		t.Fatalf("vat dict: %v", err)
	}
	vat := &Entry{Dictionary: "ut_vat_rate", Code: "FR-20", Label: "TVA 20%",
		Extra: map[string]any{"rate_bps": 2000, "rate_text": "20", "country": "FR"}}
	if err := st.CreateEntry(ctx, nilDB, vat); err != nil {
		t.Fatalf("vat entry: %v", err)
	}
	back, err := st.GetEntry(ctx, nilDB, "ut_vat_rate", "FR-20")
	if err != nil {
		t.Fatalf("vat get: %v", err)
	}
	// JSON round-trips decode numbers as float64 on PG and keep ints in
	// memory; accept both, reject actual float types at rest.
	switch bps := back.Extra["rate_bps"].(type) {
	case float64:
		if bps != 2000 {
			t.Fatalf("rate_bps round-trip: got %#v", back.Extra["rate_bps"])
		}
	case int:
		if bps != 2000 {
			t.Fatalf("rate_bps round-trip: got %#v", back.Extra["rate_bps"])
		}
	default:
		t.Fatalf("rate_bps round-trip: got %#v", back.Extra["rate_bps"])
	}
}

func TestMemoryCRUDLocaleAndCore(t *testing.T) {
	testStore(t, NewMemoryStore(), nil)
}
