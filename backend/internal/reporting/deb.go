package reporting

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/partners"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/procurement"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

// Purchasing abstracts supplier invoices for intra-EU arrivals (mirrors the
// Billing seam: db param first, entity-scoped, int64 minor-unit money).
type Purchasing interface {
	ListDocs(ctx context.Context, db platform.DBTX, entityID int64, t documents.DocType, limit, offset int) ([]procurement.Document, error)
}

// VATNumberOf extracts the counterparty's intra-EU VAT number.
//
// There is no VAT column on ferp_organizations and no new migration is
// allowed, so operators store it as custom_fields.vat_number (aliases below
// are accepted for Dolibarr tva_intra-style imports). Empty means "no VAT
// number on file": the movement is excluded from the declaration file.
func VATNumberOf(o partners.Organization) string {
	for _, k := range []string{
		"vat_number", "eu_vat_number", "intra_vat",
		"tva_intracommunautaire", "tva_intra", "vat", "tva",
	} {
		if o.CustomFields == nil {
			break
		}
		v, ok := o.CustomFields[k]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		s = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
		if s != "" {
			return s
		}
	}
	return ""
}

// IntraMovement is one declarable intra-EU line: a validated invoice to/from
// an EU counterparty outside the home country whose VAT number is on file.
// Money is int64 minor units; Period is YYYY-MM of the document date.
type IntraMovement struct {
	Direction string `json:"direction"` // "dispatch" (sales) | "arrival" (purchases)
	Flow      string `json:"flow"`      // "goods" (DEB) | "services" (DES)
	Country   string `json:"country"`   // counterparty ISO-3166 alpha-2
	VATNumber string `json:"vat_number"`
	Partner   string `json:"partner"`
	DocRef    string `json:"doc_ref"`
	Period    string `json:"period"` // YYYY-MM
	Net       int64  `json:"net"`
	VAT       int64  `json:"vat"`
}

// French declaration regimes carried on each detail line.
const (
	RegimeDispatchGoods   = "21" // DEB expedition (goods sent)
	RegimeArrivalGoods    = "25" // DEB introduction (goods received)
	RegimeDispatchService = "26" // DES services supplied
	RegimeArrivalService  = "27" // DES services received
)

// RegimeFor maps a movement to its declaration regime code.
func RegimeFor(direction, flow string) string {
	if direction == "arrival" {
		if flow == "services" {
			return RegimeArrivalService
		}
		return RegimeArrivalGoods
	}
	if flow == "services" {
		return RegimeDispatchService
	}
	return RegimeDispatchGoods
}

// CollectIntraMovements gathers declarable intra-EU sales (dispatches) and
// supplier invoices (arrivals): validated-or-later documents only (drafts and
// cancelled excluded), EU counterparty outside home, VAT number present,
// optional YYYY-MM period filter. Entity scoping rides on the underlying
// seams (ListDocs + OrgByID are entity-keyed); orphans are skipped.
//
// Flow classification: a document is "services" when every line with a known
// product resolves to a service and at least one line does; anything else
// (goods lines, mixed lines, unknown products, free-text lines) is "goods".
// A nil stock seam (or unknown product) therefore defaults to goods.
func CollectIntraMovements(ctx context.Context, db platform.DBTX, entityID int64, home, period string,
	billing Billing, purchasing Purchasing, orgs Orgs, stock Stock) ([]IntraMovement, error) {
	home = strings.ToUpper(strings.TrimSpace(home))
	if period != "" {
		if _, err := time.Parse("2006-01", period); err != nil {
			return nil, fmt.Errorf("reporting: bad period %q (want YYYY-MM): %w", period, platform.ErrValidation)
		}
	}
	var prodType map[int64]catalog.ProductType
	if stock != nil {
		prods, err := stock.ListProducts(ctx, db, entityID, 500, 0)
		if err != nil {
			return nil, err
		}
		prodType = make(map[int64]catalog.ProductType, len(prods))
		for _, p := range prods {
			prodType[p.ID] = p.Type
		}
	}
	classify := func(lines []documents.Line) string {
		seen := false
		for _, l := range lines {
			if l.ProductID == 0 {
				return "goods" // free-text line: conservatively DEB goods
			}
			t, ok := prodType[l.ProductID]
			if !ok {
				return "goods" // unknown product: conservatively DEB goods
			}
			if t != catalog.ProductService {
				return "goods"
			}
			seen = true
		}
		if seen {
			return "services"
		}
		return "goods"
	}

	var out []IntraMovement
	accept := func(orgID int64, docPeriod string, ref string, net, vat int64, lines []documents.Line, direction string) {
		if period != "" && docPeriod != period {
			return
		}
		o, err := orgs.OrgByID(ctx, db, entityID, orgID)
		if err != nil {
			return // orphan document: excluded, never blocks the report
		}
		cc := strings.ToUpper(strings.TrimSpace(o.Address.Country))
		if cc == "" || cc == home || !euMembers[cc] {
			return
		}
		vatNo := VATNumberOf(o)
		if vatNo == "" {
			return // VAT number required for DEB/DES filing
		}
		out = append(out, IntraMovement{
			Direction: direction, Flow: classify(lines),
			Country: cc, VATNumber: vatNo, Partner: o.Name,
			DocRef: ref, Period: docPeriod, Net: net, VAT: vat,
		})
	}

	if billing != nil {
		docs, err := billing.ListDocs(ctx, db, entityID, documents.TypeInvoice, 500, 0)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			if d.Status == sales.InvoiceDraft || d.Status == sales.StatusCancelled {
				continue
			}
			accept(d.OrgID, d.CreatedAt.UTC().Format("2006-01"), d.Ref,
				d.Totals.Net, d.Totals.VAT, d.Lines, "dispatch")
		}
	}
	if purchasing != nil {
		docs, err := purchasing.ListDocs(ctx, db, entityID, documents.TypeSupplierInvoice, 500, 0)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			if d.Status == procurement.Draft || d.Status == procurement.Cancelled {
				continue
			}
			accept(d.OrgID, d.CreatedAt.UTC().Format("2006-01"), d.Ref,
				d.Totals.Net, d.Totals.VAT, d.Lines, "arrival")
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Direction != out[j].Direction {
			return out[i].Direction < out[j].Direction
		}
		if out[i].Country != out[j].Country {
			return out[i].Country < out[j].Country
		}
		if out[i].VATNumber != out[j].VATNumber {
			return out[i].VATNumber < out[j].VATNumber
		}
		return out[i].DocRef < out[j].DocRef
	})
	return out, nil
}

