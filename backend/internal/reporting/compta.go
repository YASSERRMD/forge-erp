package reporting

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// LedgerDetail is the optional entry-detail capability over the Ledger seam.
// finance.PGStore and finance.MemoryStore both satisfy it; drill-down,
// general-ledger export and period P&L walk posted entries through it while
// the plain TrialBalance path stays available when detail is absent. No new
// tables or migrations: all math over existing postings.
type LedgerDetail interface {
	Ledger
	ListJournals(ctx context.Context, db platform.DBTX, entityID int64) ([]finance.Journal, error)
	EntriesByJournal(ctx context.Context, db platform.DBTX, journalID int64) ([]finance.Entry, error)
}

func detailOf(ledger Ledger) (LedgerDetail, error) {
	d, ok := ledger.(LedgerDetail)
	if !ok {
		return nil, fmt.Errorf("reporting: ledger entry detail unavailable: %w", platform.ErrValidation)
	}
	return d, nil
}

// signedBalance applies the P&L sign convention: revenue/equity/liability as
// credit-minus-debit, asset/expense as debit-minus-credit.
func signedBalance(acctType string, debit, credit int64) int64 {
	switch acctType {
	case "revenue", "equity", "liability":
		return credit - debit
	default: // asset, expense
		return debit - credit
	}
}

