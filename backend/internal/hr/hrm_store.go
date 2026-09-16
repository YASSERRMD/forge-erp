package hr

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

const establishmentCols = `id, entity_id, code, label, created_at, updated_at, row_version`

func scanEstablishment(row pgx.Row) (Establishment, error) {
	var e Establishment
	err := row.Scan(&e.ID, &e.EntityID, &e.Code, &e.Label, &e.CreatedAt, &e.UpdatedAt, &e.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Establishment{}, identity.ErrNotFound
	}
	return e, err
}

func (s *PGStore) CreateEstablishment(ctx context.Context, db platform.DBTX, e *Establishment) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_establishments
		(entity_id, code, label) VALUES ($1,$2,$3) RETURNING id, row_version`,
		e.EntityID, e.Code, e.Label).Scan(&e.ID, &e.RowVersion)
}

func (s *PGStore) EstablishmentsOf(ctx context.Context, db platform.DBTX, entityID int64) ([]Establishment, error) {
	rows, err := db.Query(ctx, `SELECT `+establishmentCols+` FROM ferp_establishments
		WHERE entity_id=$1 ORDER BY code`, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Establishment
	for rows.Next() {
		e, err := scanEstablishment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const employeeCols = `id, entity_id, code, name, establishment_id, job_title, hire_date, status, created_at, updated_at, row_version`

func scanEmployee(row pgx.Row) (Employee, error) {
	var e Employee
	err := row.Scan(&e.ID, &e.EntityID, &e.Code, &e.Name, &e.EstablishmentID,
		&e.JobTitle, &e.HireDate, &e.Status, &e.CreatedAt, &e.UpdatedAt, &e.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Employee{}, identity.ErrNotFound
	}
	return e, err
}

func (s *PGStore) CreateEmployee(ctx context.Context, db platform.DBTX, e *Employee) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM ferp_establishments WHERE id=$1 AND entity_id=$2`,
		e.EstablishmentID, e.EntityID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("hr: unknown establishment: %w", platform.ErrValidation)
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_employees
		(entity_id, code, name, establishment_id, job_title, hire_date, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, row_version`,
		e.EntityID, e.Code, e.Name, e.EstablishmentID, e.JobTitle, e.HireDate, e.Status,
	).Scan(&e.ID, &e.RowVersion)
}

func (s *PGStore) EmployeeByID(ctx context.Context, db platform.DBTX, entityID int64, id int64) (Employee, error) {
	return scanEmployee(db.QueryRow(ctx, `SELECT `+employeeCols+` FROM ferp_employees WHERE id=$1 AND entity_id=$2`, id, entityID))
}

func (s *PGStore) EmployeesOf(ctx context.Context, db platform.DBTX, entityID int64, limit, offset int) ([]Employee, error) {
	rows, err := db.Query(ctx, `SELECT `+employeeCols+` FROM ferp_employees
		WHERE entity_id=$1 ORDER BY code LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Employee
	for rows.Next() {
		e, err := scanEmployee(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *PGStore) SetEmployeeStatus(ctx context.Context, db platform.DBTX, entityID int64, id int64, to EmployeeStatus, rowVersion int64) (Employee, error) {
	e, err := scanEmployee(db.QueryRow(ctx, `SELECT `+employeeCols+` FROM ferp_employees WHERE id=$1 AND entity_id=$2`, id, entityID))
	if err != nil {
		return Employee{}, err
	}
	if e.RowVersion != rowVersion {
		return Employee{}, identity.ErrVersionConflict
	}
	if !e.CanTransition(to) {
		return Employee{}, fmt.Errorf("hr: illegal employee transition: %w", platform.ErrValidation)
	}
	tag, err := db.Exec(ctx, `UPDATE ferp_employees SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3 AND entity_id=$4`, to, id, rowVersion, entityID)
	if err != nil {
		return Employee{}, err
	}
	if tag.RowsAffected() == 0 {
		return Employee{}, identity.ErrVersionConflict
	}
	e.Status = to
	e.RowVersion++
	return e, nil
}

func (s *PGStore) AddSkill(ctx context.Context, db platform.DBTX, sk *EmployeeSkill) error {
	if err := sk.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if _, err := s.EmployeeByID(ctx, db, sk.EntityID, sk.EmployeeID); err != nil {
		return err
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_employee_skills
		(entity_id, employee_id, skill, level) VALUES ($1,$2,$3,$4) RETURNING id`,
		sk.EntityID, sk.EmployeeID, sk.Skill, sk.Level).Scan(&sk.ID)
}

func (s *PGStore) SkillsOf(ctx context.Context, db platform.DBTX, entityID int64, employeeID int64) ([]EmployeeSkill, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, employee_id, skill, level, created_at
		FROM ferp_employee_skills WHERE entity_id=$1 AND employee_id=$2 ORDER BY skill`,
		entityID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EmployeeSkill
	for rows.Next() {
		var sk EmployeeSkill
		if err := rows.Scan(&sk.ID, &sk.EntityID, &sk.EmployeeID, &sk.Skill, &sk.Level, &sk.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

func (s *PGStore) AddEvaluation(ctx context.Context, db platform.DBTX, e *Evaluation) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	if _, err := s.EmployeeByID(ctx, db, e.EntityID, e.EmployeeID); err != nil {
		return err
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_evaluations
		(entity_id, employee_id, period, rating, notes) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		e.EntityID, e.EmployeeID, e.Period, e.Rating, e.Notes).Scan(&e.ID)
}

func (s *PGStore) EvaluationsOf(ctx context.Context, db platform.DBTX, entityID int64, employeeID int64) ([]Evaluation, error) {
	rows, err := db.Query(ctx, `SELECT id, entity_id, employee_id, period, rating, notes, created_at
		FROM ferp_evaluations WHERE entity_id=$1 AND employee_id=$2 ORDER BY period`,
		entityID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Evaluation
	for rows.Next() {
		var e Evaluation
		if err := rows.Scan(&e.ID, &e.EntityID, &e.EmployeeID, &e.Period, &e.Rating, &e.Notes, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- Memory implementations ---

func (m *MemoryStore) CreateEstablishment(_ context.Context, _ platform.DBTX, e *Establishment) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.estabs {
		if x.EntityID == e.EntityID && x.Code == e.Code {
			return fmt.Errorf("hr: duplicate establishment code: %w", platform.ErrConflict)
		}
	}
	e.ID = m.next()
	e.RowVersion = 1
	m.estabs[e.ID] = *e
	return nil
}

func (m *MemoryStore) EstablishmentsOf(_ context.Context, _ platform.DBTX, entityID int64) ([]Establishment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Establishment
	for _, e := range m.estabs {
		if e.EntityID == entityID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *MemoryStore) CreateEmployee(_ context.Context, _ platform.DBTX, e *Employee) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ok := false
	for _, x := range m.estabs {
		if x.ID == e.EstablishmentID && x.EntityID == e.EntityID {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("hr: unknown establishment: %w", platform.ErrValidation)
	}
	for _, x := range m.emps {
		if x.EntityID == e.EntityID && x.Code == e.Code {
			return fmt.Errorf("hr: duplicate employee code: %w", platform.ErrConflict)
		}
	}
	e.ID = m.next()
	e.RowVersion = 1
	m.emps[e.ID] = *e
	return nil
}

func (m *MemoryStore) EmployeeByID(_ context.Context, _ platform.DBTX, entityID int64, id int64) (Employee, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.emps[id]
	if !ok || e.EntityID != entityID {
		return Employee{}, identity.ErrNotFound
	}
	return e, nil
}

func (m *MemoryStore) EmployeesOf(_ context.Context, _ platform.DBTX, entityID int64, limit, offset int) ([]Employee, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Employee
	for _, e := range m.emps {
		if e.EntityID == entityID {
			out = append(out, e)
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

func (m *MemoryStore) SetEmployeeStatus(_ context.Context, _ platform.DBTX, entityID int64, id int64, to EmployeeStatus, rowVersion int64) (Employee, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.emps[id]
	if !ok || e.EntityID != entityID {
		return Employee{}, identity.ErrNotFound
	}
	if e.RowVersion != rowVersion {
		return Employee{}, identity.ErrVersionConflict
	}
	if !e.CanTransition(to) {
		return Employee{}, fmt.Errorf("hr: illegal employee transition: %w", platform.ErrValidation)
	}
	e.Status = to
	e.RowVersion++
	m.emps[id] = e
	return e, nil
}

func (m *MemoryStore) AddSkill(_ context.Context, _ platform.DBTX, s *EmployeeSkill) error {
	if err := s.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.emps[s.EmployeeID]
	if !ok || e.EntityID != s.EntityID {
		return identity.ErrNotFound
	}
	for _, x := range m.skills {
		if x.EntityID == s.EntityID && x.EmployeeID == s.EmployeeID && x.Skill == s.Skill {
			return fmt.Errorf("hr: duplicate skill: %w", platform.ErrConflict)
		}
	}
	s.ID = m.next()
	m.skills[s.ID] = *s
	return nil
}

func (m *MemoryStore) SkillsOf(_ context.Context, _ platform.DBTX, entityID int64, employeeID int64) ([]EmployeeSkill, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EmployeeSkill
	for _, s := range m.skills {
		if s.EntityID == entityID && s.EmployeeID == employeeID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *MemoryStore) AddEvaluation(_ context.Context, _ platform.DBTX, e *Evaluation) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: %w", err, platform.ErrValidation)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	emp, ok := m.emps[e.EmployeeID]
	if !ok || emp.EntityID != e.EntityID {
		return identity.ErrNotFound
	}
	for _, x := range m.evals {
		if x.EntityID == e.EntityID && x.EmployeeID == e.EmployeeID && x.Period == e.Period {
			return fmt.Errorf("hr: duplicate evaluation period: %w", platform.ErrConflict)
		}
	}
	e.ID = m.next()
	m.evals[e.ID] = *e
	return nil
}

func (m *MemoryStore) EvaluationsOf(_ context.Context, _ platform.DBTX, entityID int64, employeeID int64) ([]Evaluation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Evaluation
	for _, e := range m.evals {
		if e.EntityID == entityID && e.EmployeeID == employeeID {
			out = append(out, e)
		}
	}
	return out, nil
}
