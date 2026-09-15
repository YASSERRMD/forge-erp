package hr

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

const payrollRunCols = `id, entity_id, label, period_start, period_end, status, created_at, updated_at, row_version`

func scanPayrollRun(row pgx.Row) (PayrollRun, error) {
	var r PayrollRun
	err := row.Scan(&r.ID, &r.EntityID, &r.Label, &r.PeriodStart, &r.PeriodEnd,
		&r.Status, &r.CreatedAt, &r.UpdatedAt, &r.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return PayrollRun{}, identity.ErrNotFound
	}
	return r, err
}

const payrollLineCols = `id, entity_id, run_id, salary_id, gross, charges, net`

func scanPayrollLine(row pgx.Row) (PayrollRunLine, error) {
	var l PayrollRunLine
	err := row.Scan(&l.ID, &l.EntityID, &l.RunID, &l.SalaryID, &l.Gross, &l.Charges, &l.Net)
	if errors.Is(err, pgx.ErrNoRows) {
		return PayrollRunLine{}, identity.ErrNotFound
	}
	return l, err
}

func (s *PGStore) CreatePayrollRun(ctx context.Context, db platform.DBTX, r *PayrollRun) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_payroll_runs
		(entity_id, label, period_start, period_end, status)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, row_version`,
		r.EntityID, r.Label, r.PeriodStart, r.PeriodEnd, r.Status,
	).Scan(&r.ID, &r.RowVersion)
}

func (s *PGStore) AddPayrollRunLine(ctx context.Context, db platform.DBTX, l *PayrollRunLine) error {
	if err := l.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	var st PayrollRunStatus
	err := db.QueryRow(ctx, `SELECT status FROM ferp_payroll_runs WHERE id=$1 AND entity_id=$2`,
		l.RunID, l.EntityID).Scan(&st)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	if err != nil {
		return err
	}
	if st != PayrollRunDraft {
		return fmt.Errorf("hr: lines editable on draft runs only: %w", platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_payroll_run_lines
		(entity_id, run_id, salary_id, gross, charges, net)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		l.EntityID, l.RunID, l.SalaryID, l.Gross, l.Charges, l.Net).Scan(&l.ID)
}

func (s *PGStore) PayrollRunByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (PayrollRun, error) {
	return scanPayrollRun(db.QueryRow(ctx, `SELECT `+payrollRunCols+` FROM ferp_payroll_runs WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) PayrollRunLines(ctx context.Context, db platform.DBTX, entityID int64, runID int64) ([]PayrollRunLine, error) {
	rows, err := db.Query(ctx, `SELECT `+payrollLineCols+` FROM ferp_payroll_run_lines
		WHERE entity_id=$1 AND run_id=$2 ORDER BY id`, entityID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PayrollRunLine
	for rows.Next() {
		l, err := scanPayrollLine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *PGStore) PayrollRunsOf(ctx context.Context, db platform.DBTX, entityID int64) ([]PayrollRun, error) {
	rows, err := db.Query(ctx, `SELECT `+payrollRunCols+` FROM ferp_payroll_runs
		WHERE entity_id=$1 ORDER BY period_start`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PayrollRun
	for rows.Next() {
		r, err := scanPayrollRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PGStore) SetPayrollRunStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to PayrollRunStatus, rowVersion int64) (PayrollRun, error) {
	r, err := s.PayrollRunByID(ctx, db, entityID, id)
	if err != nil {
		return PayrollRun{}, err
	}
	if r.RowVersion != rowVersion {
		return PayrollRun{}, identity.ErrVersionConflict
	}
	if !r.CanTransition(to) {
		return PayrollRun{}, fmt.Errorf("hr: illegal payroll run transition: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_payroll_runs SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3 AND entity_id=$4`, to, id, rowVersion, entityID)
	if err != nil {
		return PayrollRun{}, err
	}
	if tag.RowsAffected() == 0 {
		return PayrollRun{}, identity.ErrVersionConflict
	}
	r.Status = to
	r.RowVersion++
	return r, nil
}

func (m *MemoryStore) CreatePayrollRun(_ context.Context, _ platform.DBTX, r *PayrollRun) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r.ID = m.next()
	r.RowVersion = 1
	m.runs[r.ID] = *r
	return nil
}

func (m *MemoryStore) AddPayrollRunLine(_ context.Context, _ platform.DBTX, l *PayrollRunLine) error {
	if err := l.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[l.RunID]
	if !ok || r.EntityID != l.EntityID {
		return identity.ErrNotFound
	}
	if r.Status != PayrollRunDraft {
		return fmt.Errorf("hr: lines editable on draft runs only: %w", platform.ErrValidation)
	}
	l.ID = m.next()
	m.runLines[l.ID] = *l
	return nil
}

func (m *MemoryStore) PayrollRunByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (PayrollRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok || r.EntityID != entityID {
		return PayrollRun{}, identity.ErrNotFound
	}
	return r, nil
}

func (m *MemoryStore) PayrollRunLines(_ context.Context, _ platform.DBTX, entityID int64, runID int64) ([]PayrollRunLine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	if !ok || r.EntityID != entityID {
		return nil, identity.ErrNotFound
	}
	var out []PayrollRunLine
	for _, l := range m.runLines {
		if l.RunID == runID && l.EntityID == entityID {
			out = append(out, l)
		}
	}
	return out, nil
}

func (m *MemoryStore) PayrollRunsOf(_ context.Context, _ platform.DBTX, entityID int64) ([]PayrollRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []PayrollRun
	for _, r := range m.runs {
		if r.EntityID == entityID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *MemoryStore) SetPayrollRunStatus(_ context.Context, _ platform.DBTX, entityID int64, id int64, to PayrollRunStatus, rowVersion int64) (PayrollRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok || r.EntityID != entityID {
		return PayrollRun{}, identity.ErrNotFound
	}
	if r.RowVersion != rowVersion {
		return PayrollRun{}, identity.ErrVersionConflict
	}
	if !r.CanTransition(to) {
		return PayrollRun{}, fmt.Errorf("hr: illegal payroll run transition: %w", platform.ErrValidation)
	}
	r.Status = to
	r.RowVersion++
	m.runs[id] = r
	return r, nil
}
