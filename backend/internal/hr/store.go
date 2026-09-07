package hr

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Store is the persistence contract for the hr context.
type Store interface {
	CreateLeave(ctx context.Context, l *LeaveRequest) error
	LeavesOf(ctx context.Context, entityID int64, userLogin string, limit, offset int) ([]LeaveRequest, error)
	SetLeaveStatus(ctx context.Context, id int64, to LeaveStatus, rowVersion int64) (LeaveRequest, error)
	CreateExpense(ctx context.Context, r *ExpenseReport) error
	AddExpenseLine(ctx context.Context, l *ExpenseLine) error
	ExpenseTotal(ctx context.Context, reportID int64) (int64, error)
	SetExpenseStatus(ctx context.Context, id int64, to ExpenseStatus, rowVersion int64) (ExpenseReport, error)
	ExpensesOf(ctx context.Context, entityID int64, userLogin string, limit, offset int) ([]ExpenseReport, error)
	CreateSalary(ctx context.Context, s *Salary) error
	SetSalaryStatus(ctx context.Context, id int64, to SalaryStatus, rowVersion int64) (Salary, error)
	SalariesOf(ctx context.Context, entityID int64, userLogin string) ([]Salary, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const leaveCols = `id, entity_id, user_login, type, start_date, end_date, days, status, comment, created_at, updated_at, row_version`

func scanLeave(row pgx.Row) (LeaveRequest, error) {
	var l LeaveRequest
	err := row.Scan(&l.ID, &l.EntityID, &l.UserLogin, &l.Type, &l.StartDate, &l.EndDate,
		&l.Days, &l.Status, &l.Comment, &l.CreatedAt, &l.UpdatedAt, &l.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return LeaveRequest{}, identity.ErrNotFound
	}
	return l, err
}

func (s *PGStore) CreateLeave(ctx context.Context, l *LeaveRequest) error {
	if err := l.Validate(); err != nil {
		return err
	}
	l.Days = LeaveDays(l.StartDate, l.EndDate)
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_leave_requests
		(entity_id, user_login, type, start_date, end_date, days, status, comment)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, row_version`,
		l.EntityID, l.UserLogin, l.Type, l.StartDate, l.EndDate, l.Days, l.Status, l.Comment,
	).Scan(&l.ID, &l.RowVersion)
}

func (s *PGStore) LeavesOf(ctx context.Context, entityID int64, userLogin string, limit, offset int) ([]LeaveRequest, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+leaveCols+` FROM ferp_leave_requests
		WHERE entity_id=$1 AND ($2='' OR user_login=$2) ORDER BY start_date LIMIT $3 OFFSET $4`,
		entityID, userLogin, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LeaveRequest
	for rows.Next() {
		l, err := scanLeave(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *PGStore) SetLeaveStatus(ctx context.Context, id int64, to LeaveStatus, rowVersion int64) (LeaveRequest, error) {
	var l LeaveRequest
	err := s.pool.QueryRow(ctx, `SELECT `+leaveCols+` FROM ferp_leave_requests WHERE id=$1`, id).Scan(
		&l.ID, &l.EntityID, &l.UserLogin, &l.Type, &l.StartDate, &l.EndDate,
		&l.Days, &l.Status, &l.Comment, &l.CreatedAt, &l.UpdatedAt, &l.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return LeaveRequest{}, identity.ErrNotFound
	}
	if err != nil {
		return LeaveRequest{}, err
	}
	if l.RowVersion != rowVersion {
		return LeaveRequest{}, identity.ErrVersionConflict
	}
	if !l.CanTransition(to) {
		return LeaveRequest{}, errors.New("hr: illegal leave transition")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_leave_requests SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return LeaveRequest{}, err
	}
	if tag.RowsAffected() == 0 {
		return LeaveRequest{}, identity.ErrVersionConflict
	}
	l.Status = to
	l.RowVersion++
	return l, nil
}

const expenseCols = `id, entity_id, ref, user_login, status, total, created_at, updated_at, row_version`

func scanExpense(row pgx.Row) (ExpenseReport, error) {
	var r ExpenseReport
	err := row.Scan(&r.ID, &r.EntityID, &r.Ref, &r.UserLogin, &r.Status, &r.Total,
		&r.CreatedAt, &r.UpdatedAt, &r.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExpenseReport{}, identity.ErrNotFound
	}
	return r, err
}

func (s *PGStore) CreateExpense(ctx context.Context, r *ExpenseReport) error {
	if err := r.Validate(); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_expense_reports
		(entity_id, ref, user_login, status) VALUES ($1,$2,$3,$4) RETURNING id, total, row_version`,
		r.EntityID, r.Ref, r.UserLogin, r.Status,
	).Scan(&r.ID, &r.Total, &r.RowVersion)
}

func (s *PGStore) AddExpenseLine(ctx context.Context, l *ExpenseLine) error {
	if err := l.Validate(); err != nil {
		return err
	}
	var st ExpenseStatus
	err := s.pool.QueryRow(ctx, `SELECT status FROM ferp_expense_reports WHERE id=$1`, l.ReportID).Scan(&st)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	if err != nil {
		return err
	}
	if st != ExpenseDraft {
		return errors.New("hr: lines editable on draft reports only")
	}
	if err := s.pool.QueryRow(ctx, `INSERT INTO ferp_expense_lines
		(entity_id, report_id, date, label, amount, vat_bps)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		l.EntityID, l.ReportID, l.Date, l.Label, l.Amount, l.VATBps).Scan(&l.ID); err != nil {
		return err
	}
	var total int64
	if err := s.pool.QueryRow(ctx, `UPDATE ferp_expense_reports SET total=(
		SELECT COALESCE(SUM(amount),0) FROM ferp_expense_lines WHERE report_id=$1
	), updated_at=now() WHERE id=$1 RETURNING total`, l.ReportID).Scan(&total); err != nil {
		return err
	}
	return nil
}

func (s *PGStore) ExpenseTotal(ctx context.Context, reportID int64) (int64, error) {
	var total int64
	err := s.pool.QueryRow(ctx, `SELECT total FROM ferp_expense_reports WHERE id=$1`, reportID).Scan(&total)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, identity.ErrNotFound
	}
	return total, err
}

func (s *PGStore) SetExpenseStatus(ctx context.Context, id int64, to ExpenseStatus, rowVersion int64) (ExpenseReport, error) {
	r, err := scanExpense(s.pool.QueryRow(ctx, `SELECT `+expenseCols+` FROM ferp_expense_reports WHERE id=$1`, id))
	if err != nil {
		return ExpenseReport{}, err
	}
	if r.RowVersion != rowVersion {
		return ExpenseReport{}, identity.ErrVersionConflict
	}
	if !r.CanTransition(to) {
		return ExpenseReport{}, errors.New("hr: illegal expense transition")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_expense_reports SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return ExpenseReport{}, err
	}
	if tag.RowsAffected() == 0 {
		return ExpenseReport{}, identity.ErrVersionConflict
	}
	r.Status = to
	r.RowVersion++
	return r, nil
}

func (s *PGStore) ExpensesOf(ctx context.Context, entityID int64, userLogin string, limit, offset int) ([]ExpenseReport, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+expenseCols+` FROM ferp_expense_reports
		WHERE entity_id=$1 AND ($2='' OR user_login=$2) ORDER BY id LIMIT $3 OFFSET $4`,
		entityID, userLogin, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExpenseReport
	for rows.Next() {
		r, err := scanExpense(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const salaryCols = `id, entity_id, user_login, period, gross, charges, net, status, created_at, updated_at, row_version`

func scanSalary(row pgx.Row) (Salary, error) {
	var sal Salary
	err := row.Scan(&sal.ID, &sal.EntityID, &sal.UserLogin, &sal.Period, &sal.Gross,
		&sal.Charges, &sal.Net, &sal.Status, &sal.CreatedAt, &sal.UpdatedAt, &sal.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Salary{}, identity.ErrNotFound
	}
	return sal, err
}

func (s *PGStore) CreateSalary(ctx context.Context, sal *Salary) error {
	if err := sal.Validate(); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_salaries
		(entity_id, user_login, period, gross, charges, net, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, row_version`,
		sal.EntityID, sal.UserLogin, sal.Period, sal.Gross, sal.Charges, sal.Net, sal.Status,
	).Scan(&sal.ID, &sal.RowVersion)
}

func (s *PGStore) SetSalaryStatus(ctx context.Context, id int64, to SalaryStatus, rowVersion int64) (Salary, error) {
	sal, err := scanSalary(s.pool.QueryRow(ctx, `SELECT `+salaryCols+` FROM ferp_salaries WHERE id=$1`, id))
	if err != nil {
		return Salary{}, err
	}
	if sal.RowVersion != rowVersion {
		return Salary{}, identity.ErrVersionConflict
	}
	if !sal.CanTransition(to) {
		return Salary{}, errors.New("hr: illegal salary transition")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_salaries SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return Salary{}, err
	}
	if tag.RowsAffected() == 0 {
		return Salary{}, identity.ErrVersionConflict
	}
	sal.Status = to
	sal.RowVersion++
	return sal, nil
}

func (s *PGStore) SalariesOf(ctx context.Context, entityID int64, userLogin string) ([]Salary, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+salaryCols+` FROM ferp_salaries
		WHERE entity_id=$1 AND ($2='' OR user_login=$2) ORDER BY period`, entityID, userLogin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Salary
	for rows.Next() {
		sal, err := scanSalary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sal)
	}
	return out, rows.Err()
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu       sync.Mutex
	seq      int64
	leaves   map[int64]LeaveRequest
	expenses map[int64]ExpenseReport
	lines    map[int64]ExpenseLine
	salaries map[int64]Salary
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		leaves: map[int64]LeaveRequest{}, expenses: map[int64]ExpenseReport{},
		lines: map[int64]ExpenseLine{}, salaries: map[int64]Salary{},
	}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateLeave(_ context.Context, l *LeaveRequest) error {
	if err := l.Validate(); err != nil {
		return err
	}
	l.Days = LeaveDays(l.StartDate, l.EndDate)
	m.mu.Lock()
	defer m.mu.Unlock()
	l.ID = m.next()
	l.RowVersion = 1
	m.leaves[l.ID] = *l
	return nil
}

