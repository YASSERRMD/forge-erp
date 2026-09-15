package number_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/number"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// stubRow is a pgx.Row fake: scanFn runs on Scan, or err is returned.
type stubRow struct {
	val *string
	err error
}

func (r stubRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.val != nil {
		if p, ok := dest[0].(*string); ok {
			*p = *r.val
			return nil
		}
		return errors.New("stubRow: bad dest")
	}
	return pgx.ErrNoRows
}

// stubDB fakes platform.DBTX for selection tests: QueryRow returns val/err,
// Exec/Query are unused.
type stubDB struct {
	val *string
	err error
}

func (s stubDB) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (s stubDB) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, errors.New("stubDB: no query support")
}

func (s stubDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	if s.err != nil {
		return stubRow{err: s.err}
	}
	if s.val != nil {
		return stubRow{val: s.val}
	}
	return stubRow{err: pgx.ErrNoRows}
}

func strptr(s string) *string { return &s }

func TestRegistryLookup(t *testing.T) {
	for _, code := range []string{"standard", "mercure"} {
		m, err := number.ByCode(code)
		if err != nil {
			t.Fatalf("ByCode(%q): %v", code, err)
		}
		if m.Code() != code {
			t.Fatalf("Code() = %q want %q", m.Code(), code)
		}
	}
}

func TestUnknownCodeError(t *testing.T) {
	if _, err := number.ByCode("nope"); err == nil {
		t.Fatal("expected error for unknown code")
	} else if !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("unknown code err = %v, want platform.ErrValidation sentinel", err)
	}
}

func TestPreviewOutput(t *testing.T) {
	std, _ := number.ByCode("standard")
	got := std.Preview(number.Config{DocType: "invoice", At: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Seq: 7})
	if got != "INV-202609-0007" {
		t.Fatalf("standard preview = %q want INV-202609-0007", got)
	}
	mc, _ := number.ByCode("mercure")
	got = mc.Preview(number.Config{DocType: "invoice", EntityID: 1, Seq: 42})
	if got != "MC-INV-0001-000042" {
		t.Fatalf("mercure preview = %q want MC-INV-0001-000042", got)
	}
	// Schemes must be visibly different.
	if strings.HasPrefix(got, "INV-") {
		t.Fatal("mercure preview must not use the standard PREFIX-YYYYMM scheme")
	}
}

func TestSelectionFallbackToStandard(t *testing.T) {
	ctx := context.Background()
	// Unconfigured: no row → standard.
	m, err := number.ForEntity(ctx, stubDB{err: pgx.ErrNoRows}, 1, "invoice")
	if err != nil {
		t.Fatal(err)
	}
	if m.Code() != "standard" {
		t.Fatalf("fallback code = %q want standard", m.Code())
	}
	// Empty value → standard.
	m, err = number.ForEntity(ctx, stubDB{val: strptr("")}, 1, "invoice")
	if err != nil {
		t.Fatal(err)
	}
	if m.Code() != "standard" {
		t.Fatalf("empty-value code = %q want standard", m.Code())
	}
	// Configured mercure → mercure.
	m, err = number.ForEntity(ctx, stubDB{val: strptr("mercure")}, 1, "invoice")
	if err != nil {
		t.Fatal(err)
	}
	if m.Code() != "mercure" {
		t.Fatalf("configured code = %q want mercure", m.Code())
	}
	// Configured unknown → ErrValidation.
	if _, err := number.ForEntity(ctx, stubDB{val: strptr("nope")}, 1, "invoice"); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("unknown configured code err = %v, want ErrValidation", err)
	}
	// Missing tenant → ErrUnauthorized (entityID everywhere).
	if _, err := number.ForEntity(ctx, stubDB{err: pgx.ErrNoRows}, 0, "invoice"); !errors.Is(err, platform.ErrUnauthorized) {
		t.Fatalf("zero entity err = %v, want ErrUnauthorized", err)
	}
	// Key convention check.
	if k := number.Key("invoice"); k != "numbering.invoice" {
		t.Fatalf("Key = %q want numbering.invoice", k)
	}
}
