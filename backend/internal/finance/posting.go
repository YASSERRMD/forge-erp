package finance

import (
	"context"
	"time"
)

// PostInvoice auto-posts a sales/purchase invoice into the ledger (event consumer
// for invoice-paid/validated events): DR customer/supplier control, CR revenue,
// CR VAT. VAT leg omitted when zero. Returns the posted entry.
func PostInvoice(ctx context.Context, s Store, entityID, journalID int64, ref string,
	date time.Time, customerAcct, revenueAcct, vatAcct int64,
	net, vat int64, memo string, createdBy *int64) (*Entry, error) {
	lines := []EntryLine{
		{AccountID: customerAcct, Label: "customer " + ref, Debit: net + vat},
		{AccountID: revenueAcct, Label: "revenue " + ref, Credit: net},
	}
	if vat != 0 {
		lines = append(lines, EntryLine{AccountID: vatAcct, Label: "vat " + ref, Credit: vat})
	}
	e := &Entry{EntityID: entityID, JournalID: journalID, Ref: ref, Date: date,
		Memo: memo, Lines: lines, CreatedBy: createdBy}
	if err := s.PostEntry(ctx, e); err != nil {
		return nil, err
	}
	return e, nil
}
