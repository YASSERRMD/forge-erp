// Package reporting composes read-only management reports from the ledger,
// billing and stock seams (Dolibarr margin/compta reports): profit & loss by
// account type, receivables from open invoice balances, and stock valuation
// at PMP per warehouse. No new tables; all math over existing stores.
package reporting

import (
	"context"
	"sort"

	"github.com/YASSERRMD/forge-erp/backend/internal/catalog"
	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/sales"
)

// Ledger abstracts the postings needed for P&L.
type Ledger interface {
	Accounts(ctx context.Context, entityID int64) ([]finance.Account, error)
	TrialBalance(ctx context.Context, entityID int64) (map[int64][2]int64, error)
}

// Billing abstracts invoice balances for receivables.
type Billing interface {
	ListDocs(ctx context.Context, entityID int64, t documents.DocType, limit, offset int) ([]sales.Document, error)
	InvoiceBalance(ctx context.Context, invoiceID int64) (int64, error)
}

// Stock abstracts product levels for valuation.
type Stock interface {
	ListProducts(ctx context.Context, entityID int64, limit, offset int) ([]catalog.Product, error)
	Level(ctx context.Context, productID, warehouseID int64) (catalog.StockLevel, error)
}

// AccountLine is one P&L row (balance signed: revenue/equity/liability as
// credit-minus-debit, asset/expense as debit-minus-credit).
type AccountLine struct {
	Code    string `json:"code"`
	Label   string `json:"label"`
	Type    string `json:"type"`
	Balance int64  `json:"balance"` // minor units, signed per type
}

// ProfitAndLoss is the period report (all posted entries; no date slicing in lite).
type ProfitAndLoss struct {
	Lines   []AccountLine `json:"lines"`
	Revenue int64         `json:"revenue"`
	Expense int64         `json:"expense"`
	Net     int64         `json:"net"`
}

// BuildPNL assembles P&L from the chart and trial balance.
func BuildPNL(ctx context.Context, entityID int64, ledger Ledger) (ProfitAndLoss, error) {
	accts, err := ledger.Accounts(ctx, entityID)
	if err != nil {
		return ProfitAndLoss{}, err
	}
	tb, err := ledger.TrialBalance(ctx, entityID)
	if err != nil {
		return ProfitAndLoss{}, err
	}
	var out ProfitAndLoss
	for _, a := range accts {
		sums := tb[a.ID]
		var bal int64
		switch a.Type {
		case "revenue", "equity", "liability":
			bal = sums[1] - sums[0]
		default: // asset, expense
			bal = sums[0] - sums[1]
		}
		out.Lines = append(out.Lines, AccountLine{Code: a.Code, Label: a.Label, Type: a.Type, Balance: bal})
		switch a.Type {
		case "revenue":
			out.Revenue += bal
		case "expense":
			out.Expense += bal
		}
	}
	out.Net = out.Revenue - out.Expense
	return out, nil
}

// MonthlyPoint is one revenue bucket.
type MonthlyPoint struct {
	Month string `json:"month"` // YYYY-MM
	Net   int64  `json:"net"`
	Gross int64  `json:"gross"`
	Count int64  `json:"count"`
}

// SalesMonthly buckets non-draft, non-cancelled invoices by creation month
// (latest 12 months with activity, ascending).
func SalesMonthly(ctx context.Context, entityID int64, billing Billing) ([]MonthlyPoint, error) {
	docs, err := billing.ListDocs(ctx, entityID, documents.TypeInvoice, 500, 0)
	if err != nil {
		return nil, err
	}
	byMonth := map[string]*MonthlyPoint{}
	for _, d := range docs {
		if d.Status == sales.InvoiceDraft || d.Status == 9 {
			continue
		}
		key := d.CreatedAt.UTC().Format("2006-01")
		p, ok := byMonth[key]
		if !ok {
			p = &MonthlyPoint{Month: key}
			byMonth[key] = p
		}
		p.Net += d.Totals.Net
		p.Gross += d.Totals.Gross
		p.Count++
	}
	keys := make([]string, 0, len(byMonth))
	for k := range byMonth {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 12 {
		keys = keys[len(keys)-12:]
	}
	out := make([]MonthlyPoint, 0, len(keys))
	for _, k := range keys {
		out = append(out, *byMonth[k])
	}
	return out, nil
}

// Receivable is one unpaid invoice balance.
type Receivable struct {
	InvoiceID int64  `json:"invoice_id"`
	Ref       string `json:"ref"`
	OrgID     int64  `json:"org_id"`
	Gross     int64  `json:"gross"`
	Balance   int64  `json:"balance"`
}

// Receivables lists validated invoices with an outstanding balance.
func Receivables(ctx context.Context, entityID int64, billing Billing) ([]Receivable, int64, error) {
	docs, err := billing.ListDocs(ctx, entityID, documents.TypeInvoice, 500, 0)
	if err != nil {
		return nil, 0, err
	}
	var out []Receivable
	var total int64
	for _, d := range docs {
		if d.Status == sales.InvoiceDraft || d.Status == 9 { // skip drafts/cancelled
			continue
		}
		bal, err := billing.InvoiceBalance(ctx, d.ID)
		if err != nil {
			return nil, 0, err
		}
		if bal > 0 {
			out = append(out, Receivable{InvoiceID: d.ID, Ref: d.Ref,
				OrgID: d.OrgID, Gross: d.Totals.Gross, Balance: bal})
			total += bal
		}
	}
	return out, total, nil
}

// ValuationLine is one product position at PMP.
type ValuationLine struct {
	ProductID int64  `json:"product_id"`
	SKU       string `json:"sku"`
	Qty       int64  `json:"qty"`
	PMP       int64  `json:"pmp"`
	Value     int64  `json:"value"`
}

// StockValuation values every product in a warehouse at PMP.
func StockValuation(ctx context.Context, entityID, warehouseID int64, stock Stock) ([]ValuationLine, int64, error) {
	prods, err := stock.ListProducts(ctx, entityID, 500, 0)
	if err != nil {
		return nil, 0, err
	}
	var out []ValuationLine
	var total int64
	for _, p := range prods {
		lvl, err := stock.Level(ctx, p.ID, warehouseID)
		if err != nil {
			return nil, 0, err
		}
		if lvl.Qty == 0 {
			continue
		}
		pmp := lvl.PMP()
		out = append(out, ValuationLine{ProductID: p.ID, SKU: p.SKU,
			Qty: lvl.Qty, PMP: pmp, Value: lvl.TotalValue})
		total += lvl.TotalValue
	}
	return out, total, nil
}
