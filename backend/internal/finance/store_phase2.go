package finance

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Phase 2 persistence: statement import, transfers, fiscal years, ledger
// book, tax periods / VAT / charges. Store methods take (ctx, db
// platform.DBTX, ...) so service.go orchestration joins one transaction.

// ---------- bank import / statement / transfer (PG) ----------

// ImportTransactions inserts statement lines, skipping ones whose bank ref
// already exists for (entity, account). Empty refs are never deduped.
func (s *PGStore) ImportTransactions(ctx context.Context, db platform.DBTX, entityID, accountID int64, lines []ImportLine) (imported, skipped int, err error) {
	for _, l := range lines {
		if l.Amount == 0 {
			return imported, skipped, fmt.Errorf("finance: zero-amount import line %q: %w", l.Ref, platform.ErrValidation)
		}
		t := &BankTransaction{EntityID: entityID, AccountID: accountID,
			Amount: l.Amount, Label: l.Label, BankRef: l.Ref, ValueDate: l.ValueDate}
		if l.Ref == "" {
			if err := s.RecordTransaction(ctx, db, t); err != nil {
				return imported, skipped, err
			}
			imported++
			continue
		}
		tag, err := db.Exec(ctx, `INSERT INTO ferp_bank_transactions
			(entity_id, account_id, amount, label, bank_ref, value_date)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (entity_id, account_id, bank_ref) WHERE bank_ref <> '' DO NOTHING`,
			t.EntityID, t.AccountID, t.Amount, t.Label, t.BankRef, t.ValueDate)
		if err != nil {
			return imported, skipped, err
		}
		if tag.RowsAffected() == 0 {
			skipped++
			continue
		}
		imported++
	}
	return imported, skipped, nil
}

