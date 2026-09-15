package finance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a row does not exist (alias of the platform
// kernel sentinel so errors.Is works across packages).
var ErrNotFound = platform.ErrNotFound

// Store is the persistence contract for finance.
type Store interface {
	CreateAccount(ctx context.Context, db platform.DBTX, a *Account) error
	// Accounts lists the chart of accounts (reporting + UI).
	Accounts(ctx context.Context, db platform.DBTX, entityID int64) ([]Account, error)
	ListJournals(ctx context.Context, db platform.DBTX, entityID int64) ([]Journal, error)
	ListBankAccounts(ctx context.Context, db platform.DBTX, entityID int64) ([]BankAccount, error)
	ListLoans(ctx context.Context, db platform.DBTX, entityID int64) ([]Loan, error)
	CreateJournal(ctx context.Context, db platform.DBTX, j *Journal) error
	CreateFiscalYear(ctx context.Context, db platform.DBTX, f *FiscalYear) error
	// PostEntry validates balance + fiscal-year lock, chains the hash, and persists atomically.
	PostEntry(ctx context.Context, db platform.DBTX, e *Entry) error
	EntriesByJournal(ctx context.Context, db platform.DBTX, journalID int64) ([]Entry, error)
	// TrialBalance returns per-account debit/credit sums for posted entries.
	TrialBalance(ctx context.Context, db platform.DBTX, entityID int64) (map[int64][2]int64, error)
	CreateBankAccount(ctx context.Context, db platform.DBTX, a *BankAccount) error
	RecordTransaction(ctx context.Context, db platform.DBTX, t *BankTransaction) error
	Reconcile(ctx context.Context, db platform.DBTX, entityID, txID int64, at time.Time) error
	AccountBalance(ctx context.Context, db platform.DBTX, accountID int64) (int64, error)
	CreateLoan(ctx context.Context, db platform.DBTX, l *Loan) error
	// Phase 2: statement import (idempotent on bank ref), statement +
	// running balance, and atomic paired transfers.
	ImportTransactions(ctx context.Context, db platform.DBTX, entityID, accountID int64, lines []ImportLine) (imported, skipped int, err error)
	BankTransactions(ctx context.Context, db platform.DBTX, accountID int64) ([]BankTransaction, error)
	BankStatement(ctx context.Context, db platform.DBTX, accountID int64) ([]StatementLine, error)
	Transfer(ctx context.Context, db platform.DBTX, c TransferCmd) error
	// Phase 2: fiscal years, general-ledger book and account drill-down.
	FiscalYearByID(ctx context.Context, db platform.DBTX, entityID, yearID int64) (FiscalYear, error)
	ListFiscalYears(ctx context.Context, db platform.DBTX, entityID int64) ([]FiscalYear, error)
	SetFiscalYearLocked(ctx context.Context, db platform.DBTX, entityID, yearID int64, locked bool) error
	LedgerBook(ctx context.Context, db platform.DBTX, entityID int64, from, to time.Time) ([]Entry, error)
	AccountLedger(ctx context.Context, db platform.DBTX, entityID, accountID int64, from, to time.Time) (AccountDrilldown, error)
	// Phase 2: tax periods, VAT rates + return, social/fiscal charges.
	CreateTaxPeriod(ctx context.Context, db platform.DBTX, p *TaxPeriod) error
	ListTaxPeriods(ctx context.Context, db platform.DBTX, entityID int64) ([]TaxPeriod, error)
	SetTaxPeriodStatus(ctx context.Context, db platform.DBTX, entityID, periodID int64, st TaxPeriodStatus) error
	CreateVATRate(ctx context.Context, db platform.DBTX, v *VATRate) error
	ListVATRates(ctx context.Context, db platform.DBTX, entityID int64) ([]VATRate, error)
	VATReturn(ctx context.Context, db platform.DBTX, entityID int64, from, to time.Time) (VATReturn, error)
	CreateCharge(ctx context.Context, db platform.DBTX, c *TaxCharge) error
	ListCharges(ctx context.Context, db platform.DBTX, entityID int64) ([]TaxCharge, error)
	ChargesDue(ctx context.Context, db platform.DBTX, entityID int64, asOf time.Time) ([]TaxCharge, error)
	MarkChargePaid(ctx context.Context, db platform.DBTX, entityID, chargeID int64, at time.Time) error
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (s *PGStore) CreateAccount(ctx context.Context, db platform.DBTX, a *Account) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_accounts (entity_id, code, label, type)
		VALUES ($1,$2,$3,$4) RETURNING id`,
		a.EntityID, a.Code, a.Label, a.Type).Scan(&a.ID)
}

// Accounts lists an entity's chart of accounts (reporting P&L).
func (s *PGStore) Accounts(ctx context.Context, db platform.DBTX, entityID int64) ([]Account, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, code, label, type FROM ferp_accounts
		WHERE entity_id=$1 ORDER BY code`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.EntityID, &a.Code, &a.Label, &a.Type); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PGStore) ListJournals(ctx context.Context, db platform.DBTX, entityID int64) ([]Journal, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, code, label FROM ferp_journals
		WHERE entity_id=$1 ORDER BY code`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Journal
	for rows.Next() {
		var j Journal
		if err := rows.Scan(&j.ID, &j.EntityID, &j.Code, &j.Label); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *PGStore) ListBankAccounts(ctx context.Context, db platform.DBTX, entityID int64) ([]BankAccount, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, code, label, iban FROM ferp_bank_accounts
		WHERE entity_id=$1 ORDER BY code`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BankAccount
	for rows.Next() {
		var b BankAccount
		if err := rows.Scan(&b.ID, &b.EntityID, &b.Code, &b.Label, &b.IBAN); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *PGStore) ListLoans(ctx context.Context, db platform.DBTX, entityID int64) ([]Loan, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, label, principal, rate_bps, start_date, periods
		FROM ferp_loans WHERE entity_id=$1 ORDER BY id`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Loan
	for rows.Next() {
		var l Loan
		if err := rows.Scan(&l.ID, &l.EntityID, &l.Label, &l.Principal, &l.RateBps, &l.Start, &l.Periods); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *PGStore) CreateJournal(ctx context.Context, db platform.DBTX, j *Journal) error {
	return db.QueryRow(ctx, `INSERT INTO ferp_journals (entity_id, code, label)
		VALUES ($1,$2,$3) RETURNING id`, j.EntityID, j.Code, j.Label).Scan(&j.ID)
}

func (s *PGStore) CreateFiscalYear(ctx context.Context, db platform.DBTX, f *FiscalYear) error {
	return db.QueryRow(ctx, `INSERT INTO ferp_fiscal_years (entity_id, label, start_date, end_date, locked)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		f.EntityID, f.Label, f.StartDate, f.EndDate, f.Locked).Scan(&f.ID)
}

