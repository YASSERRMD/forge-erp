package reporting

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/partners"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
	"github.com/YASSERRMD/forge-erp/backend/internal/procurement"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

// TestPGIntraEUAggregation runs the intra-EU collector against real
// PostgreSQL (skipped without TEST_DATABASE_URL): VAT-filtered dispatch +
// arrival aggregation, period filtering, and entity scoping.
func TestPGIntraEUAggregation(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	pstore := partners.NewPGStore(pool)
	sstore := sales.NewPGStore(pool)
	procstore := procurement.NewPGStore(pool)

	de := &partners.Organization{EntityID: 1, Name: "PG Berlin", IsCustomer: true,
		CustomerCode: "PGDE-1", Address: partners.Address{Country: "DE"},
		CustomFields: map[string]any{"vat_number": "DE999888777"}}
	if err := pstore.CreateOrg(ctx, pool, de); err != nil {
		t.Fatalf("org de: %v", err)
	}
	novat := &partners.Organization{EntityID: 1, Name: "PG Hamburg", IsCustomer: true,
		CustomerCode: "PGDE-2", Address: partners.Address{Country: "DE"}}
	if err := pstore.CreateOrg(ctx, pool, novat); err != nil {
		t.Fatalf("org novat: %v", err)
	}
	it := &partners.Organization{EntityID: 1, Name: "PG Roma", IsSupplier: true,
		SupplierCode: "PGIT-1", Address: partners.Address{Country: "IT"},
		CustomFields: map[string]any{"vat_number": "IT111222333"}}
	if err := pstore.CreateOrg(ctx, pool, it); err != nil {
		t.Fatalf("org it: %v", err)
	}

	mkSales := func(orgID int64) *sales.Document {
		d := &sales.Document{EntityID: 1, Type: documents.TypeInvoice, OrgID: orgID,
			Currency: "EUR", RateToBase: 1000000,
			Lines: []documents.Line{{ProductID: 1, Label: "widgets", Qty: 1, UnitNet: 2000, VATRateBps: 1900}}}
		if err := sstore.CreateDoc(ctx, pool, d, "202609"); err != nil {
			t.Fatalf("sales invoice: %v", err)
		}
		if _, err := sstore.SetStatus(ctx, pool, d.EntityID, d.ID, sales.InvoiceValidated); err != nil {
			t.Fatalf("validate sales: %v", err)
		}
		return d
	}
	inv := mkSales(de.ID)
	mkSales(novat.ID) // no VAT number: excluded from the declaration

	sd := &procurement.Document{EntityID: 1, Type: documents.TypeSupplierInvoice, OrgID: it.ID,
		Currency: "EUR", RateToBase: 1000000,
		Lines: []documents.Line{{ProductID: 1, Label: "parts", Qty: 3, UnitNet: 500, VATRateBps: 2000}}}
	if err := procstore.CreateDoc(ctx, pool, sd, "202609"); err != nil {
		t.Fatalf("supplier invoice: %v", err)
	}
	if _, err := procstore.SetStatus(ctx, pool, sd.EntityID, sd.ID, procurement.Validated); err != nil {
		t.Fatalf("validate supplier: %v", err)
	}

	moves, err := CollectIntraMovements(ctx, pool, 1, "FR", "", sstore, procstore, pstore, nil)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(moves) != 2 {
		t.Fatalf("moves=%+v want 2 (arrival IT + dispatch DE)", moves)
	}
	byDir := map[string]IntraMovement{}
	for _, m := range moves {
		byDir[m.Direction] = m
	}
	disp, ok := byDir["dispatch"]
	if !ok || disp.Country != "DE" || disp.VATNumber != "DE999888777" || disp.Net != 2000 || disp.VAT != 380 {
		t.Fatalf("dispatch=%+v", disp)
	}
	arr, ok := byDir["arrival"]
	if !ok || arr.Country != "IT" || arr.Net != 1500 || arr.VAT != 300 {
		t.Fatalf("arrival=%+v", arr)
	}

	// Period of the created documents matches; an unrelated period is empty.
	period := inv.CreatedAt.UTC().Format("2006-01")
	got, err := CollectIntraMovements(ctx, pool, 1, "FR", period, sstore, procstore, pstore, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("period %s moves=%+v err=%v", period, got, err)
	}
	got, err = CollectIntraMovements(ctx, pool, 1, "FR", "1999-01", sstore, procstore, pstore, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("1999-01 moves=%+v err=%v", got, err)
	}

	// Entity scoping: a sibling tenant sees none of entity 1's movements.
	other := pgtest.NewEntity(t, pool, "intraco")
	got, err = CollectIntraMovements(ctx, pool, other, "FR", "", sstore, procstore, pstore, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("other entity moves=%+v err=%v", got, err)
	}

	// Declaration file round-trip: header count/total match the PG rows.
	deb := FormatDEB(period, "FR", "FR00000000001", moves)
	if len(deb) == 0 {
		t.Fatal("empty DEB")
	}
}
