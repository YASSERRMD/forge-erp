// Package dataio adapters: per-context import/export contracts.
//
// The Adapter interface is DEFINED IN dataio. Owning stores are mirrored as
// narrow interfaces (same method shapes as the owning contexts) so members,
// catalog, partners and sales are never modified — dataio adapts to them.
package dataio

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/members"
	"github.com/YASSERRMD/forge-erp/backend/internal/partners"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

// MemberStore mirrors members.Store (narrow: import/export needs only these).
type MemberStore interface {
	CreateMember(ctx context.Context, db platform.DBTX, m *members.Member) error
	ListMembers(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]members.Member, error)
}

// SalesStore mirrors sales.Store (narrow: import/export needs only these).
type SalesStore interface {
	CreateDoc(ctx context.Context, db platform.DBTX, d *sales.Document, yearMonth string) error
	ListDocs(ctx context.Context, db platform.DBTX, entityID int64, t documents.DocType, limit, offset int) ([]sales.Document, error)
}

// Adapter is the per-context import/export contract: canonical column
// mapping, per-row validation, row import, and CSV export.
type Adapter interface {
	// Name is the context key used by the generic endpoint
	// (members | products | orgs | sales).
	Name() string
	// Columns lists canonical field names (the export header and the
	// valid mapping targets).
	Columns() []string
	// ValidateRow checks one mapped record without writing.
	ValidateRow(entityID int64, rec map[string]string) error
	// ImportRow validates and writes one mapped record.
	ImportRow(ctx context.Context, db platform.DBTX, entityID int64, rec map[string]string) error
	// Export returns data rows (header is Columns()).
	Export(ctx context.Context, db platform.DBTX, entityID int64) ([][]string, error)
}

// RowResult is the per-row outcome of an import run.
type RowResult struct {
	Row   int    `json:"row"` // 1-based data row number (header excluded)
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// ImportReport summarizes an import run (dry-run writes nothing).
type ImportReport struct {
	Context string      `json:"context"`
	DryRun  bool        `json:"dry_run"`
	Created int         `json:"created"`
	Skipped int         `json:"skipped"`
	Rows    []RowResult `json:"rows"`
}

// RunImport validates each record and, unless dryRun, writes it.
// Validation failures and write failures both surface per-row; nothing
// is written in dry-run mode.
func RunImport(ctx context.Context, db platform.DBTX, entityID int64, ad Adapter, recs []map[string]string, dryRun bool) ImportReport {
	rep := ImportReport{Context: ad.Name(), DryRun: dryRun, Rows: []RowResult{}}
	for i, rec := range recs {
		row := i + 1
		if err := ad.ValidateRow(entityID, rec); err != nil {
			rep.Skipped++
			rep.Rows = append(rep.Rows, RowResult{Row: row, Error: err.Error()})
			continue
		}
		if dryRun {
			rep.Created++
			rep.Rows = append(rep.Rows, RowResult{Row: row, OK: true})
			continue
		}
		if err := ad.ImportRow(ctx, db, entityID, rec); err != nil {
			rep.Skipped++
			rep.Rows = append(rep.Rows, RowResult{Row: row, Error: err.Error()})
			continue
		}
		rep.Created++
		rep.Rows = append(rep.Rows, RowResult{Row: row, OK: true})
	}
	return rep
}

// ApplyMapping converts CSV rows (header + records) into canonical records
// using mapping[csvColumn] = field. Unmapped columns are ignored; unknown
// targets and missing columns are rejected.
func ApplyMapping(ad Adapter, header []string, rows [][]string, mapping map[string]string) ([]map[string]string, error) {
	valid := map[string]bool{}
	for _, c := range ad.Columns() {
		valid[c] = true
	}
	for src, dst := range mapping {
		if !valid[dst] {
			return nil, fmt.Errorf("dataio: unknown field %q for context %q", dst, ad.Name())
		}
		_ = src
	}
	seen := map[string]bool{}
	for _, h := range header {
		seen[strings.TrimSpace(h)] = true
	}
	for src := range mapping {
		if !seen[src] {
			return nil, fmt.Errorf("dataio: mapped column %q not in CSV header", src)
		}
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.TrimSpace(h)] = i
	}
	out := make([]map[string]string, 0, len(rows))
	for _, r := range rows {
		rec := map[string]string{}
		for src, dst := range mapping {
			if j, ok := idx[src]; ok && j < len(r) {
				rec[dst] = r[j]
			}
		}
		out = append(out, rec)
	}
	return out, nil
}

// DefaultMapping maps identically-named columns (header == canonical names).
func DefaultMapping(ad Adapter, header []string) map[string]string {
	valid := map[string]bool{}
	for _, c := range ad.Columns() {
		valid[c] = true
	}
	m := map[string]string{}
	for _, h := range header {
		if valid[h] {
			m[h] = h
		}
	}
	return m
}

// --- orgs adapter (mirrors partners.Organization) ---

type orgAdapter struct{ store OrgStore }

func (a orgAdapter) Name() string { return "orgs" }

