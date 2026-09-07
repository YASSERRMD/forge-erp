// Package dataio implements CSV data exchange (Dolibarr exports/imports):
// organizations and products export to CSV and import back with per-row
// validation. Money in CSV is decimal major units; the ledger keeps minor
// units internally.
package dataio

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/partners"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// OrgStore abstracts organization persistence for exchange.
type OrgStore interface {
	CreateOrg(ctx context.Context, o *partners.Organization) error
	ListOrgs(ctx context.Context, entityID int64, limit, offset int) ([]partners.Organization, error)
}

// ProductStore abstracts product persistence for exchange.
type ProductStore interface {
	CreateProduct(ctx context.Context, p *catalog.Product) error
	ListProducts(ctx context.Context, entityID int64, limit, offset int) ([]catalog.Product, error)
}

// Deps wires handlers to the owning stores.
type Deps struct {
	Orgs     OrgStore
	Products ProductStore
	Bus      platform.Bus
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the exchange surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("dataio", "export", "read")).Get("/exports/organizations.csv", h.ExportOrgs)
	r.With(mw("dataio", "export", "read")).Get("/exports/products.csv", h.ExportProducts)
	r.With(mw("dataio", "import", "write")).Post("/imports/organizations", h.ImportOrgs)
	r.With(mw("dataio", "import", "write")).Post("/imports/products", h.ImportProducts)
}

// Handler implements the exchange surface.
type Handler struct{ deps Deps }

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func entityOf(r *http.Request) int64 {
	if u, ok := identity.AuthUser(r); ok && u.EntityID != 0 {
		return u.EntityID
	}
	return 1
}

func storeErrorCode(err error) int {
	switch {
	case errors.Is(err, identity.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, identity.ErrVersionConflict):
		return http.StatusConflict
	case err != nil && strings.Contains(err.Error(), "duplicate"):
		return http.StatusConflict
	default:
		return http.StatusUnprocessableEntity
	}
}

var orgHeader = []string{"name", "customer_code", "supplier_code", "email", "phone", "is_customer", "is_supplier"}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y":
		return true, nil
	case "0", "false", "no", "n", "":
		return false, nil
	default:
		return false, fmt.Errorf("bad boolean %q", s)
	}
}

// ExportOrgs streams all organizations as CSV.
func (h *Handler) ExportOrgs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="organizations.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write(orgHeader)
	for offset := 0; ; offset += 500 {
		batch, err := h.deps.Orgs.ListOrgs(r.Context(), entityOf(r), 500, offset)
		if err != nil {
			return // headers already sent; truncated export beats a broken one
		}
		for _, o := range batch {
			_ = cw.Write([]string{o.Name, o.CustomerCode, o.SupplierCode, o.Email, o.Phone,
				strconv.FormatBool(o.IsCustomer), strconv.FormatBool(o.IsSupplier)})
		}
		if len(batch) < 500 {
			break
		}
	}
}

var productHeader = []string{"sku", "name", "type", "unit", "net_price", "vat_rate_bps", "stock_tracked"}

// ExportProducts streams all products as CSV (net_price in major units).
func (h *Handler) ExportProducts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="products.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write(productHeader)
	for offset := 0; ; offset += 500 {
		batch, err := h.deps.Products.ListProducts(r.Context(), entityOf(r), 500, offset)
		if err != nil {
			return
		}
		for _, p := range batch {
			_ = cw.Write([]string{p.SKU, p.Name, strconv.Itoa(int(p.Type)), p.Unit,
				fmt.Sprintf("%.2f", float64(p.NetPrice)/100),
				strconv.Itoa(p.VATRateBps), strconv.FormatBool(p.StockTracked)})
		}
		if len(batch) < 500 {
			break
		}
	}
}

// ImportResult summarizes an import run.
type ImportResult struct {
	Created int      `json:"created"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"` // capped, "row N: reason"
}

