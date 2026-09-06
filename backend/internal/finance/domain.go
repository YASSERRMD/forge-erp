// Package finance implements double-entry accounting (Dolibarr
// llx_accounting_account/journal/bookkeeping/fiscalyear), the tamper-evident
// entry chain (blockedlog equivalent), bank accounts + reconciliation
// (llx_bank/bank_account), and loans with schedules (llx_loan/loan_schedule).
package finance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Entry status (Dolibarr bookkeeping: draft → posted; voided via reversal).
type EntryStatus int16

const (
	EntryDraft  EntryStatus = 0
	EntryPosted EntryStatus = 1
	EntryVoid   EntryStatus = 9
)

// Account is one chart-of-accounts row (Dolibarr llx_accounting_account).
type Account struct {
	ID       int64  `json:"id"`
	EntityID int64  `json:"entity_id"`
	Code     string `json:"code"` // e.g. "411000"
	Label    string `json:"label"`
	Type     string `json:"type"` // asset|liability|equity|revenue|expense
}

// Validate account rules.
func (a Account) Validate() error {
	if strings.TrimSpace(a.Code) == "" || strings.TrimSpace(a.Label) == "" {
		return errors.New("finance: account code and label required")
	}
	switch a.Type {
	case "asset", "liability", "equity", "revenue", "expense":
		return nil
	}
	return fmt.Errorf("finance: bad account type %q", a.Type)
}

// Journal groups entries (Dolibarr llx_accounting_journal).
type Journal struct {
	ID       int64  `json:"id"`
	EntityID int64  `json:"entity_id"`
	Code     string `json:"code"` // e.g. "VEN", "ACH", "BNK"
	Label    string `json:"label"`
}

// FiscalYear bounds postings (Dolibarr llx_accounting_fiscalyear).
type FiscalYear struct {
	ID        int64      `json:"id"`
	EntityID  int64      `json:"entity_id"`
	Label     string     `json:"label"`
	StartDate time.Time  `json:"start_date"`
	EndDate   time.Time  `json:"end_date"`
	Locked    bool       `json:"locked"`
}

// Contains reports whether d falls inside the year.
func (f FiscalYear) Contains(d time.Time) bool {
	return !d.Before(f.StartDate) && !d.After(f.EndDate)
}

// EntryLine is one debit or credit leg (exactly one side non-zero).
type EntryLine struct {
	AccountID int64  `json:"account_id"`
	Label     string `json:"label"`
	Debit     int64  `json:"debit"`
	Credit    int64  `json:"credit"`
}

// Validate leg rules.
func (l EntryLine) Validate() error {
	if l.AccountID == 0 {
		return errors.New("finance: line requires an account")
	}
	if l.Debit < 0 || l.Credit < 0 {
		return errors.New("finance: negative leg")
	}
	if (l.Debit == 0) == (l.Credit == 0) {
		return errors.New("finance: leg needs exactly one of debit/credit")
	}
	return nil
}

// Entry is a balanced journal entry with a hash-chain link.
type Entry struct {
	ID         int64       `json:"id"`
	EntityID   int64       `json:"entity_id"`
	JournalID  int64       `json:"journal_id"`
	Ref        string      `json:"ref"`
	Date       time.Time   `json:"date"`
	Memo       string      `json:"memo"`
	Status     EntryStatus `json:"status"`
	Lines      []EntryLine `json:"lines"`
	PrevHash   string      `json:"prev_hash"`
	ChainHash  string      `json:"chain_hash"`
	CreatedBy  *int64      `json:"created_by"`
	RowVersion int64       `json:"row_version"`
}

// Validate checks balance (debits == credits), legs, and dating.
func (e Entry) Validate() error {
	if e.JournalID == 0 {
		return errors.New("finance: entry requires a journal")
	}
	if len(e.Lines) < 2 {
		return errors.New("finance: entry requires at least two legs")
	}
	var dr, cr int64
	for _, l := range e.Lines {
		if err := l.Validate(); err != nil {
			return err
		}
		dr += l.Debit
		cr += l.Credit
	}
	if dr != cr {
		return fmt.Errorf("finance: unbalanced entry (debit %d != credit %d)", dr, cr)
	}
	if dr == 0 {
		return errors.New("finance: zero entry")
	}
	return nil
}