func (s *PGStore) PostEntry(ctx context.Context, db platform.DBTX, e *Entry) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	// Joins the caller's transaction when one is in flight (service
	// orchestration); otherwise posts in its own transaction.
	tx, finish, err := platform.JoinTx(ctx, s.pool, db)
	if err != nil {
		return err
	}
	// Serialize posters per entity so concurrent writers queue on the chain
	// head instead of forking it. Xact-scoped: released at commit/rollback.
	// The UNIQUE (entity_id, prev_hash) constraint backstops any path that
	// bypasses this lock.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('ferp_entries')::bigint, $1::bigint)`, e.EntityID); err != nil {
		return finish(err)
	}
	// Fiscal-year lock: entry date must fall in an unlocked year (or no year defined).
	var locked bool
	err = tx.QueryRow(ctx, `SELECT COALESCE(BOOL_OR(locked), FALSE) FROM ferp_fiscal_years
		WHERE entity_id=$1 AND start_date<=$2 AND end_date>=$2`, e.EntityID, e.Date).Scan(&locked)
	if err != nil {
		return finish(err)
	}
	if locked {
		return finish(fmt.Errorf("finance: fiscal year locked for entry date: %w", platform.ErrValidation))
	}
	// Chain head.
	var prev string
	err = tx.QueryRow(ctx, `SELECT chain_hash FROM ferp_entries WHERE entity_id=$1
		ORDER BY id DESC LIMIT 1`, e.EntityID).Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		prev = GenesisHash
	} else if err != nil {
		return finish(err)
	}
	e.PrevHash = prev
	e.ChainHash = Chain(prev, e.JournalID, e.Ref, e.Date, e.Lines)
	e.Status = EntryPosted
	err = tx.QueryRow(ctx, `INSERT INTO ferp_entries
		(entity_id, journal_id, ref, date, memo, status, prev_hash, chain_hash, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id, row_version`,
		e.EntityID, e.JournalID, e.Ref, e.Date, e.Memo, e.Status, e.PrevHash, e.ChainHash, e.CreatedBy,
	).Scan(&e.ID, &e.RowVersion)
	if err != nil {
		return finish(err)
	}
	for i, l := range e.Lines {
		if _, err := tx.Exec(ctx, `INSERT INTO ferp_entry_lines
			(entry_id, pos, account_id, label, debit, credit, vat_rate_bps) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			e.ID, i, l.AccountID, l.Label, l.Debit, l.Credit, l.VATRateBps); err != nil {
			return finish(err)
		}
	}
	return finish(nil)
}

type queryFunc func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)

func loadEntries(ctx context.Context, q queryFunc, journalID int64) ([]Entry, error) {
	rows, err := q(ctx, `SELECT id, entity_id, journal_id, ref, date, memo, status,
		prev_hash, chain_hash, created_by, row_version FROM ferp_entries
		WHERE journal_id=$1 ORDER BY id`, journalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.EntityID, &e.JournalID, &e.Ref, &e.Date, &e.Memo,
			&e.Status, &e.PrevHash, &e.ChainHash, &e.CreatedBy, &e.RowVersion); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		lr, err := q(ctx, `SELECT account_id, label, debit, credit, vat_rate_bps FROM ferp_entry_lines
			WHERE entry_id=$1 ORDER BY pos, id`, out[i].ID)
		if err != nil {
			return nil, err
		}
		var lines []EntryLine
		for lr.Next() {
			var l EntryLine
			if err := lr.Scan(&l.AccountID, &l.Label, &l.Debit, &l.Credit, &l.VATRateBps); err != nil {
				lr.Close()
				return nil, err
			}
			lines = append(lines, l)
		}
		lr.Close()
		if err := lr.Err(); err != nil {
			return nil, err
		}
		out[i].Lines = lines
	}
	return out, nil
}

func (s *PGStore) EntriesByJournal(ctx context.Context, db platform.DBTX, journalID int64) ([]Entry, error) {
	return loadEntries(ctx, db.Query, journalID)
}

func (s *PGStore) TrialBalance(ctx context.Context, db platform.DBTX, entityID int64) (map[int64][2]int64, error) {
	rows, err := db.Query(ctx, `SELECT l.account_id, SUM(l.debit), SUM(l.credit)
		FROM ferp_entry_lines l JOIN ferp_entries e ON e.id=l.entry_id
		WHERE e.entity_id=$1 AND e.status=1 GROUP BY l.account_id`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][2]int64{}
	for rows.Next() {
		var id, dr, cr int64
		if err := rows.Scan(&id, &dr, &cr); err != nil {
			return nil, err
		}
		out[id] = [2]int64{dr, cr}
	}
	return out, rows.Err()
}

func (s *PGStore) CreateBankAccount(ctx context.Context, db platform.DBTX, a *BankAccount) error {
	return db.QueryRow(ctx, `INSERT INTO ferp_bank_accounts (entity_id, code, label, iban)
		VALUES ($1,$2,$3,$4) RETURNING id`, a.EntityID, a.Code, a.Label, a.IBAN).Scan(&a.ID)
}

func (s *PGStore) RecordTransaction(ctx context.Context, db platform.DBTX, t *BankTransaction) error {
	if err := t.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_bank_transactions
		(entity_id, account_id, amount, label, bank_ref, value_date) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		t.EntityID, t.AccountID, t.Amount, t.Label, t.BankRef, t.ValueDate).Scan(&t.ID)
}

func (s *PGStore) Reconcile(ctx context.Context, db platform.DBTX, entityID, txID int64, at time.Time) error {
	tag, err := db.Exec(ctx, `UPDATE ferp_bank_transactions SET reconciled=TRUE, reconciled_at=$1
		WHERE id=$2 AND entity_id=$3 AND reconciled=FALSE`, at, txID, entityID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("finance: transaction already reconciled or missing: %w", platform.ErrConflict)
	}
	return nil
}

func (s *PGStore) AccountBalance(ctx context.Context, db platform.DBTX, accountID int64) (int64, error) {
	var bal int64
	_ = db.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0) FROM ferp_bank_transactions WHERE account_id=$1`, accountID).Scan(&bal)
	return bal, nil
}

