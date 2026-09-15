// Package docgen implements Kernel 5 (document generation): printable
// business documents rendered through selectable DocModel templates.
//
// Library decision (Phase 1, verified offline 2026-09-14):
// github.com/jung-kurt/gofpdf v1.16.2 was present in the module cache, so it
// is pinned in go.mod and used directly — pure Go, no cgo, no network at
// render time. No HTML/print-CSS fallback shipped; see docs/DIFFERENCES.md
// only if this ever needs revisiting (it does not today).
//
// Determinism: gofpdf embeds /CreationDate + /ModDate (defaulting to
// time.Now) and serializes its font table in Go map order; every renderer
// freezes both dates to frozenCreationDate and enables catalog sort, and
// templates never call time.Now — issue dates arrive as caller-formatted
// strings inside the subject. Identical subject bytes therefore render
// byte-identical PDFs (gofpdf's trailer /ID is a constant empty pair,
// core fonts are unembedded).
package docgen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jung-kurt/gofpdf"

	"github.com/YASSERRMD/forge-erp/backend/internal/documents"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// frozenCreationDate stamps every generated PDF's /CreationDate metadata.
// Frozen (not wall-clock) so identical input renders byte-identical output.
// The human-visible issue date is content (InvoiceSubject.IssuedOn), not metadata.
var frozenCreationDate = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// DocModel is one printable template: a stable Code selected per entity,
// an Applies guard per document family, and a pure Render of subject → bytes.
type DocModel interface {
	Code() string
	Applies(docType string) bool
	Render(ctx context.Context, subject any, locale string) (reader io.Reader, contentType string, err error)
}

// Registry holds the template set; handlers fall back to DefaultRegistry.
type Registry struct {
	mu     sync.RWMutex
	models map[string]DocModel
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry { return &Registry{models: map[string]DocModel{}} }

// Register adds (or replaces) a model by its Code.
func (r *Registry) Register(m DocModel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.models[m.Code()] = m
}

// Lookup resolves a model code; unknown or blank codes are validation errors (422).
func (r *Registry) Lookup(code string) (DocModel, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if m, ok := r.models[strings.TrimSpace(code)]; ok {
		return m, nil
	}
	return nil, fmt.Errorf("docgen: unknown document model %q: %w", code, platform.ErrValidation)
}

var defaults = NewRegistry()

func init() { defaults.Register(StandardModel{}) }

// DefaultRegistry is the process-wide template set (standard invoice family).
func DefaultRegistry() *Registry { return defaults }

// DefaultModelCode is the fallback template when no entity override exists.
const DefaultModelCode = "standard"

// ModelConfigKey is the ferp_config name selecting an entity's sales template
// (overlay.go convention: scoped key/value rows; env FERP_* wins at boot for
// recognized keys, DB carries the per-entity value at render time).
const ModelConfigKey = "FERP_SALES_DOC_MODEL"

// ModelForEntity reads the entity's template code from ferp_config, falling
// back to DefaultModelCode when no (non-empty) row exists. A nil db also
// falls back (handlers under test wire no database).
func ModelForEntity(ctx context.Context, db platform.DBTX, entityID int64) (string, error) {
	if entityID == 0 {
		return "", platform.ErrUnauthorized
	}
	if db == nil {
		return DefaultModelCode, nil
	}
	var v string
	err := db.QueryRow(ctx, `SELECT value FROM ferp_config WHERE entity_id=$1 AND name=$2`,
		entityID, ModelConfigKey).Scan(&v)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DefaultModelCode, nil
		}
		return "", err
	}
	if strings.TrimSpace(v) == "" {
		return DefaultModelCode, nil
	}
	return strings.TrimSpace(v), nil
}

// FormatMoney renders minor-unit money without floats: 1200/"USD" → "12.00 USD".
func FormatMoney(minor int64, currency string) string {
	neg := minor < 0
	if neg {
		minor = -minor
	}
	s := fmt.Sprintf("%d.%02d %s", minor/100, minor%100, strings.ToUpper(strings.TrimSpace(currency)))
	s = strings.TrimSpace(s)
	if neg {
		return "-" + s
	}
	return s
}

// InvoiceSubject is the renderer input for the invoice layout family
// (invoices + credit notes). IssuedOn is a caller-formatted display date —
// the renderer never reads the clock, keeping output deterministic.
type InvoiceSubject struct {
	Ref      string
	DocType  string // documents.DocType string form ("invoice" | "credit_note")
	EntityID int64
	OrgID    int64
	Currency string
	IssuedOn string
	Lines    []documents.Line
	Totals   documents.Totals
}

