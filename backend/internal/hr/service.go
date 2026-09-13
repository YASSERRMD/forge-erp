package hr

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Service owns the expense-payout transaction boundary (Phase 0 task 4):
// status flip, total computation and ledger posting commit atomically, so
// the old flip-first/best-effort-revert path is gone. A nil Pool runs the
// flow directly on the given db (memory stores in handler tests).
type Service struct {
	Pool    *pgxpool.Pool
	Store   Store
	Finance Finance
	Bus     platform.Bus
}

// NewService wires a payout service (Pool may be nil in tests).
func NewService(pool *pgxpool.Pool, store Store, fin Finance, bus platform.Bus) *Service {
	return &Service{Pool: pool, Store: store, Finance: fin, Bus: bus}
}

// PayCmd pays one approved expense report through the ledger.
type PayCmd struct {
	EntityID       int64
	ReportID       int64
	JournalID      int64
	ExpenseAccount int64
	BankAccount    int64
	RowVersion     int64
}

// Pay flips the report to paid and posts the balanced entry
// (debit expense, credit bank) in one transaction. Publishes
// forgeerp.hr.expense.paid.v1 after commit.
func (s *Service) Pay(ctx context.Context, cmd PayCmd) (ExpenseReport, error) {
	if s.Pool == nil {
		out, err := s.payOn(ctx, nil, cmd)
		if err != nil {
			return ExpenseReport{}, err
		}
		s.published(ctx, cmd.EntityID, out.ID)
		return out, nil
	}
	var out ExpenseReport
	err := platform.Tx(ctx, s.Pool, func(tx pgx.Tx) error {
		var err error
		out, err = s.payOn(ctx, tx, cmd)
		return err
	})
	if err != nil {
		return ExpenseReport{}, err
	}
	s.published(ctx, cmd.EntityID, out.ID)
	return out, nil
}

func (s *Service) published(ctx context.Context, entityID, id int64) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{
		Subject: "forgeerp.hr.expense.paid.v1", Entity: "expense", EntityID: entityID, ID: id})
}

func (s *Service) payOn(ctx context.Context, db platform.DBTX, cmd PayCmd) (ExpenseReport, error) {
	paid, err := s.Store.SetExpenseStatus(ctx, db, cmd.EntityID, cmd.ReportID, ExpensePaid, cmd.RowVersion)
	if err != nil {
		return ExpenseReport{}, err
	}
	total, err := s.Store.ExpenseTotal(ctx, db, cmd.EntityID, cmd.ReportID)
	if err != nil {
		return ExpenseReport{}, err
	}
	entry := &finance.Entry{EntityID: paid.EntityID, JournalID: cmd.JournalID,
		Ref: "EXP-" + paid.Ref, Date: time.Now().UTC(), Memo: "Expense payout " + paid.Ref,
		Lines: []finance.EntryLine{
			{AccountID: cmd.ExpenseAccount, Label: "Expense " + paid.Ref, Debit: total},
			{AccountID: cmd.BankAccount, Label: "Expense " + paid.Ref, Credit: total},
		}}
	if err := s.Finance.PostEntry(ctx, db, entry); err != nil {
		return ExpenseReport{}, err
	}
	return paid, nil
}