// FilterMovements restricts movements by direction (dispatch|arrival|all) and
// flow (goods|services|all); unknown values fail with ErrValidation.
func FilterMovements(moves []IntraMovement, direction, flow string) ([]IntraMovement, error) {
	direction = strings.ToLower(strings.TrimSpace(direction))
	flow = strings.ToLower(strings.TrimSpace(flow))
	if direction == "" {
		direction = "all"
	}
	if flow == "" {
		flow = "all"
	}
	switch direction {
	case "all", "dispatch", "arrival":
	default:
		return nil, fmt.Errorf("reporting: bad direction %q: %w", direction, platform.ErrValidation)
	}
	switch flow {
	case "all", "goods", "services":
	default:
		return nil, fmt.Errorf("reporting: bad flow %q: %w", flow, platform.ErrValidation)
	}
	var out []IntraMovement
	for _, m := range moves {
		if direction != "all" && m.Direction != direction {
			continue
		}
		if flow != "all" && m.Flow != flow {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// DEB fixed-width parity layout v1 (documented here; the operator validates
// the filed output against the official DGDDI DEB / DES schemas before
// submission — exact official code tables and thresholds are operator-side):
//
//	Header (41 chars, LF-terminated):
//	  "H"(1) | period YYYYMM (6) | home country (2) |
//	  declarant VAT, left-justified space-padded to 14 (14) |
//	  line count, zero-padded (6) | total net cents, zero-padded (12)
//	Detail (55 chars, LF-terminated):
//	  "L"(1) | flow "E" dispatch / "I" arrival (1) | regime (2) |
//	  partner country (2) | partner VAT, left-justified space-padded to 14 (14) |
//	  net cents, zero-padded (12) | period YYYYMM (6) |
//	  doc ref, left-justified space-padded to 16 (16) | "G" goods / "S" services (1)
//
// Goods lines (regimes 21/25) feed the DEB goods declaration; services lines
// (26/27) feed the DES declaration — splitting the file per official schema
// is operator-side. Amounts are int64 minor units (never float).
func FormatDEB(filePeriod, home, declarant string, moves []IntraMovement) string {
	fp := strings.ReplaceAll(filePeriod, "-", "")
	var total int64
	for _, m := range moves {
		total += m.Net
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "H%6.6s%2.2s%-14.14s%06d%012d\n", fp, home, declarant, len(moves), total)
	for _, m := range moves {
		flow := "E"
		if m.Direction == "arrival" {
			flow = "I"
		}
		kind := "G"
		if m.Flow == "services" {
			kind = "S"
		}
		per := strings.ReplaceAll(m.Period, "-", "")
		fmt.Fprintf(&sb, "L%s%s%2.2s%-14.14s%012d%6.6s%-16.16s%s\n",
			flow, RegimeFor(m.Direction, m.Flow), m.Country,
			m.VATNumber, m.Net, per, m.DocRef, kind)
	}
	return sb.String()
}

// FormatIntraCSV renders movements as CSV (header + one row per movement;
// money stays int64 minor units in net_cents/vat_cents columns).
func FormatIntraCSV(moves []IntraMovement) string {
	var sb strings.Builder
	sb.WriteString("direction,flow,country,partner_vat,partner_name,doc_ref,period,net_cents,vat_cents\n")
	q := func(s string) string {
		if strings.ContainsAny(s, ",\"\n") {
			return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
		}
		return s
	}
	for _, m := range moves {
		fmt.Fprintf(&sb, "%s,%s,%s,%s,%s,%s,%s,%d,%d\n",
			m.Direction, m.Flow, m.Country, m.VATNumber,
			q(m.Partner), q(m.DocRef), m.Period, m.Net, m.VAT)
	}
	return sb.String()
}
