package docgen

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func testSubject() InvoiceSubject {
	lines := []documents.Line{
		{ProductID: 1, Label: "Widget", Qty: 2, UnitNet: 500, VATRateBps: 2000},
		{ProductID: 2, Label: "Gadget", Qty: 1, UnitNet: 199, VATRateBps: 0},
	}
	tot, _ := documents.Sum(lines)
	return InvoiceSubject{
		Ref: "INV-202609-0001", DocType: string(documents.TypeInvoice),
		EntityID: 1, OrgID: 7, Currency: "USD", IssuedOn: "2026-09-14",
		Lines: lines, Totals: tot,
	}
}

func render(t *testing.T, m DocModel, sub InvoiceSubject, locale string) []byte {
	t.Helper()
	r, ct, err := m.Render(context.Background(), sub, locale)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if ct != "application/pdf" {
		t.Fatalf("content type = %q", ct)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF")) {
		t.Fatal("output is not a PDF")
	}
	return b
}

func TestFormatMoneyMinorUnits(t *testing.T) {
	cases := map[int64]string{
		1200: "12.00 USD", 0: "0.00 USD", 5: "0.05 USD",
		-250: "-2.50 USD", 123456789: "1234567.89 USD",
	}
	for in, want := range cases {
		if got := FormatMoney(in, "USD"); got != want {
			t.Errorf("FormatMoney(%d) = %q want %q", in, got, want)
		}
	}
	if got := FormatMoney(100, "usd"); got != "1.00 USD" {
		t.Errorf("currency not uppercased: %q", got)
	}
}

func TestRegistryLookup(t *testing.T) {
	r := NewRegistry()
	r.Register(StandardModel{})
	m, err := r.Lookup("standard")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if m.Code() != "standard" {
		t.Fatalf("code = %q", m.Code())
	}
	if _, err := r.Lookup("nope"); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("unknown model err = %v, want ErrValidation", err)
	}
	if _, err := r.Lookup(""); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("blank model err = %v, want ErrValidation", err)
	}
	if got := DefaultRegistry(); got == nil {
		t.Fatal("nil default registry")
	}
	if _, err := DefaultRegistry().Lookup("standard"); err != nil {
		t.Fatalf("default registry missing standard: %v", err)
	}
}

func TestStandardApplies(t *testing.T) {
	m := StandardModel{}
	for _, dt := range []string{"invoice", "credit_note"} {
		if !m.Applies(dt) {
			t.Errorf("standard should apply to %q", dt)
		}
	}
	for _, dt := range []string{"proposal", "order", "shipment", "bogus"} {
		if m.Applies(dt) {
			t.Errorf("standard must not apply to %q", dt)
		}
	}
}

func TestStandardRenderDeterministic(t *testing.T) {
	m := StandardModel{}
	a := render(t, m, testSubject(), "en")
	// Repeat: gofpdf serializes some tables in Go map order, so one
	// comparison could pass by luck — 25 identical renders prove stability.
	for i := 0; i < 25; i++ {
		if b := render(t, m, testSubject(), "en"); !bytes.Equal(a, b) {
			t.Fatalf("render %d differs from first render", i)
		}
	}
	fr := render(t, m, testSubject(), "fr")
	if bytes.Equal(a, fr) {
		t.Fatal("fr locale rendered identical bytes to en")
	}
	cn := testSubject()
	cn.DocType = string(documents.TypeCreditNote)
	cn.Ref = "CN-202609-0001"
	c := render(t, m, cn, "en")
	if bytes.Equal(a, c) {
		t.Fatal("credit note rendered identical bytes to invoice")
	}
}

func TestStandardRenderRejects(t *testing.T) {
	m := StandardModel{}
	if _, _, err := m.Render(context.Background(), "not a subject", "en"); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad subject err = %v, want ErrValidation", err)
	}
	empty := testSubject()
	empty.Lines = nil
	if _, _, err := m.Render(context.Background(), empty, "en"); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("empty lines err = %v, want ErrValidation", err)
	}
}

// stubRow/stubDB fake platform.DBTX for config-lookup tests.
type stubRow struct {
	v   string
	err error
}

func (r stubRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) > 0 {
		if s, ok := dest[0].(*string); ok {
			*s = r.v
		}
	}
	return nil
}

type stubDB struct {
	v   string
	err error
}

func (s stubDB) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (s stubDB) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, nil
}
func (s stubDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return stubRow{v: s.v, err: s.err}
}

func TestModelForEntityFallback(t *testing.T) {
	ctx := context.Background()
	if got, err := ModelForEntity(ctx, nil, 1); err != nil || got != DefaultModelCode {
		t.Fatalf("nil db = %q,%v want standard", got, err)
	}
	if got, err := ModelForEntity(ctx, stubDB{err: pgx.ErrNoRows}, 1); err != nil || got != DefaultModelCode {
		t.Fatalf("no row = %q,%v want standard", got, err)
	}
	if got, err := ModelForEntity(ctx, stubDB{v: ""}, 1); err != nil || got != DefaultModelCode {
		t.Fatalf("empty value = %q,%v want standard", got, err)
	}
	if got, err := ModelForEntity(ctx, stubDB{v: "  custom  "}, 1); err != nil || got != "custom" {
		t.Fatalf("configured = %q,%v want custom", got, err)
	}
	if _, err := ModelForEntity(ctx, nil, 0); !errors.Is(err, platform.ErrUnauthorized) {
		t.Fatalf("entity 0 err = %v, want ErrUnauthorized", err)
	}
	boom := errors.New("boom")
	if _, err := ModelForEntity(ctx, stubDB{err: boom}, 1); !errors.Is(err, boom) {
		t.Fatalf("db error not propagated: %v", err)
	}
}

func TestModelForEntityPG(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	if got, err := ModelForEntity(ctx, pool, 1); err != nil || got != DefaultModelCode {
		t.Fatalf("unset = %q,%v want standard", got, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ferp_config (entity_id, name, value)
		VALUES (1, $1, 'fancy') ON CONFLICT (entity_id, name)
		DO UPDATE SET value='fancy'`, ModelConfigKey); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if got, err := ModelForEntity(ctx, pool, 1); err != nil || got != "fancy" {
		t.Fatalf("set = %q,%v want fancy", got, err)
	}
	other := pgtest.NewEntity(t, pool, "docgen-co")
	if got, err := ModelForEntity(ctx, pool, other); err != nil || got != DefaultModelCode {
		t.Fatalf("other entity = %q,%v want standard", got, err)
	}
	if !strings.HasPrefix(ModelConfigKey, "FERP_") {
		t.Fatal("config key must follow FERP_* convention")
	}
}
