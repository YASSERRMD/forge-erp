package finance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service owns Phase 2 finance orchestration: statement imports, paired
// account transfers and year-end closes commit atomically inside
// platform.TxEntity. A nil Pool runs the flow directly on the given db
// (memory stores in handler tests).
type Service struct {
	Pool  *pgxpool.Pool
	Store Store
}

// NewService wires a finance service (Pool may be nil in tests).
func NewService(pool *pgxpool.Pool, store Store) *Service {
	return &Service{Pool: pool, Store: store}
}

func (s *Service) inTx(ctx context.Context, entityID int64, db platform.DBTX, fn func(platform.DBTX) error) error {
	if s.Pool == nil {
		return fn(db)
	}
	return platform.TxEntity(ctx, s.Pool, entityID, func(tx pgx.Tx) error { return fn(tx) })
}

// ImportStatement parses content ("csv" or "camt053") and imports the lines
// idempotently into the bank account. Returns imported/skipped counts.
func (s *Service) ImportStatement(ctx context.Context, db platform.DBTX, entityID, accountID int64, format, content string) (imported, skipped int, err error) {
	var lines []ImportLine
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "csv":
		lines, err = ParseBankCSV(strings.NewReader(content))
	case "camt053", "camt.053", "xml":
		lines, err = ParseCAMT053(strings.NewReader(content))
	default:
		return 0, 0, fmt.Errorf("finance: unknown statement format %q: %w", format, platform.ErrValidation)
	}
	if err != nil {
		return 0, 0, err
	}
	err = s.inTx(ctx, entityID, db, func(tx platform.DBTX) error {
		imported, skipped, err = s.Store.ImportTransactions(ctx, tx, entityID, accountID, lines)
		return err
	})
	return imported, skipped, err
}

// Transfer moves amount between two bank accounts as an atomic pair.
func (s *Service) Transfer(ctx context.Context, db platform.DBTX, c TransferCmd) error {
	return s.inTx(ctx, c.EntityID, db, func(tx platform.DBTX) error {
		return s.Store.Transfer(ctx, tx, c)
	})
}

// CloseCmd closes one fiscal year: posts the P&L carry-forward entry moving
// balances to retained earnings, then locks the year so PostEntry rejects
// further postings into it.
type CloseCmd struct {
	EntityID          int64
	YearID            int64
	JournalID         int64
	RetainedAccountID int64
	Ref               string
	Date              time.Time // defaults to the year end
}

// CloseYear posts the closing entry and locks the fiscal year atomically.
func (s *Service) CloseYear(ctx context.Context, db platform.DBTX, cmd CloseCmd) (*Entry, error) {
	var out *Entry
	err := s.inTx(ctx, cmd.EntityID, db, func(tx platform.DBTX) error {
		year, err := s.Store.FiscalYearByID(ctx, tx, cmd.EntityID, cmd.YearID)
		if err != nil {
			return err
		}
		if year.Locked {
			return fmt.Errorf("finance: fiscal year already closed: %w", platform.ErrConflict)
		}
		date := cmd.Date
		if date.IsZero() {
			date = year.EndDate
		}
		if !year.Contains(date) {
			return fmt.Errorf("finance: closing date outside fiscal year: %w", platform.ErrValidation)
		}
		trial, err := s.Store.TrialBalance(ctx, tx, cmd.EntityID)
		if err != nil {
			return err
		}
		accounts, err := s.Store.Accounts(ctx, tx, cmd.EntityID)
		if err != nil {
			return err
		}
		lines, _, err := BuildClosingLines(trial, accounts, cmd.RetainedAccountID)
		if err != nil {
			return err
		}
		ref := cmd.Ref
		if ref == "" {
			ref = "CLOSE-" + year.Label
		}
		out = &Entry{EntityID: cmd.EntityID, JournalID: cmd.JournalID, Ref: ref,
			Date: date, Memo: "year-end close " + year.Label, Lines: lines}
		if err := s.Store.PostEntry(ctx, tx, out); err != nil {
			return err
		}
		return s.Store.SetFiscalYearLocked(ctx, tx, cmd.EntityID, cmd.YearID, true)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
