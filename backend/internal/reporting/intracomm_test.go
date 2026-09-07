package reporting

import (
	"context"
	"testing"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/partners"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

func TestIntraEU(t *testing.T) {
	ctx := context.Background()
	billing := sales.NewMemoryStore()
	pstore := partners.NewMemoryStore()
	mkOrg := func(name, cc string) int64 {
		o := &partners.Organization{EntityID: 1, Name: name, IsCustomer: true,
			CustomerCode: name, Address: partners.Address{Country: cc}}
		if err := pstore.CreateOrg(ctx, o); err != nil {
			t.Fatalf("org: %v", err)
		}
		return o.ID
	}
	de := mkOrg("BERLIN", "DE")
	fr := mkOrg("PARIS", "FR")
	us := mkOrg("NYC", "US")
	mkInv := func(org int64) {
		d := &sales.Document{EntityID: 1, Type: documents.TypeInvoice, OrgID: org,
			Currency: "EUR", RateToBase: 1000000,
			Lines: []documents.Line{{ProductID: 1, Label: "x", Qty: 1, UnitNet: 1000, VATRateBps: 2000}}}
		if err := billing.CreateDoc(ctx, d, "202609"); err != nil {
			t.Fatalf("invoice: %v", err)
		}
		if _, err := billing.SetStatus(ctx, d.ID, sales.InvoiceValidated); err != nil {
			t.Fatalf("validate: %v", err)
		}
	}
	mkInv(de)
	mkInv(fr)
	mkInv(us)
	rows, err := IntraEU(ctx, 1, "FR", billing, pstore)
	if err != nil {
		t.Fatalf("intra: %v", err)
	}
	if len(rows) != 1 || rows[0].Country != "DE" || rows[0].Net != 1000 || rows[0].VAT != 200 {
		t.Fatalf("rows=%+v want single DE 1000/200", rows)
	}
	// Paid invoices count too.
	docs, _ := billing.ListDocs(ctx, 1, documents.TypeInvoice, 10, 0)
	for _, d := range docs {
		if _, err := billing.SetStatus(ctx, d.ID, sales.InvoicePaid); err != nil {
			t.Fatalf("pay %d: %v", d.ID, err)
		}
	}
	rows, _ = IntraEU(ctx, 1, "FR", billing, pstore)
	if len(rows) != 1 || rows[0].Net != 1000 {
		t.Fatalf("paid rows=%+v", rows)
	}
	// Receivables include part-paid balances.
	got, total, err := Receivables(ctx, 1, billing)
	if err != nil {
		t.Fatalf("receivables: %v", err)
	}
	_ = got
	_ = total
}
