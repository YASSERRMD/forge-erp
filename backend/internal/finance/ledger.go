package finance

import "time"

// AccountLedgerLine is one ledger-book row touching the drilled account.
type AccountLedgerLine struct {
	EntryID int64     `json:"entry_id"`
	Date    time.Time `json:"date"`
	Ref     string    `json:"ref"`
	Memo    string    `json:"memo"`
	Label   string    `json:"label"`
	Debit   int64     `json:"debit"`
	Credit  int64     `json:"credit"`
}

// AccountDrilldown is the balance walk for one account: opening net,
// affecting lines in date order, closing net. Nets are debit-positive.
type AccountDrilldown struct {
	AccountID int64               `json:"account_id"`
	Opening   int64               `json:"opening"`
	Lines     []AccountLedgerLine `json:"lines"`
	Closing   int64               `json:"closing"`
}

// DrillAccount builds the drill-down from posted entries: lines before from
// form the opening balance, lines within [from, to] are returned in order.
func DrillAccount(entries []Entry, accountID int64, from, to time.Time) AccountDrilldown {
	out := AccountDrilldown{AccountID: accountID}
	for _, e := range entries {
		if e.Status != EntryPosted {
			continue
		}
		for _, l := range e.Lines {
			if l.AccountID != accountID {
				continue
			}
			if e.Date.Before(from) {
				out.Opening += l.Debit - l.Credit
				continue
			}
			if !e.Date.After(to) {
				out.Lines = append(out.Lines, AccountLedgerLine{
					EntryID: e.ID, Date: e.Date, Ref: e.Ref, Memo: e.Memo,
					Label: l.Label, Debit: l.Debit, Credit: l.Credit,
				})
				out.Closing += l.Debit - l.Credit
			}
		}
	}
	out.Closing += out.Opening
	return out
}