func (m *MemoryStore) LeavesOf(_ context.Context, entityID int64, userLogin string, limit, offset int) ([]LeaveRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []LeaveRequest
	for _, l := range m.leaves {
		if l.EntityID == entityID && (userLogin == "" || l.UserLogin == userLogin) {
			out = append(out, l)
		}
	}
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) SetLeaveStatus(_ context.Context, id int64, to LeaveStatus, rowVersion int64) (LeaveRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leaves[id]
	if !ok {
		return LeaveRequest{}, identity.ErrNotFound
	}
	if l.RowVersion != rowVersion {
		return LeaveRequest{}, identity.ErrVersionConflict
	}
	if !l.CanTransition(to) {
		return LeaveRequest{}, errors.New("hr: illegal leave transition")
	}
	l.Status = to
	l.RowVersion++
	m.leaves[id] = l
	return l, nil
}

func (m *MemoryStore) CreateExpense(_ context.Context, r *ExpenseReport) error {
	if err := r.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.expenses {
		if e.EntityID == r.EntityID && e.Ref == r.Ref {
			return errors.New("hr: duplicate expense ref")
		}
	}
	r.ID = m.next()
	r.RowVersion = 1
	m.expenses[r.ID] = *r
	return nil
}

func (m *MemoryStore) AddExpenseLine(_ context.Context, l *ExpenseLine) error {
	if err := l.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.expenses[l.ReportID]
	if !ok {
		return identity.ErrNotFound
	}
	if r.Status != ExpenseDraft {
		return errors.New("hr: lines editable on draft reports only")
	}
	l.ID = m.next()
	m.lines[l.ID] = *l
	var total int64
	for _, e := range m.lines {
		if e.ReportID == l.ReportID {
			total += e.Amount
		}
	}
	r.Total = total
	m.expenses[l.ReportID] = r
	return nil
}

