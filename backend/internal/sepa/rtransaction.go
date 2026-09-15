package sepa

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
)

// RKind is the SEPA R-transaction class (Dolibarr prelevement reject/return).
type RKind string

const (
	RReject RKind = "REJECT" // refused before settlement (e.g. AC01, AM09)
	RReturn RKind = "RETURN" // returned after settlement (e.g. MD01, MS02)
	RRefund RKind = "REFUND" // debtor-authorised refund (e.g. MD01 within 8 weeks)
)

// RTransaction records one R-handling against a pain.008 batch item. The
// batch line itself stays immutable (validated batches are API-immutable);
// the item's post-settlement state lives here, and the money movement is a
// finance reversal entry linked via LedgerRef (posted by Service).
type RTransaction struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	BatchID    int64     `json:"batch_id"`
	EndToEndID string    `json:"end_to_end_id"`
	Kind       RKind     `json:"kind"`
	Reason     string    `json:"reason"` // SEPA reason code, e.g. AC01, MD01, AM09
	Amount     int64     `json:"amount"` // minor units, copied from the batch line
	LedgerRef  string    `json:"ledger_ref"`
	CreatedAt  time.Time `json:"created_at"`
	RowVersion int64     `json:"row_version"`
}

// Validate checks R-transaction field invariants (batch membership and
// settlement state are enforced by Service.RecordR, not here).
func (r RTransaction) Validate() error {
	if r.EntityID <= 0 {
		return fmt.Errorf("sepa: entity_id required: %w", platform.ErrValidation)
	}
	if r.BatchID <= 0 {
		return fmt.Errorf("sepa: batch_id required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(r.EndToEndID) == "" {
		return fmt.Errorf("sepa: end_to_end_id required: %w", platform.ErrValidation)
	}
	switch r.Kind {
	case RReject, RReturn, RRefund:
	default:
		return fmt.Errorf("sepa: bad R kind %q: %w", r.Kind, platform.ErrValidation)
	}
	if strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("sepa: reason required: %w", platform.ErrValidation)
	}
	if r.Amount <= 0 {
		return fmt.Errorf("sepa: amount must be positive: %w", platform.ErrValidation)
	}
	return nil
}

type rtransactionStore interface {
	RecordRTransaction(ctx context.Context, db platform.DBTX, r *RTransaction) error
	ListRTransactions(ctx context.Context, db platform.DBTX, entityID int64, batchID int64) ([]RTransaction, error)
}

const rtransactionCols = `id, entity_id, batch_id, end_to_end_id, kind, reason, amount, ledger_ref, created_at, row_version`

func scanRTransaction(row pgx.Row) (RTransaction, error) {
	var r RTransaction
	err := row.Scan(&r.ID, &r.EntityID, &r.BatchID, &r.EndToEndID, &r.Kind,
		&r.Reason, &r.Amount, &r.LedgerRef, &r.CreatedAt, &r.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return RTransaction{}, identity.ErrNotFound
	}
	return r, err
}

func (s *PGStore) RecordRTransaction(ctx context.Context, db platform.DBTX, r *RTransaction) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_sepa_rtransactions
		(entity_id, batch_id, end_to_end_id, kind, reason, amount, ledger_ref)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, created_at, row_version`,
		r.EntityID, r.BatchID, r.EndToEndID, string(r.Kind), r.Reason,
		r.Amount, r.LedgerRef,
	).Scan(&r.ID, &r.CreatedAt, &r.RowVersion)
}

func (s *PGStore) ListRTransactions(ctx context.Context, db platform.DBTX, entityID int64, batchID int64) ([]RTransaction, error) {
	rows, err := db.Query(ctx, `SELECT `+rtransactionCols+` FROM ferp_sepa_rtransactions
		WHERE entity_id=$1 AND batch_id=$2 ORDER BY id`, entityID, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RTransaction
	for rows.Next() {
		r, err := scanRTransaction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (m *MemoryStore) RecordRTransaction(_ context.Context, _ platform.DBTX, r *RTransaction) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.rtxs {
		if e.EntityID == r.EntityID && e.BatchID == r.BatchID && e.EndToEndID == r.EndToEndID {
			return fmt.Errorf("sepa: duplicate R-transaction: %w", platform.ErrConflict)
		}
	}
	m.rseq++
	r.ID = m.rseq
	r.RowVersion = 1
	r.CreatedAt = time.Now().UTC()
	m.rtxs[r.ID] = *r
	return nil
}

func (m *MemoryStore) ListRTransactions(_ context.Context, _ platform.DBTX, entityID int64, batchID int64) ([]RTransaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RTransaction
	for _, r := range m.rtxs {
		if r.EntityID == entityID && r.BatchID == batchID {
			out = append(out, r)
		}
	}
	return out, nil
}