// BankTransactions lists one account's movements in statement order.
func (s *PGStore) BankTransactions(ctx context.Context, db platform.DBTX, accountID int64) ([]BankTransaction, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, account_id, amount, label, bank_ref,
		value_date, reconciled, reconciled_at FROM ferp_bank_transactions
		WHERE account_id=$1 ORDER BY value_date, id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BankTransaction
	for rows.Next() {
		var t BankTransaction
		if err := rows.Scan(&t.ID, &t.EntityID, &t.AccountID, &t.Amount, &t.Label,
			&t.BankRef, &t.ValueDate, &t.Reconciled, &t.ReconciledAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// BankStatement returns movements annotated with the running balance.
func (s *PGStore) BankStatement(ctx context.Context, db platform.DBTX, accountID int64) ([]StatementLine, error) {
	txs, err := s.BankTransactions(ctx, db, accountID)
	if err != nil {
		return nil, err
	}
	return ApplyRunningBalance(txs), nil
}

// Transfer posts the paired out/in legs atomically (serializes with the
// entry chain lock only for entries; bank legs join the caller's tx).
func (s *PGStore) Transfer(ctx context.Context, db platform.DBTX, c TransferCmd) error {
	if err := c.validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	tx, finish, err := platform.JoinTx(ctx, s.pool, db)
	if err != nil {
		return err
	}
	for _, id := range []int64{c.FromAccountID, c.ToAccountID} {
		var entity int64
		if err := tx.QueryRow(ctx, `SELECT entity_id FROM ferp_bank_accounts WHERE id=$1`, id).Scan(&entity); err != nil {
			return finish(fmt.Errorf("finance: bank account %d: %w", id, platform.ErrNotFound))
		}
		_ = entity
	}
	date := c.ValueDate
	if date.IsZero() {
		date = time.Now().UTC()
	}
	outRef, inRef := c.Ref, c.Ref
	if outRef != "" {
		outRef += "/out"
		inRef += "/in"
	}
	for _, leg := range []BankTransaction{
		{EntityID: c.EntityID, AccountID: c.FromAccountID, Amount: -c.Amount, Label: c.Label, BankRef: outRef, ValueDate: date},
		{EntityID: c.EntityID, AccountID: c.ToAccountID, Amount: c.Amount, Label: c.Label, BankRef: inRef, ValueDate: date},
	} {
		if err := leg.Validate(); err != nil {
			return finish(fmt.Errorf("%w: %w", err, platform.ErrValidation))
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ferp_bank_transactions
			(entity_id, account_id, amount, label, bank_ref, value_date) VALUES ($1,$2,$3,$4,$5,$6)`,
			leg.EntityID, leg.AccountID, leg.Amount, leg.Label, leg.BankRef, leg.ValueDate); err != nil {
			return finish(err)
		}
	}
	return finish(nil)
}

// ---------- fiscal years / ledger book (PG) ----------

// FiscalYearByID loads one year within the caller's entity.
func (s *PGStore) FiscalYearByID(ctx context.Context, db platform.DBTX, entityID, yearID int64) (FiscalYear, error) {
	var f FiscalYear
	err := db.QueryRow(ctx, `SELECT id, entity_id, label, start_date, end_date, locked
		FROM ferp_fiscal_years WHERE id=$1 AND entity_id=$2`, yearID, entityID).
		Scan(&f.ID, &f.EntityID, &f.Label, &f.StartDate, &f.EndDate, &f.Locked)
	if err != nil {
		return FiscalYear{}, fmt.Errorf("finance: fiscal year %d: %w", yearID, platform.ErrNotFound)
	}
	return f, nil
}

// ListFiscalYears lists an entity's fiscal years.
func (s *PGStore) ListFiscalYears(ctx context.Context, db platform.DBTX, entityID int64) ([]FiscalYear, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, label, start_date, end_date, locked
		FROM ferp_fiscal_years WHERE entity_id=$1 ORDER BY start_date, id`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FiscalYear
	for rows.Next() {
		var f FiscalYear
		if err := rows.Scan(&f.ID, &f.EntityID, &f.Label, &f.StartDate, &f.EndDate, &f.Locked); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SetFiscalYearLocked flips the year lock (year-end close sets it).
func (s *PGStore) SetFiscalYearLocked(ctx context.Context, db platform.DBTX, entityID, yearID int64, locked bool) error {
	tag, err := db.Exec(ctx, `UPDATE ferp_fiscal_years SET locked=$1 WHERE id=$2 AND entity_id=$3`,
		locked, yearID, entityID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("finance: fiscal year %d: %w", yearID, platform.ErrNotFound)
	}
	return nil
}

func (s *PGStore) entryLines(ctx context.Context, db platform.DBTX, entryID int64) ([]EntryLine, error) {
	rows, err := db.Query(ctx, `SELECT account_id, label, debit, credit, vat_rate_bps
		FROM ferp_entry_lines WHERE entry_id=$1 ORDER BY pos, id`, entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EntryLine
	for rows.Next() {
		var l EntryLine
		if err := rows.Scan(&l.AccountID, &l.Label, &l.Debit, &l.Credit, &l.VATRateBps); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LedgerBook returns posted + draft entries with lines in date order.
func (s *PGStore) LedgerBook(ctx context.Context, db platform.DBTX, entityID int64, from, to time.Time) ([]Entry, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, journal_id, ref, date, memo, status,
		prev_hash, chain_hash, created_by, row_version FROM ferp_entries
		WHERE entity_id=$1 AND date>=$2 AND date<=$3 ORDER BY date, id`, entityID, from, to)
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
		lines, err := s.entryLines(ctx, db, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Lines = lines
	}
	return out, nil
}

// AccountLedger drills one account: opening net before from, lines in range.
func (s *PGStore) AccountLedger(ctx context.Context, db platform.DBTX, entityID, accountID int64, from, to time.Time) (AccountDrilldown, error) {
	out := AccountDrilldown{AccountID: accountID}
	_ = db.QueryRow(ctx, `SELECT COALESCE(SUM(l.debit - l.credit),0) FROM ferp_entry_lines l
		JOIN ferp_entries e ON e.id=l.entry_id
		WHERE e.entity_id=$1 AND e.status=1 AND l.account_id=$2 AND e.date<$3`,
		entityID, accountID, from).Scan(&out.Opening)
	rows, err := db.Query(ctx, `SELECT e.id, e.date, e.ref, e.memo, l.label, l.debit, l.credit
		FROM ferp_entry_lines l JOIN ferp_entries e ON e.id=l.entry_id
		WHERE e.entity_id=$1 AND e.status=1 AND l.account_id=$2 AND e.date>=$3 AND e.date<=$4
		ORDER BY e.date, e.id`, entityID, accountID, from, to)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var l AccountLedgerLine
		if err := rows.Scan(&l.EntryID, &l.Date, &l.Ref, &l.Memo, &l.Label, &l.Debit, &l.Credit); err != nil {
			return out, err
		}
		out.Lines = append(out.Lines, l)
		out.Closing += l.Debit - l.Credit
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Closing += out.Opening
	return out, nil
}

// ---------- tax periods / VAT / charges (PG) ----------

// CreateTaxPeriod opens a declaration window.
func (s *PGStore) CreateTaxPeriod(ctx context.Context, db platform.DBTX, p *TaxPeriod) error {
	if p.Status == "" {
		p.Status = TaxPeriodOpen
	}
	if err := p.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_tax_periods (entity_id, label, start_date, end_date, status)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		p.EntityID, p.Label, p.Start, p.End, string(p.Status)).Scan(&p.ID)
}

// ListTaxPeriods lists an entity's declaration windows.
func (s *PGStore) ListTaxPeriods(ctx context.Context, db platform.DBTX, entityID int64) ([]TaxPeriod, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, label, start_date, end_date, status
		FROM ferp_tax_periods WHERE entity_id=$1 ORDER BY start_date, id`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaxPeriod
	for rows.Next() {
		var p TaxPeriod
		var st string
		if err := rows.Scan(&p.ID, &p.EntityID, &p.Label, &p.Start, &p.End, &st); err != nil {
			return nil, err
		}
		p.Status = TaxPeriodStatus(st)
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetTaxPeriodStatus advances a period (open → filed → paid).
func (s *PGStore) SetTaxPeriodStatus(ctx context.Context, db platform.DBTX, entityID, periodID int64, st TaxPeriodStatus) error {
	switch st {
	case TaxPeriodOpen, TaxPeriodFiled, TaxPeriodPaid:
	default:
		return fmt.Errorf("finance: bad tax period status %q: %w", st, platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_tax_periods SET status=$1 WHERE id=$2 AND entity_id=$3`,
		string(st), periodID, entityID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("finance: tax period %d: %w", periodID, platform.ErrNotFound)
	}
	return nil
}

// CreateVATRate registers rate metadata.
func (s *PGStore) CreateVATRate(ctx context.Context, db platform.DBTX, v *VATRate) error {
	if err := v.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	var acct any
	if v.VATAccountID != 0 {
		acct = v.VATAccountID
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_vat_rates (entity_id, code, label, rate_bps, vat_account_id)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		v.EntityID, v.Code, v.Label, v.RateBps, acct).Scan(&v.ID)
}

// ListVATRates lists an entity's rate metadata.
func (s *PGStore) ListVATRates(ctx context.Context, db platform.DBTX, entityID int64) ([]VATRate, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, code, label, rate_bps, vat_account_id
		FROM ferp_vat_rates WHERE entity_id=$1 ORDER BY rate_bps, id`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VATRate
	for rows.Next() {
		var v VATRate
		var acct *int64
		if err := rows.Scan(&v.ID, &v.EntityID, &v.Code, &v.Label, &v.RateBps, &acct); err != nil {
			return nil, err
		}
		if acct != nil {
			v.VATAccountID = *acct
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// VATReturn computes the VAT position from posted tagged lines.
func (s *PGStore) VATReturn(ctx context.Context, db platform.DBTX, entityID int64, from, to time.Time) (VATReturn, error) {
	ret := VATReturn{From: from, To: to}
	rows, err := db.Query(ctx, `SELECT l.vat_rate_bps,
			SUM(CASE WHEN a.type='liability' THEN l.credit ELSE 0 END),
			SUM(CASE WHEN a.type='liability' THEN l.debit ELSE 0 END),
			SUM(CASE WHEN a.type='revenue' THEN l.credit - l.debit
			         WHEN a.type='expense' THEN l.debit - l.credit ELSE 0 END)
		FROM ferp_entry_lines l
		JOIN ferp_entries e ON e.id=l.entry_id
		JOIN ferp_accounts a ON a.id=l.account_id
		WHERE e.entity_id=$1 AND e.status=1 AND e.date>=$2 AND e.date<=$3 AND l.vat_rate_bps<>0
		GROUP BY l.vat_rate_bps ORDER BY l.vat_rate_bps`, entityID, from, to)
	if err != nil {
		return ret, err
	}
	defer rows.Close()
	for rows.Next() {
		var b VATBucket
		if err := rows.Scan(&b.RateBps, &b.Collected, &b.Deductible, &b.Base); err != nil {
			return ret, err
		}
		ret.Buckets = append(ret.Buckets, b)
		ret.TotalCollected += b.Collected
		ret.TotalDeductible += b.Deductible
	}
	if err := rows.Err(); err != nil {
		return ret, err
	}
	ret.NetDue = ret.TotalCollected - ret.TotalDeductible
	return ret, nil
}

// CreateCharge records a social/fiscal charge.
func (s *PGStore) CreateCharge(ctx context.Context, db platform.DBTX, c *TaxCharge) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_tax_charges (entity_id, label, kind, amount, due_date)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		c.EntityID, c.Label, c.Kind, c.Amount, c.DueDate).Scan(&c.ID)
}

// ListCharges lists an entity's charges in due order.
func (s *PGStore) ListCharges(ctx context.Context, db platform.DBTX, entityID int64) ([]TaxCharge, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, label, kind, amount, due_date, paid, paid_at
		FROM ferp_tax_charges WHERE entity_id=$1 ORDER BY due_date, id`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCharges(rows)
}

// ChargesDue lists unpaid charges due at or before asOf.
func (s *PGStore) ChargesDue(ctx context.Context, db platform.DBTX, entityID int64, asOf time.Time) ([]TaxCharge, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, label, kind, amount, due_date, paid, paid_at
		FROM ferp_tax_charges WHERE entity_id=$1 AND paid=FALSE AND due_date<=$2 ORDER BY due_date, id`,
		entityID, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCharges(rows)
}

type chargeRowScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanCharges(rows chargeRowScanner) ([]TaxCharge, error) {
	var out []TaxCharge
	for rows.Next() {
		var c TaxCharge
		if err := rows.Scan(&c.ID, &c.EntityID, &c.Label, &c.Kind, &c.Amount,
			&c.DueDate, &c.Paid, &c.PaidAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkChargePaid settles a charge (idempotent guard: already-paid conflicts).
func (s *PGStore) MarkChargePaid(ctx context.Context, db platform.DBTX, entityID, chargeID int64, at time.Time) error {
	tag, err := db.Exec(ctx, `UPDATE ferp_tax_charges SET paid=TRUE, paid_at=$1
		WHERE id=$2 AND entity_id=$3 AND paid=FALSE`, at, chargeID, entityID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		_ = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ferp_tax_charges WHERE id=$1 AND entity_id=$2)`,
			chargeID, entityID).Scan(&exists)
		if exists {
			return fmt.Errorf("finance: charge already paid: %w", platform.ErrConflict)
		}
		return fmt.Errorf("finance: charge %d: %w", chargeID, platform.ErrNotFound)
	}
	return nil
}

// ---------- memory implementations ----------

func (m *MemoryStore) ImportTransactions(_ context.Context, _ platform.DBTX, entityID, accountID int64, lines []ImportLine) (imported, skipped int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	for _, t := range m.txs {
		if t.EntityID == entityID && t.AccountID == accountID && t.BankRef != "" {
			seen[t.BankRef] = true
		}
	}
	for _, l := range lines {
		if l.Amount == 0 {
			return imported, skipped, fmt.Errorf("finance: zero-amount import line %q: %w", l.Ref, platform.ErrValidation)
		}
		if l.Ref != "" && seen[l.Ref] {
			skipped++
			continue
		}
		m.seq++
		m.txs[m.seq] = BankTransaction{ID: m.seq, EntityID: entityID, AccountID: accountID,
			Amount: l.Amount, Label: l.Label, BankRef: l.Ref, ValueDate: l.ValueDate}
		if l.Ref != "" {
			seen[l.Ref] = true
		}
		imported++
	}
	return imported, skipped, nil
}

func (m *MemoryStore) BankTransactions(_ context.Context, _ platform.DBTX, accountID int64) ([]BankTransaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []BankTransaction
	for _, t := range m.txs {
		if t.AccountID == accountID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ValueDate.Equal(out[j].ValueDate) {
			return out[i].ID < out[j].ID
		}
		return out[i].ValueDate.Before(out[j].ValueDate)
	})
	return out, nil
}

func (m *MemoryStore) BankStatement(ctx context.Context, db platform.DBTX, accountID int64) ([]StatementLine, error) {
	txs, err := m.BankTransactions(ctx, db, accountID)
	if err != nil {
		return nil, err
	}
	return ApplyRunningBalance(txs), nil
}

func (m *MemoryStore) Transfer(_ context.Context, _ platform.DBTX, c TransferCmd) error {
	if err := c.validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	from, ok1 := m.banks[c.FromAccountID]
	to, ok2 := m.banks[c.ToAccountID]
	if !ok1 || !ok2 {
		return fmt.Errorf("finance: bank account missing: %w", platform.ErrNotFound)
	}
	_ = from
	_ = to
	date := c.ValueDate
	if date.IsZero() {
		date = time.Now().UTC()
	}
	outRef, inRef := c.Ref, c.Ref
	if outRef != "" {
		outRef += "/out"
		inRef += "/in"
	}
	m.seq++
	m.txs[m.seq] = BankTransaction{ID: m.seq, EntityID: c.EntityID, AccountID: c.FromAccountID,
		Amount: -c.Amount, Label: c.Label, BankRef: outRef, ValueDate: date}
	m.seq++
	m.txs[m.seq] = BankTransaction{ID: m.seq, EntityID: c.EntityID, AccountID: c.ToAccountID,
		Amount: c.Amount, Label: c.Label, BankRef: inRef, ValueDate: date}
	return nil
}

func (m *MemoryStore) FiscalYearByID(_ context.Context, _ platform.DBTX, entityID, yearID int64) (FiscalYear, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, y := range m.years {
		if y.ID == yearID && y.EntityID == entityID {
			return y, nil
		}
	}
	return FiscalYear{}, fmt.Errorf("finance: fiscal year %d: %w", yearID, platform.ErrNotFound)
}

func (m *MemoryStore) ListFiscalYears(_ context.Context, _ platform.DBTX, entityID int64) ([]FiscalYear, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []FiscalYear
	for _, y := range m.years {
		if y.EntityID == entityID {
			out = append(out, y)
		}
	}
	return out, nil
}

func (m *MemoryStore) SetFiscalYearLocked(_ context.Context, _ platform.DBTX, entityID, yearID int64, locked bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, y := range m.years {
		if y.ID == yearID && y.EntityID == entityID {
			m.years[i].Locked = locked
			return nil
		}
	}
	return fmt.Errorf("finance: fiscal year %d: %w", yearID, platform.ErrNotFound)
}

func (m *MemoryStore) LedgerBook(_ context.Context, _ platform.DBTX, entityID int64, from, to time.Time) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Entry
	for _, e := range m.entries {
		if e.EntityID != entityID || e.Date.Before(from) || e.Date.After(to) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date.Equal(out[j].Date) {
			return out[i].ID < out[j].ID
		}
		return out[i].Date.Before(out[j].Date)
	})
	return out, nil
}

func (m *MemoryStore) AccountLedger(_ context.Context, _ platform.DBTX, entityID, accountID int64, from, to time.Time) (AccountDrilldown, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var mine []Entry
	for _, e := range m.entries {
		if e.EntityID == entityID {
			mine = append(mine, e)
		}
	}
	sort.Slice(mine, func(i, j int) bool {
		if mine[i].Date.Equal(mine[j].Date) {
			return mine[i].ID < mine[j].ID
		}
		return mine[i].Date.Before(mine[j].Date)
	})
	return DrillAccount(mine, accountID, from, to), nil
}

func (m *MemoryStore) CreateTaxPeriod(_ context.Context, _ platform.DBTX, p *TaxPeriod) error {
	if p.Status == "" {
		p.Status = TaxPeriodOpen
	}
	if err := p.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	p.ID = m.seq
	m.periods[p.ID] = *p
	return nil
}

func (m *MemoryStore) ListTaxPeriods(_ context.Context, _ platform.DBTX, entityID int64) ([]TaxPeriod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []TaxPeriod
	for _, p := range m.periods {
		if p.EntityID == entityID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

func (m *MemoryStore) SetTaxPeriodStatus(_ context.Context, _ platform.DBTX, entityID, periodID int64, st TaxPeriodStatus) error {
	switch st {
	case TaxPeriodOpen, TaxPeriodFiled, TaxPeriodPaid:
	default:
		return fmt.Errorf("finance: bad tax period status %q: %w", st, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.periods[periodID]
	if !ok || p.EntityID != entityID {
		return fmt.Errorf("finance: tax period %d: %w", periodID, platform.ErrNotFound)
	}
	p.Status = st
	m.periods[periodID] = p
	return nil
}

func (m *MemoryStore) CreateVATRate(_ context.Context, _ platform.DBTX, v *VATRate) error {
	if err := v.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	v.ID = m.seq
	m.rates[v.ID] = *v
	return nil
}

func (m *MemoryStore) ListVATRates(_ context.Context, _ platform.DBTX, entityID int64) ([]VATRate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []VATRate
	for _, v := range m.rates {
		if v.EntityID == entityID {
			out = append(out, v)
		}
	}
	return out, nil
}

func (m *MemoryStore) VATReturn(_ context.Context, _ platform.DBTX, entityID int64, from, to time.Time) (VATReturn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var entries []Entry
	for _, e := range m.entries {
		if e.EntityID == entityID {
			entries = append(entries, e)
		}
	}
	var accounts []Account
	for _, a := range m.accts {
		if a.EntityID == entityID {
			accounts = append(accounts, a)
		}
	}
	return ComputeVATReturn(entries, accounts, from, to), nil
}

func (m *MemoryStore) CreateCharge(_ context.Context, _ platform.DBTX, c *TaxCharge) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	c.ID = m.seq
	m.charges[c.ID] = *c
	return nil
}

func (m *MemoryStore) ListCharges(_ context.Context, _ platform.DBTX, entityID int64) ([]TaxCharge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []TaxCharge
	for _, c := range m.charges {
		if c.EntityID == entityID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DueDate.Before(out[j].DueDate) })
	return out, nil
}

func (m *MemoryStore) ChargesDue(_ context.Context, _ platform.DBTX, entityID int64, asOf time.Time) ([]TaxCharge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []TaxCharge
	for _, c := range m.charges {
		if c.EntityID == entityID && !c.Paid && !c.DueDate.After(asOf) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DueDate.Before(out[j].DueDate) })
	return out, nil
}

func (m *MemoryStore) MarkChargePaid(_ context.Context, _ platform.DBTX, entityID, chargeID int64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.charges[chargeID]
	if !ok || c.EntityID != entityID {
		return fmt.Errorf("finance: charge %d: %w", chargeID, platform.ErrNotFound)
	}
	if c.Paid {
		return fmt.Errorf("finance: charge already paid: %w", platform.ErrConflict)
	}
	c.Paid = true
	c.PaidAt = &at
	m.charges[chargeID] = c
	return nil
}
