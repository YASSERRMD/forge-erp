package finance

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Tax declaration periods (VAT returns, etc.): entity, label, start/end, status.
type TaxPeriodStatus string

const (
	TaxPeriodOpen  TaxPeriodStatus = "open"
	TaxPeriodFiled TaxPeriodStatus = "filed"
	TaxPeriodPaid  TaxPeriodStatus = "paid"
)

// TaxPeriod is one declaration window.
type TaxPeriod struct {
	ID       int64           `json:"id"`
	EntityID int64           `json:"entity_id"`
	Label    string          `json:"label"`
	Start    time.Time       `json:"start_date"`
	End      time.Time       `json:"end_date"`
	Status   TaxPeriodStatus `json:"status"`
}

// Validate period rules.
func (p TaxPeriod) Validate() error {
	if strings.TrimSpace(p.Label) == "" {
		return errors.New("finance: tax period label required")
	}
	if p.End.Before(p.Start) {
		return errors.New("finance: tax period ends before it starts")
	}
	switch p.Status {
	case "", TaxPeriodOpen, TaxPeriodFiled, TaxPeriodPaid:
		return nil
	}
	return fmt.Errorf("finance: bad tax period status %q", p.Status)
}

// VATRate is the minimal rate metadata the VAT return joins against: code +
// rate, optionally bound to the collected-VAT balance-sheet account.
type VATRate struct {
	ID           int64  `json:"id"`
	EntityID     int64  `json:"entity_id"`
	Code         string `json:"code"` // e.g. "TVA20"
	Label        string `json:"label"`
	RateBps      int    `json:"rate_bps"` // e.g. 2000 = 20%
	VATAccountID int64  `json:"vat_account_id"`
}

// Validate rate rules.
func (v VATRate) Validate() error {
	if strings.TrimSpace(v.Code) == "" {
		return errors.New("finance: VAT rate code required")
	}
	if v.RateBps < 0 {
		return errors.New("finance: negative VAT rate")
	}
	return nil
}

// TaxCharge tracks one social/fiscal charge with a due date.
type TaxCharge struct {
	ID       int64      `json:"id"`
	EntityID int64      `json:"entity_id"`
	Label    string     `json:"label"`
	Kind     string     `json:"kind"` // social|fiscal
	Amount   int64      `json:"amount"`
	DueDate  time.Time  `json:"due_date"`
	Paid     bool       `json:"paid"`
	PaidAt   *time.Time `json:"paid_at"`
}

// Validate charge rules.
func (c TaxCharge) Validate() error {
	if strings.TrimSpace(c.Label) == "" {
		return errors.New("finance: charge label required")
	}
	switch c.Kind {
	case "social", "fiscal":
	default:
		return fmt.Errorf("finance: bad charge kind %q", c.Kind)
	}
	if c.Amount <= 0 {
		return errors.New("finance: charge amount must be positive")
	}
	if c.DueDate.IsZero() {
		return errors.New("finance: charge due date required")
	}
	return nil
}

// VATBucket aggregates one rate: taxable base plus VAT legs.
type VATBucket struct {
	RateBps    int   `json:"rate_bps"`
	Base       int64 `json:"base"`       // revenue credits / expense debits tagged with the rate
	Collected  int64 `json:"collected"`  // credits on liability accounts
	Deductible int64 `json:"deductible"` // debits on liability accounts
}

// VATReturn is the computed VAT position over [From, To].
type VATReturn struct {
	From            time.Time   `json:"from"`
	To              time.Time   `json:"to"`
	Buckets         []VATBucket `json:"buckets"`
	TotalCollected  int64       `json:"total_collected"`
	TotalDeductible int64       `json:"total_deductible"`
	NetDue          int64       `json:"net_due"` // collected - deductible
}

// ComputeVATReturn aggregates posted entry lines tagged with a VAT rate:
// liability-account credits count as collected, liability debits as
// deductible, and revenue-credit / expense-debit legs form the taxable base.
func ComputeVATReturn(entries []Entry, accounts []Account, from, to time.Time) VATReturn {
	types := map[int64]string{}
	for _, a := range accounts {
		types[a.ID] = a.Type
	}
	buckets := map[int]*VATBucket{}
	for _, e := range entries {
		if e.Status != EntryPosted || e.Date.Before(from) || e.Date.After(to) {
			continue
		}
		for _, l := range e.Lines {
			if l.VATRateBps == 0 {
				continue
			}
			b := buckets[l.VATRateBps]
			if b == nil {
				b = &VATBucket{RateBps: l.VATRateBps}
				buckets[l.VATRateBps] = b
			}
			switch types[l.AccountID] {
			case "liability":
				b.Collected += l.Credit
				b.Deductible += l.Debit
			case "revenue":
				b.Base += l.Credit - l.Debit
			case "expense":
				b.Base += l.Debit - l.Credit
			}
		}
	}
	ret := VATReturn{From: from, To: to}
	for _, b := range buckets {
		ret.Buckets = append(ret.Buckets, *b)
		ret.TotalCollected += b.Collected
		ret.TotalDeductible += b.Deductible
	}
	sort.Slice(ret.Buckets, func(i, j int) bool { return ret.Buckets[i].RateBps < ret.Buckets[j].RateBps })
	ret.NetDue = ret.TotalCollected - ret.TotalDeductible
	return ret
}

// BuildClosingLines generates the year-end closing legs from a trial balance:
// revenue credit-balances close by debit, expense debit-balances by credit,
// and the balancing leg lands on the retained-earnings account (credit on
// profit, debit on loss). Balance-sheet accounts carry forward untouched.
// Returns ErrValidation when there is nothing to close or the retained
// account is unknown.
func BuildClosingLines(trial map[int64][2]int64, accounts []Account, retainedID int64) ([]EntryLine, int64, error) {
	types := map[int64]Account{}
	for _, a := range accounts {
		types[a.ID] = a
	}
	retained, ok := types[retainedID]
	if !ok {
		return nil, 0, fmt.Errorf("finance: retained-earnings account unknown: %w", platform.ErrValidation)
	}
	_ = retained
	var lines []EntryLine
	var income int64 // profit-positive
	for id, sums := range trial {
		a, ok := types[id]
		if !ok {
			continue
		}
		switch a.Type {
		case "revenue":
			if bal := sums[1] - sums[0]; bal != 0 {
				lines = append(lines, EntryLine{AccountID: id, Label: "closing " + a.Code, Debit: bal})
				income += bal
			}
		case "expense":
			if bal := sums[0] - sums[1]; bal != 0 {
				lines = append(lines, EntryLine{AccountID: id, Label: "closing " + a.Code, Credit: bal})
				income -= bal
			}
		}
	}
	if len(lines) == 0 {
		return nil, 0, fmt.Errorf("finance: nothing to close: %w", platform.ErrValidation)
	}
	switch {
	case income > 0:
		lines = append(lines, EntryLine{AccountID: retainedID, Label: "net income", Credit: income})
	case income < 0:
		lines = append(lines, EntryLine{AccountID: retainedID, Label: "net loss", Debit: -income})
	default:
		return nil, 0, fmt.Errorf("finance: zero P&L balance: %w", platform.ErrValidation)
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].AccountID < lines[j].AccountID })
	return lines, income, nil
}
