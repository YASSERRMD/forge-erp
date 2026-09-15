// Member loans with French amortisation (Phase 2 loan/don depth).
//
// Rounding policy (documented): monthly interest is computed per period on
// the remaining principal at the annual rate, rounded half-up to minor
// units: interest = (remaining*rateBps + 60000) / 120000. The constant
// annuity is the smallest integer payment that amortizes the loan in the
// agreed number of periods (found by binary search over pure int64
// simulation — no floats anywhere); the last installment absorbs the
// residual, so it may differ from the annuity by a few minor units.
// Total paid = principal + total interest exactly; principal legs sum to
// the principal exactly. Zero rate degenerates to an even split with the
// remainder on the last line.
//
// Ledger legs are posted through finance (LedgerPoster, satisfied by
// finance stores) — this package never touches finance internals.
package members

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/finance"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// MemberLoanStatus tracks disbursement life (Dolibarr llx_loan steps,
// member-scoped here).
type MemberLoanStatus int16

const (
	MemberLoanDraft     MemberLoanStatus = 0
	MemberLoanDisbursed MemberLoanStatus = 1
	MemberLoanRepaid    MemberLoanStatus = 2
	MemberLoanCanceled  MemberLoanStatus = -1
)

// MemberLoan is one borrowing contract with its generated schedule.
type MemberLoan struct {
	ID         int64            `json:"id"`
	EntityID   int64            `json:"entity_id"`
	Label      string           `json:"label"`
	Principal  int64            `json:"principal"` // minor units, > 0
	RateBps    int              `json:"rate_bps"`  // annual, basis points (>= 0)
	Start      time.Time        `json:"start"`
	Periods    int              `json:"periods"`
	Status     MemberLoanStatus `json:"status"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
	RowVersion int64            `json:"row_version"`
}

// Validate checks loan terms. Constraint: principal*rateBps must fit int64
// (principal ≤ 1e12 minor units, rate ≤ 100000 bps are enforced).
func (l MemberLoan) Validate() error {
	if l.EntityID <= 0 {
		return errors.New("members: loan requires an entity")
	}
	if strings.TrimSpace(l.Label) == "" {
		return errors.New("members: loan label required")
	}
	if l.Principal <= 0 || l.Principal > 1_000_000_000_000 {
		return fmt.Errorf("members: loan principal %d out of range", l.Principal)
	}
	if l.RateBps < 0 || l.RateBps > 100_000 {
		return fmt.Errorf("members: loan rate %d bps out of range", l.RateBps)
	}
	if l.Periods <= 0 || l.Periods > 360 {
		return fmt.Errorf("members: loan periods %d out of range 1-360", l.Periods)
	}
	if l.Start.IsZero() {
		return errors.New("members: loan start required")
	}
	return nil
}

// CanTransition reports whether a loan status change is legal.
func (l MemberLoan) CanTransition(to MemberLoanStatus) bool {
	switch l.Status {
	case MemberLoanDraft:
		return to == MemberLoanDisbursed || to == MemberLoanCanceled
	case MemberLoanDisbursed:
		return to == MemberLoanRepaid
	default:
		return false
	}
}

// MemberLoanLine is one amortisation installment (persisted schedule row).
type MemberLoanLine struct {
	ID        int64      `json:"id"`
	LoanID    int64      `json:"loan_id"`
	Seq       int        `json:"seq"` // 1-based
	DueDate   time.Time  `json:"due_date"`
	Payment   int64      `json:"payment"`
	Principal int64      `json:"principal"`
	Interest  int64      `json:"interest"`
	Remaining int64      `json:"remaining"` // principal left after this line
	Paid      bool       `json:"paid"`
	PaidAt    *time.Time `json:"paid_at"`
}

// monthlyInterest is one month of interest on remaining, half-up.
// interest = remaining * rateBps / 120000 (annual bps → monthly fraction).
func monthlyInterest(remaining int64, rateBps int) int64 {
	return (remaining*int64(rateBps) + 60000) / 120000
}

// amortizes reports whether constant payment pay clears principal within
// periods (pure int64 simulation; overpaying early counts as success).
func amortizes(principal int64, rateBps, periods int, pay int64) bool {
	remaining := principal
	for i := 0; i < periods; i++ {
		interest := monthlyInterest(remaining, rateBps)
		if pay-interest <= 0 {
			return false // payment does not even cover interest: never amortizes
		}
		if pay-interest >= remaining {
			return true // cleared on or before this period
		}
		remaining -= pay - interest
	}
	return remaining <= 0
}

// BuildAmortisation generates the French-amortisation schedule (constant
// annuity, last line absorbs rounding). See the package rounding policy.
func BuildAmortisation(principal int64, rateBps int, start time.Time, periods int) ([]MemberLoanLine, error) {
	l := MemberLoan{EntityID: 1, Label: "x", Principal: principal, RateBps: rateBps, Start: start, Periods: periods}
	if err := l.Validate(); err != nil {
		// EntityID/label are scaffolding here; surface only term errors.
		return nil, err
	}
	// Smallest integer payment that amortizes in `periods` installments.
	high := principal + int64(periods)*monthlyInterest(principal, rateBps) + 1
	low := int64(0) // fails (covers nothing)
	for high-low > 1 {
		mid := (low + high) / 2
		if amortizes(principal, rateBps, periods, mid) {
			high = mid
		} else {
			low = mid
		}
	}
	pay := high
	out := make([]MemberLoanLine, 0, periods)
	remaining := principal
	for i := 0; i < periods; i++ {
		interest := monthlyInterest(remaining, rateBps)
		princ := pay - interest
		if i == periods-1 || princ >= remaining {
			princ = remaining // last (or early-clearing) line absorbs the residual
		}
		remaining -= princ
		out = append(out, MemberLoanLine{
			Seq: i + 1, DueDate: start.AddDate(0, i, 0),
			Payment: princ + interest, Principal: princ,
			Interest: interest, Remaining: remaining,
		})
		if remaining == 0 && i < periods-1 {
			break // overpaying annuity clears early (only when pay > minimum)
		}
	}
	return out, nil
}

// LedgerPoster posts balanced entries; finance stores satisfy it. Members
// depends on this narrow seam only — never on finance internals.
type LedgerPoster interface {
	PostEntry(ctx context.Context, db platform.DBTX, e *finance.Entry) error
}

// PostLoanDisbursement posts the disbursement: DR loan receivable,
// CR bank, for the principal. Returns the posted entry.
func PostLoanDisbursement(ctx context.Context, db platform.DBTX, ledger LedgerPoster,
	entityID, journalID int64, loan MemberLoan, receivableAcct, bankAcct int64,
	date time.Time, createdBy *int64) (*finance.Entry, error) {
	if loan.Principal <= 0 {
		return nil, fmt.Errorf("members: bad loan principal: %w", platform.ErrValidation)
	}
	if receivableAcct == 0 || bankAcct == 0 || journalID == 0 {
		return nil, fmt.Errorf("members: disbursement needs journal, receivable and bank accounts: %w", platform.ErrValidation)
	}
	ref := fmt.Sprintf("LOAN-%d-DISB", loan.ID)
	e := &finance.Entry{EntityID: entityID, JournalID: journalID, Ref: ref, Date: date,
		Memo: "loan disbursement " + loan.Label,
		Lines: []finance.EntryLine{
			{AccountID: receivableAcct, Label: "loan receivable " + ref, Debit: loan.Principal},
			{AccountID: bankAcct, Label: "bank " + ref, Credit: loan.Principal},
		},
		CreatedBy: createdBy}
	if err := ledger.PostEntry(ctx, db, e); err != nil {
		return nil, err
	}
	return e, nil
}

// PostLoanRepayment posts one installment: DR bank for the payment,
// CR loan receivable for the principal leg, CR interest income for the
// interest leg (omitted when zero). Returns the posted entry.
func PostLoanRepayment(ctx context.Context, db platform.DBTX, ledger LedgerPoster,
	entityID, journalID int64, loan MemberLoan, line MemberLoanLine,
	bankAcct, receivableAcct, interestAcct int64, date time.Time, createdBy *int64) (*finance.Entry, error) {
	if line.Paid {
		return nil, fmt.Errorf("members: installment already paid: %w", platform.ErrConflict)
	}
	if bankAcct == 0 || receivableAcct == 0 || (line.Interest != 0 && interestAcct == 0) {
		return nil, fmt.Errorf("members: repayment needs bank, receivable and interest accounts: %w", platform.ErrValidation)
	}
	ref := fmt.Sprintf("LOAN-%d-R%02d", loan.ID, line.Seq)
	e := &finance.Entry{EntityID: entityID, JournalID: journalID, Ref: ref, Date: date,
		Memo: "loan repayment " + loan.Label,
		Lines: []finance.EntryLine{
			{AccountID: bankAcct, Label: "bank " + ref, Debit: line.Payment},
			{AccountID: receivableAcct, Label: "loan receivable " + ref, Credit: line.Principal},
		},
		CreatedBy: createdBy}
	if line.Interest != 0 {
		e.Lines = append(e.Lines, finance.EntryLine{
			AccountID: interestAcct, Label: "loan interest " + ref, Credit: line.Interest})
	}
	if err := ledger.PostEntry(ctx, db, e); err != nil {
		return nil, err
	}
	return e, nil
}

// --- Lookups shared by receipt + workflow handlers ---

// SubscriptionByID fetches one subscription on the PG store.
func (s *PGStore) SubscriptionByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Subscription, error) {
	return scanSub(db.QueryRow(ctx, `SELECT `+subCols+` FROM ferp_subscriptions WHERE id=$1 AND entity_id=$2`, id, entityID))
}

// SubscriptionByID fetches one subscription on the memory fake.
func (m *MemoryStore) SubscriptionByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok || s.EntityID != entityID {
		return Subscription{}, identity.ErrNotFound
	}
	return s, nil
}

// DonationByID fetches one donation on the PG store.
func (s *PGStore) DonationByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Donation, error) {
	return scanDonation(db.QueryRow(ctx, `SELECT `+donationCols+` FROM ferp_donations WHERE id=$1 AND entity_id=$2`, id, entityID))
}

// DonationByID fetches one donation on the memory fake.
func (m *MemoryStore) DonationByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (Donation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.dons[id]
	if !ok || d.EntityID != entityID {
		return Donation{}, identity.ErrNotFound
	}
	return d, nil
}

// --- Member-loan persistence ---

const memberLoanCols = `id, entity_id, label, principal, rate_bps, start_date, periods, status, created_at, updated_at, row_version`

func scanMemberLoan(row pgx.Row) (MemberLoan, error) {
	var l MemberLoan
	err := row.Scan(&l.ID, &l.EntityID, &l.Label, &l.Principal, &l.RateBps,
		&l.Start, &l.Periods, &l.Status, &l.CreatedAt, &l.UpdatedAt, &l.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemberLoan{}, identity.ErrNotFound
	}
	return l, err
}

const memberLoanLineCols = `id, loan_id, seq, due_date, payment, principal, interest, remaining, paid, paid_at`

func scanMemberLoanLine(row pgx.Row) (MemberLoanLine, error) {
	var l MemberLoanLine
	err := row.Scan(&l.ID, &l.LoanID, &l.Seq, &l.DueDate, &l.Payment,
		&l.Principal, &l.Interest, &l.Remaining, &l.Paid, &l.PaidAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemberLoanLine{}, identity.ErrNotFound
	}
	return l, err
}

// CreateMemberLoan validates, generates the schedule, and persists header +
// lines atomically on the PG store.
func (s *PGStore) CreateMemberLoan(ctx context.Context, db platform.DBTX, l *MemberLoan) error {
	if err := l.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	lines, err := BuildAmortisation(l.Principal, l.RateBps, l.Start, l.Periods)
	if err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	tx, finish, err := platform.JoinTx(ctx, s.pool, db)
	if err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO ferp_member_loans
		(entity_id, label, principal, rate_bps, start_date, periods, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, row_version`,
		l.EntityID, l.Label, l.Principal, l.RateBps, l.Start, l.Periods, l.Status,
	).Scan(&l.ID, &l.RowVersion); err != nil {
		return finish(err)
	}
	for i := range lines {
		lines[i].LoanID = l.ID
		if err := tx.QueryRow(ctx, `INSERT INTO ferp_member_loan_lines
			(loan_id, seq, due_date, payment, principal, interest, remaining)
			VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			l.ID, lines[i].Seq, lines[i].DueDate, lines[i].Payment,
			lines[i].Principal, lines[i].Interest, lines[i].Remaining,
		).Scan(&lines[i].ID); err != nil {
			return finish(err)
		}
	}
	return finish(nil)
}

// MemberLoanByID fetches one loan on the PG store.
func (s *PGStore) MemberLoanByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (MemberLoan, error) {
	return scanMemberLoan(db.QueryRow(ctx, `SELECT `+memberLoanCols+` FROM ferp_member_loans WHERE id=$1 AND entity_id=$2`, id, entityID))
}

// MemberLoanSchedule lists installments in seq order on the PG store.
func (s *PGStore) MemberLoanSchedule(ctx context.Context, db platform.DBTX, entityID int64, loanID int64) ([]MemberLoanLine, error) {
	if _, err := s.MemberLoanByID(ctx, db, entityID, loanID); err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, `SELECT `+memberLoanLineCols+` FROM ferp_member_loan_lines
		WHERE loan_id=$1 ORDER BY seq`, loanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemberLoanLine
	for rows.Next() {
		l, err := scanMemberLoanLine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SetMemberLoanStatus moves a loan along its lifecycle on the PG store.
func (s *PGStore) SetMemberLoanStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to MemberLoanStatus, rowVersion int64) (MemberLoan, error) {
	l, err := s.MemberLoanByID(ctx, db, entityID, id)
	if err != nil {
		return MemberLoan{}, err
	}
	if l.RowVersion != rowVersion {
		return MemberLoan{}, identity.ErrVersionConflict
	}
	if !l.CanTransition(to) {
		return MemberLoan{}, fmt.Errorf("members: illegal loan transition: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_member_loans SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND entity_id=$4 AND row_version=$3`, to, id, rowVersion, entityID)
	if err != nil {
		return MemberLoan{}, err
	}
	if tag.RowsAffected() == 0 {
		return MemberLoan{}, identity.ErrVersionConflict
	}
	l.Status = to
	l.RowVersion++
	return l, nil
}