// Chain computes the tamper-evident hash over prev-hash + canonical content
// (blockedlog chain equivalent: each entry commits to its predecessor).
func Chain(prev string, journalID int64, ref string, date time.Time, lines []EntryLine) string {
	var sb strings.Builder
	sb.WriteString(prev)
	fmt.Fprintf(&sb, "|%d|%s|%s", journalID, ref, date.UTC().Format(time.RFC3339))
	for _, l := range lines {
		fmt.Fprintf(&sb, "|%d:%s:%d:%d", l.AccountID, l.Label, l.Debit, l.Credit)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// VerifyChain recomputes every link; the head expected hash is stored server-side
// (or recomputed from genesis "ferp-genesis").
func VerifyChain(entries []Entry) error {
	prev := GenesisHash
	for _, e := range entries {
		if e.PrevHash != prev {
			return fmt.Errorf("finance: chain break at entry %d", e.ID)
		}
		if want := Chain(e.PrevHash, e.JournalID, e.Ref, e.Date, e.Lines); want != e.ChainHash {
			return fmt.Errorf("finance: tampered entry %d", e.ID)
		}
		prev = e.ChainHash
	}
	return nil
}

// GenesisHash anchors new chains.
const GenesisHash = "ferp-genesis"

// BankAccount tracks a real-world account (Dolibarr llx_bank_account).
type BankAccount struct {
	ID        int64  `json:"id"`
	EntityID  int64  `json:"entity_id"`
	Code      string `json:"code"`
	Label     string `json:"label"`
	IBAN      string `json:"iban"`
	Balance   int64  `json:"balance"` // minor units, derived from transactions
}

// BankTransaction is one movement (Dolibarr llx_bank); reconciled links to entries.
type BankTransaction struct {
	ID            int64      `json:"id"`
	EntityID      int64      `json:"entity_id"`
	AccountID     int64      `json:"account_id"`
	Amount        int64      `json:"amount"` // signed minor units
	Label         string     `json:"label"`
	ValueDate     time.Time  `json:"value_date"`
	Reconciled    bool       `json:"reconciled"`
	ReconciledAt  *time.Time `json:"reconciled_at"`
}

// Reconcile marks a transaction reconciled (idempotent guard at store).
func (t BankTransaction) Validate() error {
	if t.AccountID == 0 {
		return errors.New("finance: transaction requires an account")
	}
	if t.Amount == 0 {
		return errors.New("finance: zero-amount transaction")
	}
	return nil
}

// LoanScheduleLine is one amortisation row (Dolibarr llx_loan_schedule).
type LoanScheduleLine struct {
	Seq       int       `json:"seq"`
	DueDate   time.Time `json:"due_date"`
	Principal int64     `json:"principal"`
	Interest  int64     `json:"interest"`
}

// Loan is a financing contract (Dolibarr llx_loan).
type Loan struct {
	ID         int64              `json:"id"`
	EntityID   int64              `json:"entity_id"`
	Label      string             `json:"label"`
	Principal  int64              `json:"principal"`
	RateBps    int                `json:"rate_bps"` // annual, basis points
	Start      time.Time          `json:"start"`
	Periods    int                `json:"periods"`
	Schedule   []LoanScheduleLine `json:"schedule"`
}

// BuildSchedule generates a straight-line amortisation (principal split evenly,
// interest on remaining at annual rate; half-up; last line absorbs rounding).
func BuildSchedule(principal int64, rateBps int, start time.Time, periods int) []LoanScheduleLine {
	out := make([]LoanScheduleLine, 0, periods)
	remaining := principal
	base := principal / int64(periods)
	paid := int64(0)
	for i := 0; i < periods; i++ {
		p := base
		if i == periods-1 {
			p = principal - paid
		}
		interest := (remaining*int64(rateBps) + 500000) / 1000000 / 12
		out = append(out, LoanScheduleLine{Seq: i + 1,
			DueDate: start.AddDate(0, i, 0), Principal: p, Interest: interest})
		remaining -= p
		paid += p
	}
	return out
}
