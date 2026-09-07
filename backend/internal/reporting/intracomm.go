package reporting

import (
	"context"
	"strings"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/partners"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

// euMembers is the EU country set (ISO-3166 alpha-2) for intra-EU reporting.
var euMembers = map[string]bool{
	"AT": true, "BE": true, "BG": true, "CY": true, "CZ": true, "DE": true,
	"DK": true, "EE": true, "ES": true, "FI": true, "FR": true, "GR": true,
	"HR": true, "HU": true, "IE": true, "IT": true, "LT": true, "LU": true,
	"LV": true, "MT": true, "NL": true, "PL": true, "PT": true, "RO": true,
	"SE": true, "SI": true, "SK": true,
}

// Orgs abstracts customer lookup for country attribution.
type Orgs interface {
	OrgByID(ctx context.Context, id int64) (partners.Organization, error)
}

// IntraRow is one destination-country aggregate (Dolibarr intracommreport:
// dispatches by customer country, goods net + VAT).
type IntraRow struct {
	Country   string `json:"country"`
	Customers int64  `json:"customers"`
	Net       int64  `json:"net"`
	VAT       int64  `json:"vat"`
}

// IntraEU aggregates validated/closed invoices to EU customers outside the
// home country (home = caller's country code, e.g. FR).
func IntraEU(ctx context.Context, entityID int64, home string, billing Billing, orgs Orgs) ([]IntraRow, error) {
	home = strings.ToUpper(strings.TrimSpace(home))
	docs, err := billing.ListDocs(ctx, entityID, documents.TypeInvoice, 500, 0)
	if err != nil {
		return nil, err
	}
	byCountry := map[string]*IntraRow{}
	seen := map[string]map[int64]bool{}
	for _, d := range docs {
		if d.Status == sales.InvoiceDraft || d.Status == 9 { // skip drafts/cancelled
			continue
		}
		o, err := orgs.OrgByID(ctx, d.OrgID)
		if err != nil {
			continue // orphan invoice: excluded, never blocks the report
		}
		cc := strings.ToUpper(strings.TrimSpace(o.Address.Country))
		if cc == "" || cc == home || !euMembers[cc] {
			continue
		}
		row, ok := byCountry[cc]
		if !ok {
			row = &IntraRow{Country: cc}
			byCountry[cc] = row
			seen[cc] = map[int64]bool{}
		}
		if !seen[cc][o.ID] {
			seen[cc][o.ID] = true
			row.Customers++
		}
		row.Net += d.Totals.Net
		row.VAT += d.Totals.VAT
	}
	out := make([]IntraRow, 0, len(byCountry))
	for _, row := range byCountry {
		out = append(out, *row)
	}
	return out, nil
}
