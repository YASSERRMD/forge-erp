package sepa

import (
	"context"
	"fmt"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Ledger abstracts the finance entry posting used for R-transaction
// reversals (implemented by finance stores; nil disables posting).
type Ledger interface {
	PostEntry(ctx context.Context, db platform.DBTX, e *finance.Entry) error
}

// Service owns the R-transaction boundary: the R row and its ledger
// reversal commit atomically, so a rejected/returned collection can never
// record the handling without unwinding the money (or vice versa). A nil
// Pool runs the flow directly on the given db (memory stores in handler
// tests).
type Service struct {
	Pool   *pgxpool.Pool
	Store  Store
	Ledger Ledger
	Bus    platform.Bus
}

// NewService wires an R-transaction service (Pool may be nil in tests).
func NewService(pool *pgxpool.Pool, store Store, ledger Ledger, bus platform.Bus) *Service {
	return &Service{Pool: pool, Store: store, Ledger: ledger, Bus: bus}
}

// RCmd records one R-handling against a collected batch item.
type RCmd struct {
	EntityID          int64
	BatchID           int64
	EndToEndID        string
	Kind              RKind
	Reason            string
	JournalID         int64
	ReceivableAccount int64
	BankAccount       int64
}

// RecordR stores the R-transaction and posts its reversal entry
// (debit receivable, credit bank) in one transaction. Publishes
// forgeerp.sepa.batch.rtransaction.v1 after commit.
func (s *Service) RecordR(ctx context.Context, cmd RCmd) (RTransaction, error) {
	if s.Pool == nil {
		out, err := s.recordOn(ctx, nil, cmd)
		if err != nil {
			return RTransaction{}, err
		}
		s.published(ctx, cmd.EntityID, out.ID)
		return out, nil
	}
	var out RTransaction
	err := platform.TxEntity(ctx, s.Pool, cmd.EntityID, func(tx pgx.Tx) error {
		var err error
		out, err = s.recordOn(ctx, tx, cmd)
		return err
	})
	if err != nil {
		return RTransaction{}, err
	}
	s.published(ctx, cmd.EntityID, out.ID)
	return out, nil
}

func (s *Service) published(ctx context.Context, entityID, id int64) {
	if s.Bus == nil {
		return
	}
	_ = s.Bus.Publish(ctx, platform.Event{
		Subject: "forgeerp.sepa.batch.rtransaction.v1", Entity: "rtransaction", EntityID: entityID, ID: id})
}

func (s *Service) recordOn(ctx context.Context, db platform.DBTX, cmd RCmd) (RTransaction, error) {
	b, err := s.Store.BatchByID(ctx, db, cmd.EntityID, cmd.BatchID)
	if err != nil {
		return RTransaction{}, err
	}
	// R-handling only makes sense once the bank has the file.
	if b.Status != BatchValidated && b.Status != BatchSent {
		return RTransaction{}, fmt.Errorf("sepa: R-transactions need a validated batch: %w", platform.ErrValidation)
	}
	var amount int64
	found := false
	for _, t := range b.Transactions {
		if t.EndToEndID == cmd.EndToEndID {
			amount = t.Amount
			found = true
			break
		}
	}
	if !found {
		return RTransaction{}, fmt.Errorf("sepa: batch item %q not found: %w", cmd.EndToEndID, platform.ErrNotFound)
	}
	r := &RTransaction{
		EntityID: cmd.EntityID, BatchID: cmd.BatchID,
		EndToEndID: cmd.EndToEndID, Kind: cmd.Kind, Reason: cmd.Reason,
		Amount: amount,
	}
	if err := r.Validate(); err != nil {
		return RTransaction{}, fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if s.Ledger != nil {
		ref := "SEPA-R-" + b.Ref + "-" + cmd.EndToEndID
		entry := &finance.Entry{EntityID: cmd.EntityID, JournalID: cmd.JournalID,
			Ref: ref, Date: time.Now().UTC(),
			Memo: string(cmd.Kind) + " " + cmd.Reason + " " + cmd.EndToEndID,
			Lines: []finance.EntryLine{
				{AccountID: cmd.ReceivableAccount, Label: "SEPA " + string(cmd.Kind) + " " + cmd.EndToEndID, Debit: amount},
				{AccountID: cmd.BankAccount, Label: "SEPA " + string(cmd.Kind) + " " + cmd.EndToEndID, Credit: amount},
			}}
		if err := s.Ledger.PostEntry(ctx, db, entry); err != nil {
			return RTransaction{}, err
		}
		r.LedgerRef = ref
	}
	if err := s.Store.RecordRTransaction(ctx, db, r); err != nil {
		return RTransaction{}, err
	}
	return *r, nil
}