func (a orgAdapter) Columns() []string {
	return []string{"name", "customer_code", "supplier_code", "email", "phone", "is_customer", "is_supplier"}
}

func (a orgAdapter) build(entityID int64, rec map[string]string) (*partners.Organization, error) {
	o := &partners.Organization{EntityID: entityID, Name: strings.TrimSpace(rec["name"]),
		CustomerCode: strings.TrimSpace(rec["customer_code"]), SupplierCode: strings.TrimSpace(rec["supplier_code"]),
		Email: strings.TrimSpace(rec["email"]), Phone: strings.TrimSpace(rec["phone"]),
		Status: partners.OrgActive}
	var err error
	if o.IsCustomer, err = parseBool(rec["is_customer"]); err != nil {
		return nil, err
	}
	if o.IsSupplier, err = parseBool(rec["is_supplier"]); err != nil {
		return nil, err
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	return o, nil
}

func (a orgAdapter) ValidateRow(entityID int64, rec map[string]string) error {
	_, err := a.build(entityID, rec)
	return err
}

func (a orgAdapter) ImportRow(ctx context.Context, db platform.DBTX, entityID int64, rec map[string]string) error {
	o, err := a.build(entityID, rec)
	if err != nil {
		return err
	}
	return a.store.CreateOrg(ctx, db, o)
}

func (a orgAdapter) Export(ctx context.Context, db platform.DBTX, entityID int64) ([][]string, error) {
	var out [][]string
	for offset := 0; ; offset += 500 {
		batch, err := a.store.ListOrgs(ctx, db, entityID, 500, offset)
		if err != nil {
			return nil, err
		}
		for _, o := range batch {
			out = append(out, []string{o.Name, o.CustomerCode, o.SupplierCode, o.Email, o.Phone,
				strconv.FormatBool(o.IsCustomer), strconv.FormatBool(o.IsSupplier)})
		}
		if len(batch) < 500 {
			break
		}
	}
	return out, nil
}

// --- products adapter (mirrors catalog.Product) ---

type productAdapter struct{ store ProductStore }

func (a productAdapter) Name() string { return "products" }

func (a productAdapter) Columns() []string {
	return []string{"sku", "name", "type", "unit", "net_price", "vat_rate_bps", "stock_tracked"}
}

func (a productAdapter) build(entityID int64, rec map[string]string) (*catalog.Product, error) {
	pt, err := strconv.Atoi(strings.TrimSpace(rec["type"]))
	if err != nil || (pt != 0 && pt != 1) {
		return nil, fmt.Errorf("dataio: type must be 0 (goods) or 1 (service)")
	}
	price, err := strconv.ParseFloat(strings.TrimSpace(rec["net_price"]), 64)
	if err != nil || price < 0 {
		return nil, fmt.Errorf("dataio: bad net_price")
	}
	vat, err := strconv.Atoi(strings.TrimSpace(rec["vat_rate_bps"]))
	if err != nil {
		return nil, fmt.Errorf("dataio: bad vat_rate_bps")
	}
	tracked, err := parseBool(rec["stock_tracked"])
	if err != nil {
		return nil, err
	}
	p := &catalog.Product{EntityID: entityID, SKU: strings.TrimSpace(rec["sku"]),
		Name: strings.TrimSpace(rec["name"]), Type: catalog.ProductType(pt), Unit: strings.TrimSpace(rec["unit"]),
		NetPrice: int64(price*100 + 0.5), VATRateBps: vat, Status: catalog.ProductActive,
		StockTracked: tracked}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func (a productAdapter) ValidateRow(entityID int64, rec map[string]string) error {
	_, err := a.build(entityID, rec)
	return err
}

func (a productAdapter) ImportRow(ctx context.Context, db platform.DBTX, entityID int64, rec map[string]string) error {
	p, err := a.build(entityID, rec)
	if err != nil {
		return err
	}
	return a.store.CreateProduct(ctx, db, p)
}

func (a productAdapter) Export(ctx context.Context, db platform.DBTX, entityID int64) ([][]string, error) {
	var out [][]string
	for offset := 0; ; offset += 500 {
		batch, err := a.store.ListProducts(ctx, db, entityID, 500, offset)
		if err != nil {
			return nil, err
		}
		for _, p := range batch {
			out = append(out, []string{p.SKU, p.Name, strconv.Itoa(int(p.Type)), p.Unit,
				fmt.Sprintf("%.2f", float64(p.NetPrice)/100),
				strconv.Itoa(p.VATRateBps), strconv.FormatBool(p.StockTracked)})
		}
		if len(batch) < 500 {
			break
		}
	}
	return out, nil
}

// --- members adapter (mirrors members.Member) ---

type memberAdapter struct{ store MemberStore }

func (a memberAdapter) Name() string { return "members" }

func (a memberAdapter) Columns() []string {
	return []string{"ref", "type_id", "first_name", "last_name", "company", "email"}
}

func (a memberAdapter) build(entityID int64, rec map[string]string) (*members.Member, error) {
	typeID, err := strconv.ParseInt(strings.TrimSpace(rec["type_id"]), 10, 64)
	if err != nil || typeID <= 0 {
		return nil, fmt.Errorf("dataio: bad type_id")
	}
	m := &members.Member{EntityID: entityID, Ref: strings.TrimSpace(rec["ref"]), TypeID: typeID,
		FirstName: strings.TrimSpace(rec["first_name"]), LastName: strings.TrimSpace(rec["last_name"]),
		Company: strings.TrimSpace(rec["company"]), Email: strings.TrimSpace(rec["email"]),
		Status: members.MemberDraft}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

func (a memberAdapter) ValidateRow(entityID int64, rec map[string]string) error {
	_, err := a.build(entityID, rec)
	return err
}

func (a memberAdapter) ImportRow(ctx context.Context, db platform.DBTX, entityID int64, rec map[string]string) error {
	m, err := a.build(entityID, rec)
	if err != nil {
		return err
	}
	return a.store.CreateMember(ctx, db, m)
}

func (a memberAdapter) Export(ctx context.Context, db platform.DBTX, entityID int64) ([][]string, error) {
	var out [][]string
	for offset := 0; ; offset += 500 {
		batch, err := a.store.ListMembers(ctx, db, entityID, 500, offset)
		if err != nil {
			return nil, err
		}
		for _, m := range batch {
			out = append(out, []string{m.Ref, strconv.FormatInt(m.TypeID, 10),
				m.FirstName, m.LastName, m.Company, m.Email})
		}
		if len(batch) < 500 {
			break
		}
	}
	return out, nil
}

// --- sales documents adapter (mirrors sales.Document, one single-line doc per row) ---

type salesAdapter struct {
	store   SalesStore
	docType documents.DocType
}

func (a salesAdapter) Name() string { return "sales" }

func (a salesAdapter) Columns() []string {
	return []string{"type", "org_id", "currency", "label", "qty", "unit_net", "vat_rate_bps"}
}

var salesTypes = map[string]documents.DocType{
	"proposal": documents.TypeProposal, "order": documents.TypeOrder,
	"shipment": documents.TypeShipment, "invoice": documents.TypeInvoice,
	"credit_note": documents.TypeCreditNote,
}

func (a salesAdapter) build(entityID int64, rec map[string]string) (*sales.Document, error) {
	t, ok := salesTypes[strings.TrimSpace(rec["type"])]
	if !ok {
		return nil, fmt.Errorf("dataio: unknown sales type %q", rec["type"])
	}
	orgID, err := strconv.ParseInt(strings.TrimSpace(rec["org_id"]), 10, 64)
	if err != nil || orgID <= 0 {
		return nil, fmt.Errorf("dataio: bad org_id")
	}
	qty, err := strconv.ParseInt(strings.TrimSpace(rec["qty"]), 10, 64)
	if err != nil || qty <= 0 {
		return nil, fmt.Errorf("dataio: bad qty")
	}
	unitNet, err := strconv.ParseInt(strings.TrimSpace(rec["unit_net"]), 10, 64)
	if err != nil || unitNet < 0 {
		return nil, fmt.Errorf("dataio: bad unit_net (minor units)")
	}
	vat, err := strconv.Atoi(strings.TrimSpace(rec["vat_rate_bps"]))
	if err != nil || vat < 0 {
		return nil, fmt.Errorf("dataio: bad vat_rate_bps")
	}
	label := strings.TrimSpace(rec["label"])
	if label == "" {
		return nil, fmt.Errorf("dataio: label required")
	}
	currency := strings.TrimSpace(rec["currency"])
	if currency == "" {
		return nil, fmt.Errorf("dataio: currency required")
	}
	d := &sales.Document{EntityID: entityID, Type: t, OrgID: orgID, Currency: currency,
		RateToBase: 1000000,
		Lines: []documents.Line{{Label: label, Qty: qty, UnitNet: unitNet, VATRateBps: vat}}}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return d, nil
}

func (a salesAdapter) ValidateRow(entityID int64, rec map[string]string) error {
	_, err := a.build(entityID, rec)
	return err
}

func (a salesAdapter) ImportRow(ctx context.Context, db platform.DBTX, entityID int64, rec map[string]string) error {
	d, err := a.build(entityID, rec)
	if err != nil {
		return err
	}
	return a.store.CreateDoc(ctx, db, d, time.Now().UTC().Format("2006-01"))
}

func (a salesAdapter) Export(ctx context.Context, db platform.DBTX, entityID int64) ([][]string, error) {
	var out [][]string
	for offset := 0; ; offset += 500 {
		batch, err := a.store.ListDocs(ctx, db, entityID, a.docType, 500, offset)
		if err != nil {
			return nil, err
		}
		for _, d := range batch {
			for _, l := range d.Lines {
				out = append(out, []string{string(d.Type), strconv.FormatInt(d.OrgID, 10),
					d.Currency, l.Label, strconv.FormatInt(l.Qty, 10),
					strconv.FormatInt(l.UnitNet, 10), strconv.Itoa(l.VATRateBps)})
			}
		}
		if len(batch) < 500 {
			break
		}
	}
	return out, nil
}
