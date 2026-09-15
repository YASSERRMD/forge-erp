package hr

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// PayrollService owns the payroll-run posting transaction boundary (Phase 2):
// the draft→posted flip and the balanced ledger entry commit atomically, so
// a ledger failure rolls the run back to draft instead of stranding it as
// posted. A nil Pool runs the flow directly on the given db (memory stores
// in handler tests). Follows the Service (expense payout) pattern.
type PayrollService struct {
	Pool    *pgxpool.Pool
	Store   Store
	Finance Finance
	Bus     platform.Bus
}

// NewPayrollService wires a run-posting service (Pool may be nil in tests).
func NewPayrollService(pool *pgxpool.Pool, store Store, fin Finance, bus platform.Bus) *PayrollService {
	return &PayrollService{Pool: pool, Store: store, Finance: fin, Bus: bus}
}

// PostRunCmd posts one draft payroll run through the ledger.
type PostRunCmd struct {
	EntityID       int64
	RunID          int64
	JournalID      int64
	ExpenseAccount int64 // debit: salary expense (gross total)
	BankAccount    int64 // credit: bank / net pay (net total)
	PayableAccount int64 // credit: charges payable (charges total, leg omitted when zero)
	RowVersion     int64
}

// Post flips the run to posted and posts the balanced entry atomically:
// debit salary expense for the gross total, credit charges payable for the
// charges total, credit bank for the net total (gross = charges + net, so the
// entry balances; the payable leg is omitted when the run has no charges).
func (s *PayrollService) Post(ctx context.Context, cmd PostRunCmd) (PayrollRun, error) {
	if s.Pool == nil {
		// Memory path (handler tests): no transaction; publish directly.
		out, err := s.postOn(ctx, nil, cmd)
		if err != nil {
			return PayrollRun{}, err
		}
		s.published(ctx, cmd.EntityID, out.ID)
		return out, nil
	}
	var out PayrollRun
	err := platform.TxEntity(ctx, s.Pool, cmd.EntityID, func(tx pgx.Tx) error {
		var err error
		out, err = s.postOn(ctx, tx, cmd)
		return err
	})
	if err != nil {
		return PayrollRun{}, err
	}
	s.published(ctx, cmd.EntityID, out.ID)
	return out, nil
}

func (s *PayrollService) published(ctx context.Context, entityID, id int64) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{
		Subject: "forgeerp.hr.payroll.posted.v1", Entity: "payroll_run", EntityID: entityID, ID: id})
}

func (s *PayrollService) postOn(ctx context.Context, db platform.DBTX, cmd PostRunCmd) (PayrollRun, error) {
	if cmd.JournalID <= 0 || cmd.ExpenseAccount <= 0 || cmd.BankAccount <= 0 {
		return PayrollRun{}, fmt.Errorf("hr: journal, expense and bank accounts required: %w", platform.ErrValidation)
	}
	lines, err := s.Store.PayrollRunLines(ctx, db, cmd.EntityID, cmd.RunID)
	if err != nil {
		return PayrollRun{}, err
	}
	if len(lines) == 0 {
		return PayrollRun{}, fmt.Errorf("hr: cannot post an empty run: %w", platform.ErrValidation)
	}
	gross, charges, net := PayrollTotals(lines)
	if net != gross-charges {
		return PayrollRun{}, fmt.Errorf("hr: run totals unbalanced: %w", platform.ErrValidation)
	}
	if gross <= 0 {
		return PayrollRun{}, fmt.Errorf("hr: run total must be positive: %w", platform.ErrValidation)
	}
	if charges > 0 && cmd.PayableAccount <= 0 {
		return PayrollRun{}, fmt.Errorf("hr: payable account required when charges present: %w", platform.ErrValidation)
	}
	posted, err := s.Store.SetPayrollRunStatus(ctx, db, cmd.EntityID, cmd.RunID, PayrollRunPosted, cmd.RowVersion)
	if err != nil {
		return PayrollRun{}, err
	}
	ref := fmt.Sprintf("PAY-%d", posted.ID)
	entryLines := []finance.EntryLine{
		{AccountID: cmd.ExpenseAccount, Label: "Payroll " + posted.Label, Debit: gross},
	}
	if charges > 0 {
		entryLines = append(entryLines,
			finance.EntryLine{AccountID: cmd.PayableAccount, Label: "Payroll charges " + posted.Label, Credit: charges})
	}
	entryLines = append(entryLines,
		finance.EntryLine{AccountID: cmd.BankAccount, Label: "Payroll " + posted.Label, Credit: net})
	entry := &finance.Entry{EntityID: posted.EntityID, JournalID: cmd.JournalID,
		Ref: ref, Date: time.Now().UTC(), Memo: "Payroll run " + posted.Label,
		Lines: entryLines}
	if err := s.Finance.PostEntry(ctx, db, entry); err != nil {
		return PayrollRun{}, err
	}
	return posted, nil
}