// postedEntriesOf collects posted entries of one entity across its journals
// (journals are entity-scoped, so entry scope follows), ordered by date then id.
func postedEntriesOf(ctx context.Context, db platform.DBTX, entityID int64, ledger LedgerDetail) ([]finance.Entry, []finance.Journal, error) {
	journals, err := ledger.ListJournals(ctx, db, entityID)
	if err != nil {
		return nil, nil, err
	}
	var out []finance.Entry
	for _, j := range journals {
		if j.EntityID != entityID {
			continue
		}
		ents, err := ledger.EntriesByJournal(ctx, db, j.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, e := range ents {
			if e.EntityID != entityID || e.Status != finance.EntryPosted {
				continue
			}
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date.Equal(out[j].Date) {
			return out[i].ID < out[j].ID
		}
		return out[i].Date.Before(out[j].Date)
	})
	return out, journals, nil
}

// DrillLine is one posted leg touching the drilled account.
type DrillLine struct {
	EntryID     int64     `json:"entry_id"`
	JournalID   int64     `json:"journal_id"`
	JournalCode string    `json:"journal_code"`
	Ref         string    `json:"ref"`
	Date        time.Time `json:"date"`
	LineLabel   string    `json:"line_label"`
	Debit       int64     `json:"debit"`
	Credit      int64     `json:"credit"`
}

// AccountDrill is the balance drill-down: one chart account with its debit /
// credit sums, signed balance, and every posted leg behind it.
type AccountDrill struct {
	Account finance.Account `json:"account"`
	Debit   int64           `json:"debit"`
	Credit  int64           `json:"credit"`
	Balance int64           `json:"balance"`
	Lines   []DrillLine     `json:"lines"`
}

// AccountDrillDown resolves account → posted entries (unknown account is
// ErrNotFound; zero id or a ledger without entry detail is ErrValidation).
func AccountDrillDown(ctx context.Context, db platform.DBTX, entityID, accountID int64, ledger Ledger) (AccountDrill, error) {
	if accountID <= 0 {
		return AccountDrill{}, fmt.Errorf("reporting: account_id required: %w", platform.ErrValidation)
	}
	accts, err := ledger.Accounts(ctx, db, entityID)
	if err != nil {
		return AccountDrill{}, err
	}
	var acct *finance.Account
	for i := range accts {
		if accts[i].ID == accountID && accts[i].EntityID == entityID {
			acct = &accts[i]
			break
		}
	}
	if acct == nil {
		return AccountDrill{}, fmt.Errorf("reporting: account %d: %w", accountID, platform.ErrNotFound)
	}
	d, err := detailOf(ledger)
	if err != nil {
		return AccountDrill{}, err
	}
	entries, journals, err := postedEntriesOf(ctx, db, entityID, d)
	if err != nil {
		return AccountDrill{}, err
	}
	codeOf := map[int64]string{}
	for _, j := range journals {
		codeOf[j.ID] = j.Code
	}
	var drill AccountDrill
	drill.Account = *acct
	for _, e := range entries {
		for _, l := range e.Lines {
			if l.AccountID != accountID {
				continue
			}
			drill.Debit += l.Debit
			drill.Credit += l.Credit
			drill.Lines = append(drill.Lines, DrillLine{
				EntryID: e.ID, JournalID: e.JournalID,
				JournalCode: codeOf[e.JournalID], Ref: e.Ref, Date: e.Date,
				LineLabel: l.Label, Debit: l.Debit, Credit: l.Credit,
			})
		}
	}
	drill.Balance = signedBalance(acct.Type, drill.Debit, drill.Credit)
	if drill.Lines == nil {
		drill.Lines = []DrillLine{}
	}
	return drill, nil
}

// GLRow is one general-ledger line: a posted entry leg with its account and
// journal codes denormalized. Money is int64 minor units.
type GLRow struct {
	Date         time.Time `json:"date"`
	JournalCode  string    `json:"journal_code"`
	EntryRef     string    `json:"entry_ref"`
	AccountCode  string    `json:"account_code"`
	AccountLabel string    `json:"account_label"`
	LineLabel    string    `json:"line_label"`
	Debit        int64     `json:"debit"`
	Credit       int64     `json:"credit"`
	EntryID      int64     `json:"entry_id"`
	AccountID    int64     `json:"account_id"`
	JournalID    int64     `json:"journal_id"`
}

// GeneralLedger expands every posted entry of the entity into ledger rows
// ordered by date, entry id, then account code.
func GeneralLedger(ctx context.Context, db platform.DBTX, entityID int64, ledger Ledger) ([]GLRow, error) {
	d, err := detailOf(ledger)
	if err != nil {
		return nil, err
	}
	entries, journals, err := postedEntriesOf(ctx, db, entityID, d)
	if err != nil {
		return nil, err
	}
	accts, err := ledger.Accounts(ctx, db, entityID)
	if err != nil {
		return nil, err
	}
	byAcct := map[int64]finance.Account{}
	for _, a := range accts {
		byAcct[a.ID] = a
	}
	codeOf := map[int64]string{}
	for _, j := range journals {
		codeOf[j.ID] = j.Code
	}
	var rows []GLRow
	for _, e := range entries {
		for _, l := range e.Lines {
			a := byAcct[l.AccountID]
			rows = append(rows, GLRow{
				Date: e.Date, JournalCode: codeOf[e.JournalID],
				EntryRef: e.Ref, AccountCode: a.Code, AccountLabel: a.Label,
				LineLabel: l.Label, Debit: l.Debit, Credit: l.Credit,
				EntryID: e.ID, AccountID: l.AccountID, JournalID: e.JournalID,
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Date.Equal(rows[j].Date) {
			if rows[i].EntryID == rows[j].EntryID {
				return rows[i].AccountCode < rows[j].AccountCode
			}
			return rows[i].EntryID < rows[j].EntryID
		}
		return rows[i].Date.Before(rows[j].Date)
	})
	return rows, nil
}

// WriteGeneralLedgerCSV streams rows as CSV (date YYYY-MM-DD; money stays
// int64 minor units in debit_cents/credit_cents — never float).
func WriteGeneralLedgerCSV(w io.Writer, rows []GLRow) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"date", "journal", "entry_ref", "account", "account_label", "line_label", "debit_cents", "credit_cents"}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write([]string{
			r.Date.UTC().Format("2006-01-02"), r.JournalCode, r.EntryRef,
			r.AccountCode, r.AccountLabel, r.LineLabel,
			fmt.Sprintf("%d", r.Debit), fmt.Sprintf("%d", r.Credit),
		}); err != nil {
			return err
		}
	}
	return cw.Error()
}

// CloseResultAccount is the proposed carry-forward code for the closing
// proposal (French PCG class-12 style). The operator creates (or maps) it in
// finance; reporting never posts.
const CloseResultAccount = "120000"

// CloseLeg is one proposed closing leg (int64 minor units).
type CloseLeg struct {
	AccountID   int64  `json:"account_id"`
	AccountCode string `json:"account_code"`
	Label       string `json:"label"`
	Debit       int64  `json:"debit"`
	Credit      int64  `json:"credit"`
}

