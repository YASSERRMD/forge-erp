package pos

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jung-kurt/gofpdf"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/docgen"
)

// ReceiptCode is the Kernel-5 template code for till receipts. A completed
// sale renders through the docgen registry under this code (Applies matches
// the code itself — receipts are their own document family, not invoices).
const ReceiptCode = "pos_receipt"

// ReceiptLine is one printed receipt line (server-side price snapshot is NOT
// stored on Sale, so the service resolves labels/prices from catalog at
// render time; a later price change reprints at current prices — see
// DIFFERENCES).
type ReceiptLine struct {
	Label     string `json:"label"`
	Qty       int64  `json:"qty"`
	UnitGross int64  `json:"unit_gross"` // minor units, VAT included
	LineGross int64  `json:"line_gross"` // minor units, VAT included
}

// ReceiptSubject is the renderer input for the receipt layout family.
// IssuedOn is a caller-formatted display date — the renderer never reads the
// clock, keeping output deterministic.
type ReceiptSubject struct {
	Ref        string        `json:"ref"`
	Terminal   string        `json:"terminal"`
	Cashier    string        `json:"cashier"`
	IssuedOn   string        `json:"issued_on"`
	Method     string        `json:"method"`
	Currency   string        `json:"currency"`
	Lines      []ReceiptLine `json:"lines"`
	TotalGross int64         `json:"total_gross"`
	Tendered   int64         `json:"tendered"`
	Change     int64         `json:"change"`
}

const receiptWidth = 40

func receiptPad(key, val string) string {
	if len(key)+len(val) >= receiptWidth {
		return key + " " + val
	}
	return key + strings.Repeat(" ", receiptWidth-len(key)-len(val)) + val
}

// ReceiptText renders the plain-text 40-column till layout (ESC/POS feed and
// the ?format=text download share this builder).
func ReceiptText(sub ReceiptSubject) string {
	var b strings.Builder
	b.WriteString("*** RECEIPT ***\n")
	b.WriteString("Ref: " + sub.Ref + "\n")
	b.WriteString("Terminal: " + sub.Terminal + "  Cashier: " + sub.Cashier + "\n")
	b.WriteString("Date: " + sub.IssuedOn + "\n")
	b.WriteString(strings.Repeat("-", receiptWidth) + "\n")
	for _, l := range sub.Lines {
		left := fmt.Sprintf("%dx %s", l.Qty, l.Label)
		if len(left) > receiptWidth-12 {
			left = left[:receiptWidth-12]
		}
		b.WriteString(receiptPad(left, docgen.FormatMoney(l.LineGross, sub.Currency)) + "\n")
	}
	b.WriteString(strings.Repeat("-", receiptWidth) + "\n")
	b.WriteString(receiptPad("TOTAL:", docgen.FormatMoney(sub.TotalGross, sub.Currency)) + "\n")
	b.WriteString(receiptPad("TENDERED ("+sub.Method+"):", docgen.FormatMoney(sub.Tendered, sub.Currency)) + "\n")
	b.WriteString(receiptPad("CHANGE:", docgen.FormatMoney(sub.Change, sub.Currency)) + "\n")
	b.WriteString("*** THANK YOU ***\n")
	return b.String()
}

// ReceiptModel is the till-receipt template: the same subject renders as PDF
// through the Kernel-5 registry (gofpdf path reused from the invoice model:
// core fonts only, frozen dates, catalog sort — identical subject bytes
// render byte-identical PDFs).
type ReceiptModel struct{}

// Code implements docgen.DocModel.
func (ReceiptModel) Code() string { return ReceiptCode }

// Applies implements docgen.DocModel (receipt family only).
func (ReceiptModel) Applies(docType string) bool { return docType == ReceiptCode }

var receiptFrozenDate = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// Render implements docgen.DocModel: single-pass gofpdf receipt layout.
func (ReceiptModel) Render(_ context.Context, subject any, locale string) (io.Reader, string, error) {
	var sub ReceiptSubject
	switch v := subject.(type) {
	case ReceiptSubject:
		sub = v
	case *ReceiptSubject:
		if v == nil {
			return nil, "", fmt.Errorf("docgen: receipt model needs ReceiptSubject: %w", platform.ErrValidation)
		}
		sub = *v
	default:
		return nil, "", fmt.Errorf("docgen: receipt model needs ReceiptSubject: %w", platform.ErrValidation)
	}
	if len(sub.Lines) == 0 {
		return nil, "", fmt.Errorf("docgen: receipt has no lines: %w", platform.ErrValidation)
	}
	_ = locale // single-language till layout (Dolibarr takepos prints the ticket as-is)

	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetCreationDate(receiptFrozenDate)
	pdf.SetModificationDate(receiptFrozenDate)
	pdf.SetCatalogSort(true)
	pdf.SetTitle(sub.Ref, false)
	pdf.SetAuthor("ForgeERP", false)
	pdf.SetMargins(15, 15, 15)
	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 20)
	pdf.Cell(0, 12, "Receipt")
	pdf.Ln(14)
	pdf.SetFont("Helvetica", "", 11)
	pdf.CellFormat(0, 7, fmt.Sprintf("%s  %s", sub.Ref, sub.IssuedOn), "", 1, "", false, 0, "")
	pdf.CellFormat(0, 7, fmt.Sprintf("Terminal: %s  Cashier: %s", sub.Terminal, sub.Cashier), "", 1, "", false, 0, "")
	pdf.CellFormat(0, 7, "Method: "+sub.Method, "", 1, "", false, 0, "")
	pdf.Ln(4)

	widths := []float64{90, 25, 35, 40}
	pdf.SetFont("Helvetica", "B", 10)
	for i, h := range []string{"Item", "Qty", "Unit", "Total"} {
		pdf.CellFormat(widths[i], 8, h, "1", 0, "", false, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 10)
	for _, l := range sub.Lines {
		pdf.CellFormat(widths[0], 7, l.Label, "1", 0, "", false, 0, "")
		pdf.CellFormat(widths[1], 7, fmt.Sprintf("%d", l.Qty), "1", 0, "R", false, 0, "")
		pdf.CellFormat(widths[2], 7, docgen.FormatMoney(l.UnitGross, sub.Currency), "1", 0, "R", false, 0, "")
		pdf.CellFormat(widths[3], 7, docgen.FormatMoney(l.LineGross, sub.Currency), "1", 0, "R", false, 0, "")
		pdf.Ln(-1)
	}
	pdf.Ln(4)
	pdf.SetFont("Helvetica", "", 11)
	for _, row := range [][2]string{
		{"Total", docgen.FormatMoney(sub.TotalGross, sub.Currency)},
		{"Tendered", docgen.FormatMoney(sub.Tendered, sub.Currency)},
		{"Change", docgen.FormatMoney(sub.Change, sub.Currency)},
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

func init() { docgen.DefaultRegistry().Register(ReceiptModel{}) }
