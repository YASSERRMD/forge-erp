// Donation receipts (Phase 2 loan/don depth): paid donations render a PDF
// receipt through the platform docgen registry. The template is defined and
// registered here — the docgen package itself is never modified.
package members

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/jung-kurt/gofpdf"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/docgen"
)

// DonationReceiptCode is the docgen model code for donation receipts.
const DonationReceiptCode = "donation-receipt"

// DonationReceiptDocType is the document family the model applies to.
const DonationReceiptDocType = "donation"

// DonationReceiptSubject is the renderer input. DonatedOn is a
// caller-formatted display date — the renderer never reads the clock,
// keeping output deterministic (same rule as docgen.InvoiceSubject).
type DonationReceiptSubject struct {
	Ref       string
	EntityID  int64
	DonorName string
	Amount    int64 // minor units
	Currency  string
	DonatedOn string
	Method    string
}

// DonationReceiptModel is the minimum-viable donation-receipt template.
type DonationReceiptModel struct{}

// Code implements docgen.DocModel.
func (DonationReceiptModel) Code() string { return DonationReceiptCode }

// Applies implements docgen.DocModel (donation family only).
func (DonationReceiptModel) Applies(docType string) bool { return docType == DonationReceiptDocType }

// Render implements docgen.DocModel: single-pass gofpdf layout, core fonts
// only, frozen metadata so identical input renders byte-identical PDFs.
func (DonationReceiptModel) Render(_ context.Context, subject any, locale string) (io.Reader, string, error) {
	sub, ok := subject.(DonationReceiptSubject)
	if !ok {
		return nil, "", fmt.Errorf("members: donation receipt needs DonationReceiptSubject: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(sub.Ref) == "" || strings.TrimSpace(sub.DonorName) == "" || sub.Amount <= 0 {
		return nil, "", fmt.Errorf("members: incomplete donation receipt: %w", platform.ErrValidation)
	}
	title := "Donation receipt"
	fr := strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "fr")
	if fr {
		title = "Reçu de don"
	}
	frozen := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetCreationDate(frozen)
	pdf.SetModificationDate(frozen)
	pdf.SetCatalogSort(true)
	pdf.SetTitle(sub.Ref, false)
	pdf.SetAuthor("ForgeERP", false)
	pdf.SetMargins(15, 15, 15)
	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 20)
	pdf.Cell(0, 12, title)
	pdf.Ln(14)
	pdf.SetFont("Helvetica", "", 11)
	pdf.CellFormat(0, 7, fmt.Sprintf("%s  %s", sub.Ref, sub.DonatedOn), "", 1, "", false, 0, "")
	pdf.CellFormat(0, 7, fmt.Sprintf("Donor: %s", sub.DonorName), "", 1, "", false, 0, "")
	pdf.CellFormat(0, 7, fmt.Sprintf("Amount: %s", docgen.FormatMoney(sub.Amount, sub.Currency)), "", 1, "", false, 0, "")
	pdf.CellFormat(0, 7, fmt.Sprintf("Method: %s", sub.Method), "", 1, "", false, 0, "")
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, "", err
	}
	return bytes.NewReader(buf.Bytes()), "application/pdf", nil
}

var receiptOnce sync.Once

// ensureReceiptModel registers the template on the process-wide registry
// exactly once (registration only — docgen internals untouched).
func ensureReceiptModel() {
	receiptOnce.Do(func() { docgen.DefaultRegistry().Register(DonationReceiptModel{}) })
}

// RenderDonationReceipt renders a paid donation's receipt through the
// platform docgen registry. Promised/canceled donations are rejected (422):
// a receipt attests money received.
func RenderDonationReceipt(ctx context.Context, d Donation, locale string) (io.Reader, string, error) {
	if d.Status != DonationPaid {
		return nil, "", fmt.Errorf("members: receipt needs a paid donation: %w", platform.ErrValidation)
	}
	ensureReceiptModel()
	m, err := docgen.DefaultRegistry().Lookup(DonationReceiptCode)
	if err != nil {
		return nil, "", err
	}
	return m.Render(ctx, DonationReceiptSubject{
		Ref: d.Ref, EntityID: d.EntityID, DonorName: d.DonorName,
		Amount: d.Amount, DonatedOn: d.DonatedAt.UTC().Format("2006-01-02"), Method: d.Method,
	}, locale)
}