// StandardModel is the minimum-viable customer-invoice template; the credit
// note reuses the same layout family with a mirrored title and sign note.
type StandardModel struct{}

// Code implements DocModel.
func (StandardModel) Code() string { return DefaultModelCode }

// Applies implements DocModel (invoice family only).
func (StandardModel) Applies(docType string) bool {
	return docType == string(documents.TypeInvoice) || docType == string(documents.TypeCreditNote)
}

type invStrings struct{ title, billed, issued, desc, qty, unit, vat, total, net, vatTot, gross string }

func stringsFor(locale string) invStrings {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "fr") {
		return invStrings{"Facture", "Facturé à", "Émise le", "Désignation", "Qté", "P.U. HT",
			"TVA %", "Total", "Total HT", "TVA", "Total TTC"}
	}
	return invStrings{"Invoice", "Billed to", "Issued", "Description", "Qty", "Unit net",
		"VAT %", "Total", "Net total", "VAT", "Gross total"}
}

// Render implements DocModel: single-pass gofpdf layout, core fonts only.
func (StandardModel) Render(_ context.Context, subject any, locale string) (io.Reader, string, error) {
	sub, ok := subject.(InvoiceSubject)
	if !ok {
		return nil, "", fmt.Errorf("docgen: standard model needs InvoiceSubject: %w", platform.ErrValidation)
	}
	if len(sub.Lines) == 0 {
		return nil, "", fmt.Errorf("docgen: invoice has no lines: %w", platform.ErrValidation)
	}
	ls := stringsFor(locale)
	title := ls.title
	if sub.DocType == string(documents.TypeCreditNote) {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "fr") {
			title = "Avoir"
		} else {
			title = "Credit note"
		}
	}

	pdf := gofpdf.New("P", "mm", "A4", "")
	// Determinism guards (identical subject → byte-identical PDF):
	// frozen /CreationDate + /ModDate (gofpdf otherwise stamps time.Now),
	// catalog sort (font objects are collected in Go map order).
	pdf.SetCreationDate(frozenCreationDate)
	pdf.SetModificationDate(frozenCreationDate)
	pdf.SetCatalogSort(true)
	pdf.SetTitle(sub.Ref, false)
	pdf.SetAuthor("ForgeERP", false)
	pdf.SetMargins(15, 15, 15)
	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 20)
	pdf.Cell(0, 12, title)
	pdf.Ln(14)
	pdf.SetFont("Helvetica", "", 11)
	pdf.CellFormat(0, 7, fmt.Sprintf("%s  %s", sub.Ref, sub.IssuedOn), "", 1, "", false, 0, "")
	pdf.CellFormat(0, 7, fmt.Sprintf("%s: #%d", ls.billed, sub.OrgID), "", 1, "", false, 0, "")
	pdf.Ln(4)

	widths := []float64{80, 20, 30, 20, 40}
	head := []string{ls.desc, ls.qty, ls.unit, ls.vat, ls.total}
	pdf.SetFont("Helvetica", "B", 10)
	for i, h := range head {
		pdf.CellFormat(widths[i], 8, h, "1", 0, "", false, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 10)
	for _, l := range sub.Lines {
		vatPct := fmt.Sprintf("%d.%02d", l.VATRateBps/100, l.VATRateBps%100)
		cells := []string{
			l.Label,
			fmt.Sprintf("%d", l.Qty),
			FormatMoney(l.UnitNet, sub.Currency),
			vatPct,
			FormatMoney(l.Net()+l.VAT(), sub.Currency),
		}
		align := []string{"", "R", "R", "R", "R"}
		for i, c := range cells {
			pdf.CellFormat(widths[i], 7, c, "1", 0, align[i], false, 0, "")
		}
		pdf.Ln(-1)
	}
	pdf.Ln(4)
	pdf.SetFont("Helvetica", "", 11)
	for _, row := range [][2]string{
		{ls.net, FormatMoney(sub.Totals.Net, sub.Currency)},
		{ls.vatTot, FormatMoney(sub.Totals.VAT, sub.Currency)},
		{ls.gross, FormatMoney(sub.Totals.Gross, sub.Currency)},
	} {
		pdf.CellFormat(150, 7, row[0], "", 0, "R", false, 0, "")
		pdf.CellFormat(40, 7, row[1], "", 1, "R", false, 0, "")
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, "", err
	}
	return bytes.NewReader(buf.Bytes()), "application/pdf", nil
}
