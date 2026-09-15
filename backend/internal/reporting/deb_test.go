package reporting

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/partners"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/procurement"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

func TestVATNumberOf(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
		want   string
	}{
		{"primary", map[string]any{"vat_number": "de123 456"}, "DE123456"},
		{"alias tva_intra", map[string]any{"tva_intra": "it987"}, "IT987"},
		{"alias vat", map[string]any{"vat": "ESX1"}, "ESX1"},
		{"non-string ignored", map[string]any{"vat_number": 123}, ""},
		{"blank ignored", map[string]any{"vat_number": "  "}, ""},
		{"missing", nil, ""},
	}
	for _, c := range cases {
		o := partners.Organization{CustomFields: c.fields}
		if got := VATNumberOf(o); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func goldenMoves() []IntraMovement {
	return []IntraMovement{
		{Direction: "dispatch", Flow: "goods", Country: "DE", VATNumber: "DE123456789",
			Partner: "Berlin GmbH", DocRef: "INV-202609-0001", Period: "2026-09", Net: 1000, VAT: 190},
		{Direction: "arrival", Flow: "goods", Country: "IT", VATNumber: "IT987654321",
			Partner: "Roma Spa", DocRef: "SINV-202609-0001", Period: "2026-09", Net: 2500, VAT: 550},
		{Direction: "dispatch", Flow: "services", Country: "ES", VATNumber: "ESX1234567",
			Partner: "Madrid SL", DocRef: "INV-202609-0002", Period: "2026-09", Net: 400, VAT: 84},
	}
}

func TestFormatDEBGolden(t *testing.T) {
	got := FormatDEB("2026-09", "FR", "FR12345678901", goldenMoves())
	want := "H202609FRFR12345678901 000003000000003900\n" +
		"LE21DEDE123456789   000000001000202609INV-202609-0001 G\n" +
		"LI25ITIT987654321   000000002500202609SINV-202609-0001G\n" +
		"LE26ESESX1234567    000000000400202609INV-202609-0002 S\n"
	if got != want {
		t.Fatalf("DEB mismatch:\n got %q\nwant %q", got, want)
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines[0]) != 41 {
		t.Fatalf("header len=%d want 41", len(lines[0]))
	}
	for i, l := range lines[1:] {
		if len(l) != 55 {
			t.Fatalf("line %d len=%d want 55 (%q)", i, len(l), l)
		}
	}
}

func TestFormatDEBEmpty(t *testing.T) {
	got := FormatDEB("2026-09", "FR", "FR1", nil)
	want := "H202609FRFR1           000000000000000000\n"
	if got != want {
		t.Fatalf("empty DEB: got %q want %q", got, want)
	}
}

func TestFormatIntraCSVGolden(t *testing.T) {
	got := FormatIntraCSV(goldenMoves())
	want := "direction,flow,country,partner_vat,partner_name,doc_ref,period,net_cents,vat_cents\n" +
		"dispatch,goods,DE,DE123456789,Berlin GmbH,INV-202609-0001,2026-09,1000,190\n" +
		"arrival,goods,IT,IT987654321,Roma Spa,SINV-202609-0001,2026-09,2500,550\n" +
		"dispatch,services,ES,ESX1234567,Madrid SL,INV-202609-0002,2026-09,400,84\n"
	if got != want {
		t.Fatalf("CSV mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestFilterMovements(t *testing.T) {
	moves := goldenMoves()
	got, err := FilterMovements(moves, "dispatch", "goods")
	if err != nil || len(got) != 1 || got[0].Country != "DE" {
		t.Fatalf("filter=%+v err=%v", got, err)
	}
	if _, err := FilterMovements(moves, "sideways", "all"); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad direction err=%v", err)
	}
	if _, err := FilterMovements(moves, "all", "liquid"); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad flow err=%v", err)
	}
}

func collectFixture(t *testing.T) (Billing, Purchasing, Orgs, Stock) {
	t.Helper()
	ctx := context.Background()
	pstore := partners.NewMemoryStore()
	mkOrg := func(name, cc, code string, supplier bool, vat map[string]any) int64 {
		o := &partners.Organization{EntityID: 1, Name: name,
			Address: partners.Address{Country: cc}, CustomFields: vat}
		if supplier {
			o.IsSupplier = true
			o.SupplierCode = code
		} else {
			o.IsCustomer = true
			o.CustomerCode = code
		}
		if err := pstore.CreateOrg(ctx, nil, o); err != nil {
			t.Fatalf("org %s: %v", name, err)
		}
		return o.ID
	}
	deGoods := mkOrg("Berlin", "DE", "DE-1", false, map[string]any{"vat_number": "DE111"})
	deNoVAT := mkOrg("Hamburg", "DE", "DE-2", false, nil)
	frHome := mkOrg("Paris", "FR", "FR-1", false, map[string]any{"vat_number": "FR111"})
	usCust := mkOrg("NYC", "US", "US-1", false, map[string]any{"vat_number": "US111"})
	itSup := mkOrg("Roma", "IT", "IT-1", true, map[string]any{"vat_number": "IT222"})
	esSvc := mkOrg("Madrid", "ES", "ES-1", false, map[string]any{"tva_intra": "es 333"})

	cstore := catalog.NewMemoryStore()
	goods := &catalog.Product{EntityID: 1, SKU: "G-1", Name: "Goods", Type: catalog.ProductGoods,
		Status: catalog.ProductActive, StockTracked: true}
	if err := cstore.CreateProduct(ctx, nil, goods); err != nil {
		t.Fatalf("goods product: %v", err)
	}
	svc := &catalog.Product{EntityID: 1, SKU: "S-1", Name: "Service", Type: catalog.ProductService,
		Status: catalog.ProductActive}
	if err := cstore.CreateProduct(ctx, nil, svc); err != nil {
		t.Fatalf("service product: %v", err)
	}

	billing := sales.NewMemoryStore()
	mkInv := func(org int64, pid int64, validate bool) {
		d := &sales.Document{EntityID: 1, Type: documents.TypeInvoice, OrgID: org,
			Currency: "EUR", RateToBase: 1000000,
			Lines: []documents.Line{{ProductID: pid, Label: "x", Qty: 1, UnitNet: 1000, VATRateBps: 2000}}}
		if err := billing.CreateDoc(ctx, nil, d, "202609"); err != nil {
			t.Fatalf("invoice: %v", err)
		}
		if validate {
			if _, err := billing.SetStatus(ctx, nil, d.EntityID, d.ID, sales.InvoiceValidated); err != nil {
				t.Fatalf("validate: %v", err)
			}
		}
	}
	mkInv(deGoods, goods.ID, true)  // dispatch goods DE
	mkInv(deNoVAT, goods.ID, true)  // excluded: no VAT number
	mkInv(frHome, goods.ID, true)   // excluded: home country
	mkInv(usCust, goods.ID, true)   // excluded: non-EU
	mkInv(deGoods, goods.ID, false) // excluded: draft
	mkInv(esSvc, svc.ID, true)      // dispatch services ES

	purchasing := procurement.NewMemoryStore()
	sd := &procurement.Document{EntityID: 1, Type: documents.TypeSupplierInvoice, OrgID: itSup,
		Currency: "EUR", RateToBase: 1000000,
		Lines: []documents.Line{{ProductID: goods.ID, Label: "parts", Qty: 2, UnitNet: 500, VATRateBps: 2000}}}
	if err := purchasing.CreateDoc(ctx, nil, sd, "202609"); err != nil {
		t.Fatalf("supplier invoice: %v", err)
	}
	if _, err := purchasing.SetStatus(ctx, nil, sd.EntityID, sd.ID, procurement.Validated); err != nil {
		t.Fatalf("validate supplier invoice: %v", err)
	}
	return billing, purchasing, pstore, cstore
}

func TestCollectIntraMovementsMemory(t *testing.T) {
	ctx := context.Background()
	billing, purchasing, orgs, stock := collectFixture(t)
	moves, err := CollectIntraMovements(ctx, nil, 1, "fr", "", billing, purchasing, orgs, stock)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(moves) != 3 {
		t.Fatalf("moves=%+v want 3", moves)
	}
	// Sorted by direction, country, VAT, ref: arrival IT, dispatch DE, dispatch ES.
	if moves[0].Direction != "arrival" || moves[0].Country != "IT" || moves[0].Net != 1000 {
		t.Fatalf("moves[0]=%+v", moves[0])
	}
	if moves[1].Direction != "dispatch" || moves[1].Country != "DE" || moves[1].VATNumber != "DE111" {
		t.Fatalf("moves[1]=%+v", moves[1])
	}
	if moves[2].Flow != "services" || moves[2].VATNumber != "ES333" || moves[2].Country != "ES" {
		t.Fatalf("moves[2]=%+v", moves[2])
	}
	// Nil purchasing skips arrivals; nil stock defaults flows to goods.
	moves, err = CollectIntraMovements(ctx, nil, 1, "FR", "", billing, nil, orgs, nil)
	if err != nil {
		t.Fatalf("collect nil seams: %v", err)
	}
	if len(moves) != 2 {
		t.Fatalf("nil-seam moves=%+v want 2", moves)
	}
	for _, m := range moves {
		if m.Flow != "goods" {
			t.Fatalf("nil-stock flow=%+v want goods", m)
		}
	}
	// A period matching nothing yields nothing; a bad period is 422.
	moves, err = CollectIntraMovements(ctx, nil, 1, "FR", "1999-01", billing, purchasing, orgs, stock)
	if err != nil || len(moves) != 0 {
		t.Fatalf("period filter moves=%+v err=%v", moves, err)
	}
	if _, err := CollectIntraMovements(ctx, nil, 1, "FR", "09-2026", billing, nil, orgs, nil); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("bad period err=%v", err)
	}
	// Cross-entity scope: entity 2 sees nothing.
	moves, err = CollectIntraMovements(ctx, nil, 2, "FR", "", billing, purchasing, orgs, stock)
	if err != nil || len(moves) != 0 {
		t.Fatalf("entity 2 moves=%+v err=%v", moves, err)
	}
}