func (m *MemoryStore) ExpenseTotal(_ context.Context, reportID int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.expenses[reportID]
	if !ok {
		return 0, identity.ErrNotFound
	}
	return r.Total, nil
}

func (m *MemoryStore) SetExpenseStatus(_ context.Context, id int64, to ExpenseStatus, rowVersion int64) (ExpenseReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.expenses[id]
	if !ok {
		return ExpenseReport{}, identity.ErrNotFound
	}
	if r.RowVersion != rowVersion {
		return ExpenseReport{}, identity.ErrVersionConflict
	}
	if !r.CanTransition(to) {
		return ExpenseReport{}, errors.New("hr: illegal expense transition")
	}
	r.Status = to
	r.RowVersion++
	m.expenses[id] = r
	return r, nil
}

func (m *MemoryStore) ExpensesOf(_ context.Context, entityID int64, userLogin string, limit, offset int) ([]ExpenseReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ExpenseReport
	for _, r := range m.expenses {
		if r.EntityID == entityID && (userLogin == "" || r.UserLogin == userLogin) {
			out = append(out, r)
		}
	}
	if offset > len(out) {
		return nil, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) CreateSalary(_ context.Context, s *Salary) error {
	if err := s.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.salaries {
		if e.EntityID == s.EntityID && e.UserLogin == s.UserLogin && e.Period == s.Period {
			return errors.New("hr: duplicate salary period")
		}
	}
	s.ID = m.next()
	s.RowVersion = 1
	m.salaries[s.ID] = *s
	return nil
}

func (m *MemoryStore) SetSalaryStatus(_ context.Context, id int64, to SalaryStatus, rowVersion int64) (Salary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.salaries[id]
	if !ok {
		return Salary{}, identity.ErrNotFound
	}
	if s.RowVersion != rowVersion {
		return Salary{}, identity.ErrVersionConflict
	}
	if !s.CanTransition(to) {
		return Salary{}, errors.New("hr: illegal salary transition")
	}
	s.Status = to
	s.RowVersion++
	m.salaries[id] = s
	return s, nil
}

func (m *MemoryStore) SalariesOf(_ context.Context, entityID int64, userLogin string) ([]Salary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Salary
	for _, s := range m.salaries {
		if s.EntityID == entityID && (userLogin == "" || s.UserLogin == userLogin) {
			out = append(out, s)
		}
	}
	return out, nil
}