func readCSV(r *http.Request, want []string) ([][]string, error) {
	defer r.Body.Close()
	rows, err := csv.NewReader(http.MaxBytesReader(nil, r.Body, 4<<20)).ReadAll()
	if err != nil {
		return nil, errors.New("dataio: malformed CSV")
	}
	if len(rows) == 0 {
		return nil, errors.New("dataio: empty CSV")
	}
	if len(rows[0]) != len(want) {
		return nil, fmt.Errorf("dataio: want %d columns (%s)", len(want), strings.Join(want, ","))
	}
	for i, h := range rows[0] {
		if strings.ToLower(strings.TrimSpace(h)) != want[i] {
			return nil, fmt.Errorf("dataio: column %d must be %q", i+1, want[i])
		}
	}
	return rows[1:], nil
}

func capErr(errs []string, row int, err error) []string {
	if len(errs) < 20 {
		errs = append(errs, fmt.Sprintf("row %d: %s", row, err.Error()))
	}
	return errs
}

// ImportOrgs creates organizations row by row, skipping invalid rows.
func (h *Handler) ImportOrgs(w http.ResponseWriter, r *http.Request) {
	rows, err := readCSV(r, orgHeader)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	res := ImportResult{}
	for i, c := range rows {
		o := &partners.Organization{EntityID: entityOf(r), Name: strings.TrimSpace(c[0]),
			CustomerCode: strings.TrimSpace(c[1]), SupplierCode: strings.TrimSpace(c[2]),
			Email: strings.TrimSpace(c[3]), Phone: strings.TrimSpace(c[4])}
		var err error
		if o.IsCustomer, err = parseBool(c[5]); err != nil {
			res.Skipped++
			res.Errors = capErr(res.Errors, i+2, err)
			continue
		}
		if o.IsSupplier, err = parseBool(c[6]); err != nil {
			res.Skipped++
			res.Errors = capErr(res.Errors, i+2, err)
			continue
		}
		o.Status = partners.OrgActive
		if err := h.deps.Orgs.CreateOrg(r.Context(), o); err != nil {
			res.Skipped++
			res.Errors = capErr(res.Errors, i+2, err)
			continue
		}
		res.Created++
	}
	if res.Errors == nil {
		res.Errors = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

// ImportProducts creates products row by row (net_price decimal major units).
func (h *Handler) ImportProducts(w http.ResponseWriter, r *http.Request) {
	rows, err := readCSV(r, productHeader)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	res := ImportResult{}
	for i, c := range rows {
		fail := func(err error) {
			res.Skipped++
			res.Errors = capErr(res.Errors, i+2, err)
		}
		pt, err := strconv.Atoi(strings.TrimSpace(c[2]))
		if err != nil || (pt != 0 && pt != 1) {
			fail(errors.New("dataio: type must be 0 (goods) or 1 (service)"))
			continue
		}
		price, err := strconv.ParseFloat(strings.TrimSpace(c[4]), 64)
		if err != nil || price < 0 {
			fail(errors.New("dataio: bad net_price"))
			continue
		}
		vat, err := strconv.Atoi(strings.TrimSpace(c[5]))
		if err != nil {
			fail(errors.New("dataio: bad vat_rate_bps"))
			continue
		}
		tracked, err := parseBool(c[6])
		if err != nil {
			fail(err)
			continue
		}
		p := &catalog.Product{EntityID: entityOf(r), SKU: strings.TrimSpace(c[0]),
			Name: strings.TrimSpace(c[1]), Type: catalog.ProductType(pt), Unit: strings.TrimSpace(c[3]),
			NetPrice: int64(price*100 + 0.5), VATRateBps: vat, Status: catalog.ProductActive,
			StockTracked: tracked}
		if err := h.deps.Products.CreateProduct(r.Context(), p); err != nil {
			fail(err)
			continue
		}
		res.Created++
	}
	if res.Errors == nil {
		res.Errors = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}