// ClosePreview is a computed year-end closing proposal: revenue and expense
// balances zeroed against the result account. Proposal only — posting lives
// in finance (POST /finance/entries); this endpoint never writes.
type ClosePreview struct {
	Revenue       int64      `json:"revenue"`
	Expense       int64      `json:"expense"`
	Net           int64      `json:"net"`
	ResultAccount string     `json:"result_account"`
	Legs          []CloseLeg `json:"legs"`
	Balanced      bool       `json:"balanced"`
	Note          string     `json:"note"`
}

// PreviewClose builds the closing proposal from the trial balance (same sign
// convention as the P&L): each non-zero revenue balance is debited (zeroing
// the credit) against the result account, each non-zero expense balance is
// credited against it. The net result lands as a credit (profit) or debit
// (loss) on the result account.
func PreviewClose(ctx context.Context, db platform.DBTX, entityID int64, ledger Ledger) (ClosePreview, error) {
	accts, err := ledger.Accounts(ctx, db, entityID)
	if err != nil {
		return ClosePreview{}, err
	}
	tb, err := ledger.TrialBalance(ctx, db, entityID)
	if err != nil {
		return ClosePreview{}, err
	}
	var out ClosePreview
	out.ResultAccount = CloseResultAccount
	out.Note = "computed proposal only; posting lives in finance (POST /finance/entries)"
	for _, a := range accts {
		sums := tb[a.ID]
		switch a.Type {
		case "revenue":
			bal := sums[1] - sums[0]
			out.Revenue += bal
			if bal != 0 {
				out.Legs = append(out.Legs,
					CloseLeg{AccountID: a.ID, AccountCode: a.Code, Label: "close " + a.Code, Debit: bal},
					CloseLeg{AccountCode: CloseResultAccount, Label: "result <- " + a.Code, Credit: bal})
			}
		case "expense":
			bal := sums[0] - sums[1]
			out.Expense += bal
			if bal != 0 {
				out.Legs = append(out.Legs,
					CloseLeg{AccountID: a.ID, AccountCode: a.Code, Label: "close " + a.Code, Credit: bal},
					CloseLeg{AccountCode: CloseResultAccount, Label: "result <- " + a.Code, Debit: bal})
			}
		}
	}
	out.Net = out.Revenue - out.Expense
	var dr, cr int64
	for _, l := range out.Legs {
		dr += l.Debit
		cr += l.Credit
	}
	out.Balanced = dr == cr
	if out.Legs == nil {
		out.Legs = []CloseLeg{}
	}
	return out, nil
}

// BuildPNLPeriod assembles P&L over posted entries within [from, to]
// (inclusive). The TrialBalance seam carries no dates, so period slicing
// walks entry detail (ErrValidation when the ledger lacks it); with both
// bounds zero it falls back to the all-postings BuildPNL.
func BuildPNLPeriod(ctx context.Context, db platform.DBTX, entityID int64, ledger Ledger, from, to time.Time) (ProfitAndLoss, error) {
	if from.IsZero() && to.IsZero() {
		return BuildPNL(ctx, db, entityID, ledger)
	}
	if from.IsZero() || to.IsZero() || from.After(to) {
		return ProfitAndLoss{}, fmt.Errorf("reporting: bad period bounds: %w", platform.ErrValidation)
	}
	d, err := detailOf(ledger)
	if err != nil {
		return ProfitAndLoss{}, err
	}
	accts, err := ledger.Accounts(ctx, db, entityID)
	if err != nil {
		return ProfitAndLoss{}, err
	}
	entries, _, err := postedEntriesOf(ctx, db, entityID, d)
	if err != nil {
		return ProfitAndLoss{}, err
	}
	sums := map[int64][2]int64{}
	for _, e := range entries {
		if e.Date.Before(from) || e.Date.After(to) {
			continue
		}
		for _, l := range e.Lines {
			acc := sums[l.AccountID]
			acc[0] += l.Debit
			acc[1] += l.Credit
			sums[l.AccountID] = acc
		}
	}
	var out ProfitAndLoss
	for _, a := range accts {
		s := sums[a.ID]
		bal := signedBalance(a.Type, s[0], s[1])
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