func (s *PGStore) CreateLoan(ctx context.Context, db platform.DBTX, l *Loan) error {
	if l.Principal <= 0 || l.Periods <= 0 {
		return fmt.Errorf("finance: bad loan terms: %w", platform.ErrValidation)
	}
	l.Schedule = BuildSchedule(l.Principal, l.RateBps, l.Start, l.Periods)
	return db.QueryRow(ctx, `INSERT INTO ferp_loans
		(entity_id, label, principal, rate_bps, start_date, periods) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		l.EntityID, l.Label, l.Principal, l.RateBps, l.Start, l.Periods).Scan(&l.ID)
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu      sync.Mutex
	seq     int64
	accts   map[int64]Account
	jrns    map[int64]Journal
	years   []FiscalYear
	entries []Entry
	banks   map[int64]BankAccount
	txs     map[int64]BankTransaction
	loans   map[int64]Loan
	periods map[int64]TaxPeriod
	rates   map[int64]VATRate
	charges map[int64]TaxCharge
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{accts: map[int64]Account{}, jrns: map[int64]Journal{},
		banks: map[int64]BankAccount{}, txs: map[int64]BankTransaction{}, loans: map[int64]Loan{},
		periods: map[int64]TaxPeriod{}, rates: map[int64]VATRate{}, charges: map[int64]TaxCharge{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateAccount(_ context.Context, _ platform.DBTX, a *Account) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a.ID = m.next()
	m.accts[a.ID] = *a
	return nil
}

// Accounts lists an entity's chart of accounts (reporting P&L).
func (m *MemoryStore) Accounts(_ context.Context, _ platform.DBTX, entityID int64) ([]Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Account
	for _, a := range m.accts {
		if a.EntityID == entityID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *MemoryStore) ListJournals(_ context.Context, _ platform.DBTX, entityID int64) ([]Journal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Journal
	for _, j := range m.jrns {
		if j.EntityID == entityID {
			out = append(out, j)
		}
	}
	return out, nil
}

func (m *MemoryStore) ListBankAccounts(_ context.Context, _ platform.DBTX, entityID int64) ([]BankAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BankAccount
	for _, b := range m.banks {
		if b.EntityID == entityID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (m *MemoryStore) ListLoans(_ context.Context, _ platform.DBTX, entityID int64) ([]Loan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Loan
	for _, l := range m.loans {
		if l.EntityID == entityID {
			out = append(out, l)
		}
	}
	return out, nil
}

func (m *MemoryStore) CreateJournal(_ context.Context, _ platform.DBTX, j *Journal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j.ID = m.next()
	m.jrns[j.ID] = *j
	return nil
}

func (m *MemoryStore) CreateFiscalYear(_ context.Context, _ platform.DBTX, f *FiscalYear) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f.ID = m.next()
	m.years = append(m.years, *f)
	return nil
}

func (m *MemoryStore) PostEntry(_ context.Context, _ platform.DBTX, e *Entry) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, y := range m.years {
		if y.EntityID == e.EntityID && y.Contains(e.Date) && y.Locked {
			return fmt.Errorf("finance: fiscal year locked for entry date: %w", platform.ErrValidation)
		}
	}
	prev := GenesisHash
	for _, x := range m.entries {
		if x.EntityID == e.EntityID {
			prev = x.ChainHash
		}
	}
	e.PrevHash = prev
	e.ChainHash = Chain(prev, e.JournalID, e.Ref, e.Date, e.Lines)
	e.Status = EntryPosted
	e.ID = m.next()
	e.RowVersion = 1
	m.entries = append(m.entries, *e)
	return nil
}

func (m *MemoryStore) EntriesByJournal(_ context.Context, _ platform.DBTX, journalID int64) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Entry
	for _, e := range m.entries {
		if e.JournalID == journalID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *MemoryStore) TrialBalance(_ context.Context, _ platform.DBTX, entityID int64) (map[int64][2]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[int64][2]int64{}
	for _, e := range m.entries {
		if e.EntityID != entityID || e.Status != EntryPosted {
			continue
		}
		for _, l := range e.Lines {
			acc := out[l.AccountID]
			acc[0] += l.Debit
			acc[1] += l.Credit
			out[l.AccountID] = acc
		}
	}
	return out, nil
}

func (m *MemoryStore) CreateBankAccount(_ context.Context, _ platform.DBTX, a *BankAccount) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a.ID = m.next()
	m.banks[a.ID] = *a
	return nil
}

func (m *MemoryStore) RecordTransaction(_ context.Context, _ platform.DBTX, t *BankTransaction) error {
	if err := t.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t.ID = m.next()
	m.txs[t.ID] = *t
	return nil
}

func (m *MemoryStore) Reconcile(_ context.Context, _ platform.DBTX, entityID, txID int64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.txs[txID]
	if !ok || t.EntityID != entityID {
		return ErrNotFound
	}
	if t.Reconciled {
		return fmt.Errorf("finance: transaction already reconciled or missing: %w", platform.ErrConflict)
	}
	t.Reconciled = true
	t.ReconciledAt = &at
	m.txs[txID] = t
	return nil
}

func (m *MemoryStore) AccountBalance(_ context.Context, _ platform.DBTX, accountID int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var bal int64
	for _, t := range m.txs {
		if t.AccountID == accountID {
			bal += t.Amount
		}
	}
	return bal, nil
}

func (m *MemoryStore) CreateLoan(_ context.Context, _ platform.DBTX, l *Loan) error {
	if l.Principal <= 0 || l.Periods <= 0 {
		return fmt.Errorf("finance: bad loan terms: %w", platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l.Schedule = BuildSchedule(l.Principal, l.RateBps, l.Start, l.Periods)
	l.ID = m.next()
	m.loans[l.ID] = *l
	return nil
}
