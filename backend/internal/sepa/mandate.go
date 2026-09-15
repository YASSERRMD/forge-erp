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

// Mandate status (Dolibarr prelevement rum lifecycle, simplified).
type MandateStatus int16

const (
	MandateDraft    MandateStatus = 0
	MandateActive   MandateStatus = 1 // signed; amendments stay active
	MandateCanceled MandateStatus = -1
)

// Mandate is one SEPA direct-debit mandate (Dolibarr prelevement rum):
// the debtor's signed authorisation to collect. UMR is unique per entity.
// Amounts elsewhere stay int64 minor units; the mandate itself carries none.
type Mandate struct {
	ID         int64         `json:"id"`
	EntityID   int64         `json:"entity_id"`
	UMR        string        `json:"umr"`
	DebtorName string        `json:"debtor_name"`
	IBAN       string        `json:"iban"`
	BIC        string        `json:"bic"`
	Sequence   string        `json:"sequence"` // FRST|RCUR|FNAL|OOFF|OFF
	Status     MandateStatus `json:"status"`
	SignedAt   *time.Time    `json:"signed_at,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
	AmendedAt  *time.Time    `json:"amended_at,omitempty"`
	RowVersion int64         `json:"row_version"`
}

// checkMandateSequence accepts the SEPA sequence types; OFF is the Phase 2
// brief's spelling of the one-off OOFF type (both accepted, stored as given).
func checkMandateSequence(seq string) error {
	switch seq {
	case "FRST", "RCUR", "FNAL", "OOFF", "OFF":
		return nil
	default:
		return fmt.Errorf("sepa: bad mandate sequence %q: %w", seq, platform.ErrValidation)
	}
}

// Validate checks mandate field invariants (lifecycle enforced by the
// sign/amend/cancel transition methods, not here).
func (m Mandate) Validate() error {
	if m.EntityID <= 0 {
		return fmt.Errorf("sepa: entity_id required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(m.UMR) == "" {
		return fmt.Errorf("sepa: umr required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(m.DebtorName) == "" {
		return fmt.Errorf("sepa: debtor name required: %w", platform.ErrValidation)
	}
	if err := CheckIBAN(m.IBAN); err != nil {
		return err
	}
	if m.BIC != "" {
		if err := CheckBIC(m.BIC); err != nil {
			return err
		}
	}
	return checkMandateSequence(m.Sequence)
}

// CanSign reports whether the mandate may be signed (draft only).
func (m Mandate) CanSign() bool { return m.Status == MandateDraft }

// CanAmend reports whether the mandate may be amended (signed only;
// amendments keep the mandate active and stamp amended_at).
func (m Mandate) CanAmend() bool { return m.Status == MandateActive }

// CanCancel reports whether the mandate may be canceled.
func (m Mandate) CanCancel() bool { return m.Status == MandateDraft || m.Status == MandateActive }

// mandateStore is the mandate slice of persistence (implemented by PGStore
// and MemoryStore; part of the Store contract below via embedding).
type mandateStore interface {
	CreateMandate(ctx context.Context, db platform.DBTX, m *Mandate) error
	MandateByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Mandate, error)
	ListMandates(ctx context.Context, db platform.DBTX, entityID int64) ([]Mandate, error)
	SignMandate(ctx context.Context, db platform.DBTX, entityID int64, id int64, signedAt time.Time, rowVersion int64) (Mandate, error)
	AmendMandate(ctx context.Context, db platform.DBTX, entityID int64, id int64, debtorName, iban, bic string, rowVersion int64) (Mandate, error)
	CancelMandate(ctx context.Context, db platform.DBTX, entityID int64, id int64, rowVersion int64) (Mandate, error)
}

const mandateCols = `id, entity_id, umr, debtor_name, debtor_iban, debtor_bic, sequence, status, signed_at, created_at, amended_at, row_version`

func scanMandate(row pgx.Row) (Mandate, error) {
	var m Mandate
	err := row.Scan(&m.ID, &m.EntityID, &m.UMR, &m.DebtorName, &m.IBAN,
		&m.BIC, &m.Sequence, &m.Status, &m.SignedAt, &m.CreatedAt,
		&m.AmendedAt, &m.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Mandate{}, identity.ErrNotFound
	}
	return m, err
}

func (s *PGStore) CreateMandate(ctx context.Context, db platform.DBTX, m *Mandate) error {
	if err := m.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_sepa_mandates
		(entity_id, umr, debtor_name, debtor_iban, debtor_bic, sequence, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, created_at, row_version`,
		m.EntityID, m.UMR, m.DebtorName, m.IBAN, m.BIC, m.Sequence, m.Status,
	).Scan(&m.ID, &m.CreatedAt, &m.RowVersion)
}