// MarkLoanLinePaid flags one installment paid on the PG store.
func (s *PGStore) MarkLoanLinePaid(ctx context.Context, db platform.DBTX, entityID int64, loanID int64, seq int, at time.Time) (MemberLoanLine, error) {
	if _, err := s.MemberLoanByID(ctx, db, entityID, loanID); err != nil {
		return MemberLoanLine{}, err
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_member_loan_lines SET paid=TRUE, paid_at=$1
		WHERE loan_id=$2 AND seq=$3 AND paid=FALSE`, at, loanID, seq)
	if err != nil {
		return MemberLoanLine{}, err
	}
	if tag.RowsAffected() == 0 {
		return MemberLoanLine{}, fmt.Errorf("members: installment missing or already paid: %w", platform.ErrConflict)
	}
	return scanMemberLoanLine(db.QueryRow(ctx, `SELECT `+memberLoanLineCols+` FROM ferp_member_loan_lines
		WHERE loan_id=$1 AND seq=$2`, loanID, seq))
}

// CreateMemberLoan validates, generates the schedule, and stores both on the
// memory fake.
func (m *MemoryStore) CreateMemberLoan(_ context.Context, _ platform.DBTX, l *MemberLoan) error {
	if err := l.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	lines, err := BuildAmortisation(l.Principal, l.RateBps, l.Start, l.Periods)
	if err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l.ID = m.next()
	l.RowVersion = 1
	m.mloans[l.ID] = *l
	for i := range lines {
		lines[i].ID = m.next()
		lines[i].LoanID = l.ID
	}
	m.mlines[l.ID] = lines
	return nil
}

// MemberLoanByID fetches one loan on the memory fake.
func (m *MemoryStore) MemberLoanByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (MemberLoan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.mloans[id]
	if !ok || l.EntityID != entityID {
		return MemberLoan{}, identity.ErrNotFound
	}
	return l, nil
}

// MemberLoanSchedule lists installments on the memory fake.
func (m *MemoryStore) MemberLoanSchedule(_ context.Context, _ platform.DBTX, entityID int64, loanID int64) ([]MemberLoanLine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.mloans[loanID]
	if !ok || l.EntityID != entityID {
		return nil, identity.ErrNotFound
	}
	return append([]MemberLoanLine(nil), m.mlines[loanID]...), nil
}

// SetMemberLoanStatus moves a loan along its lifecycle on the memory fake.
func (m *MemoryStore) SetMemberLoanStatus(_ context.Context, _ platform.DBTX, entityID int64, id int64, to MemberLoanStatus, rowVersion int64) (MemberLoan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.mloans[id]
	if !ok || l.EntityID != entityID {
		return MemberLoan{}, identity.ErrNotFound
	}
	if l.RowVersion != rowVersion {
		return MemberLoan{}, identity.ErrVersionConflict
	}
	if !l.CanTransition(to) {
		return MemberLoan{}, fmt.Errorf("members: illegal loan transition: %w", platform.ErrValidation)
	}
	l.Status = to
	l.RowVersion++
	m.mloans[id] = l
	return l, nil
}

// MarkLoanLinePaid flags one installment paid on the memory fake.
func (m *MemoryStore) MarkLoanLinePaid(_ context.Context, _ platform.DBTX, entityID int64, loanID int64, seq int, at time.Time) (MemberLoanLine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.mloans[loanID]
	if !ok || l.EntityID != entityID {
		return MemberLoanLine{}, identity.ErrNotFound
	}
	lines := m.mlines[loanID]
	for i := range lines {
		if lines[i].Seq == seq {
			if lines[i].Paid {
				return MemberLoanLine{}, fmt.Errorf("members: installment already paid: %w", platform.ErrConflict)
			}
			lines[i].Paid = true
			lines[i].PaidAt = &at
			m.mlines[loanID] = lines
			return lines[i], nil
		}
	}
	return MemberLoanLine{}, fmt.Errorf("members: installment missing: %w", platform.ErrNotFound)
}