func (s *PGStore) MandateByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Mandate, error) {
	return scanMandate(db.QueryRow(ctx, `SELECT `+mandateCols+` FROM ferp_sepa_mandates WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) ListMandates(ctx context.Context, db platform.DBTX, entityID int64) ([]Mandate, error) {
	rows, err := db.Query(ctx, `SELECT `+mandateCols+` FROM ferp_sepa_mandates WHERE entity_id=$1 ORDER BY id`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Mandate
	for rows.Next() {
		m, err := scanMandate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *PGStore) SignMandate(ctx context.Context, db platform.DBTX, entityID int64, id int64, signedAt time.Time, rowVersion int64) (Mandate, error) {
	m, err := s.MandateByID(ctx, db, entityID, id)
	if err != nil {
		return Mandate{}, err
	}
	if m.RowVersion != rowVersion {
		return Mandate{}, identity.ErrVersionConflict
	}
	if signedAt.IsZero() {
		return Mandate{}, fmt.Errorf("sepa: signed_at required: %w", platform.ErrValidation)
	}
	if !m.CanSign() {
		return Mandate{}, fmt.Errorf("sepa: mandate not signable: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_sepa_mandates
		SET status=$1, signed_at=$2, row_version=row_version+1
		WHERE id=$3 AND row_version=$4 AND entity_id=$5`,
		MandateActive, signedAt.UTC(), id, rowVersion, entityID)
	if err != nil {
		return Mandate{}, err
	}
	if tag.RowsAffected() == 0 {
		return Mandate{}, identity.ErrVersionConflict
	}
	m.Status = MandateActive
	m.SignedAt = &signedAt
	m.RowVersion++
	return m, nil
}

func (s *PGStore) AmendMandate(ctx context.Context, db platform.DBTX, entityID int64, id int64, debtorName, iban, bic string, rowVersion int64) (Mandate, error) {
	m, err := s.MandateByID(ctx, db, entityID, id)
	if err != nil {
		return Mandate{}, err
	}
	if m.RowVersion != rowVersion {
		return Mandate{}, identity.ErrVersionConflict
	}
	if !m.CanAmend() {
		return Mandate{}, fmt.Errorf("sepa: mandate not amendable: %w", platform.ErrValidation)
	}
	probe := m
	probe.DebtorName, probe.IBAN, probe.BIC = debtorName, iban, bic
	if err := probe.Validate(); err != nil {
		return Mandate{}, fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	var amended time.Time
	err = db.QueryRow(ctx, `UPDATE ferp_sepa_mandates
		SET debtor_name=$1, debtor_iban=$2, debtor_bic=$3, amended_at=now(), row_version=row_version+1
		WHERE id=$4 AND row_version=$5 AND entity_id=$6
		RETURNING amended_at, row_version`,
		debtorName, iban, bic, id, rowVersion, entityID).Scan(&amended, &m.RowVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Mandate{}, identity.ErrVersionConflict
		}
		return Mandate{}, err
	}
	m.DebtorName, m.IBAN, m.BIC = debtorName, iban, bic
	m.AmendedAt = &amended
	return m, nil
}

func (s *PGStore) CancelMandate(ctx context.Context, db platform.DBTX, entityID int64, id int64, rowVersion int64) (Mandate, error) {
	m, err := s.MandateByID(ctx, db, entityID, id)
	if err != nil {
		return Mandate{}, err
	}
	if m.RowVersion != rowVersion {
		return Mandate{}, identity.ErrVersionConflict
	}
	if !m.CanCancel() {
		return Mandate{}, fmt.Errorf("sepa: mandate not cancelable: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_sepa_mandates SET status=$1, row_version=row_version+1
		WHERE id=$2 AND row_version=$3 AND entity_id=$4`, MandateCanceled, id, rowVersion, entityID)
	if err != nil {
		return Mandate{}, err
	}
	if tag.RowsAffected() == 0 {
		return Mandate{}, identity.ErrVersionConflict
	}
	m.Status = MandateCanceled
	m.RowVersion++
	return m, nil
}

// MemoryStore mandate methods (mutex shared with the batch maps).

func (m *MemoryStore) CreateMandate(_ context.Context, _ platform.DBTX, md *Mandate) error {
	if err := md.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.mandates {
		if e.EntityID == md.EntityID && e.UMR == md.UMR {
			return fmt.Errorf("sepa: duplicate umr: %w", platform.ErrConflict)
		}
	}
	m.mseq++
	md.ID = m.mseq
	md.RowVersion = 1
	md.CreatedAt = time.Now().UTC()
	m.mandates[md.ID] = *md
	return nil
}

func (m *MemoryStore) MandateByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (Mandate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	md, ok := m.mandates[id]
	if !ok || md.EntityID != entityID {
		return Mandate{}, identity.ErrNotFound
	}
	return md, nil
}

func (m *MemoryStore) ListMandates(_ context.Context, _ platform.DBTX, entityID int64) ([]Mandate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Mandate
	for _, md := range m.mandates {
		if md.EntityID == entityID {
			out = append(out, md)
		}
	}
	return out, nil
}

func (m *MemoryStore) SignMandate(_ context.Context, _ platform.DBTX, entityID int64, id int64, signedAt time.Time, rowVersion int64) (Mandate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	md, ok := m.mandates[id]
	if !ok || md.EntityID != entityID {
		return Mandate{}, identity.ErrNotFound
	}
	if md.RowVersion != rowVersion {
		return Mandate{}, identity.ErrVersionConflict
	}
	if signedAt.IsZero() {
		return Mandate{}, fmt.Errorf("sepa: signed_at required: %w", platform.ErrValidation)
	}
	if !md.CanSign() {
		return Mandate{}, fmt.Errorf("sepa: mandate not signable: %w", platform.ErrValidation)
	}
	md.Status = MandateActive
	md.SignedAt = &signedAt
	md.RowVersion++
	m.mandates[id] = md
	return md, nil
}

func (m *MemoryStore) AmendMandate(_ context.Context, _ platform.DBTX, entityID int64, id int64, debtorName, iban, bic string, rowVersion int64) (Mandate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	md, ok := m.mandates[id]
	if !ok || md.EntityID != entityID {
		return Mandate{}, identity.ErrNotFound
	}
	if md.RowVersion != rowVersion {
		return Mandate{}, identity.ErrVersionConflict
	}
	if !md.CanAmend() {
		return Mandate{}, fmt.Errorf("sepa: mandate not amendable: %w", platform.ErrValidation)
	}
	probe := md
	probe.DebtorName, probe.IBAN, probe.BIC = debtorName, iban, bic
	if err := probe.Validate(); err != nil {
		return Mandate{}, fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	now := time.Now().UTC()
	md.DebtorName, md.IBAN, md.BIC = debtorName, iban, bic
	md.AmendedAt = &now
	md.RowVersion++
	m.mandates[id] = md
	return md, nil
}

func (m *MemoryStore) CancelMandate(_ context.Context, _ platform.DBTX, entityID int64, id int64, rowVersion int64) (Mandate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	md, ok := m.mandates[id]
	if !ok || md.EntityID != entityID {
		return Mandate{}, identity.ErrNotFound
	}
	if md.RowVersion != rowVersion {
		return Mandate{}, identity.ErrVersionConflict
	}
	if !md.CanCancel() {
		return Mandate{}, fmt.Errorf("sepa: mandate not cancelable: %w", platform.ErrValidation)
	}
	md.Status = MandateCanceled
	md.RowVersion++
	m.mandates[id] = md
	return md, nil
}
