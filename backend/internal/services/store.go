package services

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Store is the persistence contract for the services context.
type Store interface {
	CreateProject(ctx context.Context, p *Project) error
	ProjectByID(ctx context.Context, id int64) (Project, error)
	ListProjects(ctx context.Context, entityID int64, limit, offset int) ([]Project, error)
	SetProjectStatus(ctx context.Context, id int64, to ProjectStatus, rowVersion int64) (Project, error)
	CreateTask(ctx context.Context, t *Task) error
	TasksOf(ctx context.Context, projectID int64) ([]Task, error)
	SetTaskStatus(ctx context.Context, id int64, to TaskStatus, rowVersion int64) (Task, error)
	AddTime(ctx context.Context, e *TimeEntry) error
	TaskHours(ctx context.Context, taskID int64) (int64, error)
	ProjectHours(ctx context.Context, projectID int64) (int64, error)
	CreateContract(ctx context.Context, c *ServiceContract) error
	SetContractStatus(ctx context.Context, id int64, to ContractStatus, rowVersion int64) (ServiceContract, error)
	ContractsOfOrg(ctx context.Context, orgID int64) ([]ServiceContract, error)
	ListContracts(ctx context.Context, entityID int64, limit, offset int) ([]ServiceContract, error)
	CreateIntervention(ctx context.Context, i *Intervention) error
	SetInterventionStatus(ctx context.Context, id int64, to InterventionStatus, rowVersion int64) (Intervention, error)
	ListInterventions(ctx context.Context, entityID int64, limit, offset int) ([]Intervention, error)
	CreateTicket(ctx context.Context, t *Ticket) error
	TicketByID(ctx context.Context, id int64) (Ticket, error)
	ListTickets(ctx context.Context, entityID int64, limit, offset int) ([]Ticket, error)
	SetTicketStatus(ctx context.Context, id int64, to TicketStatus, rowVersion int64) (Ticket, error)
	AddMessage(ctx context.Context, m *TicketMessage) error
	MessagesOf(ctx context.Context, ticketID int64) ([]TicketMessage, error)
}

// PGStore implements Store against PostgreSQL.
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const projectCols = `id, entity_id, ref, label, description, org_id, status, created_at, updated_at, created_by, updated_by, row_version`

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.EntityID, &p.Ref, &p.Label, &p.Description, &p.OrgID,
		&p.Status, &p.CreatedAt, &p.UpdatedAt, &p.CreatedBy, &p.UpdatedBy, &p.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, identity.ErrNotFound
	}
	return p, err
}

func (s *PGStore) CreateProject(ctx context.Context, p *Project) error {
	if err := p.Validate(); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_projects
		(entity_id, ref, label, description, org_id, status, created_by, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, row_version`,
		p.EntityID, p.Ref, p.Label, p.Description, p.OrgID, p.Status, p.CreatedBy, p.UpdatedBy,
	).Scan(&p.ID, &p.RowVersion)
}

func (s *PGStore) ProjectByID(ctx context.Context, id int64) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx, `SELECT `+projectCols+` FROM ferp_projects WHERE id=$1`, id))
}

func (s *PGStore) ListProjects(ctx context.Context, entityID int64, limit, offset int) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+projectCols+` FROM ferp_projects
		WHERE entity_id=$1 ORDER BY ref LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PGStore) SetProjectStatus(ctx context.Context, id int64, to ProjectStatus, rowVersion int64) (Project, error) {
	p, err := s.ProjectByID(ctx, id)
	if err != nil {
		return Project{}, err
	}
	if p.RowVersion != rowVersion {
		return Project{}, identity.ErrVersionConflict
	}
	if !p.CanTransition(to) {
		return Project{}, errors.New("services: illegal project transition")
	}
	p.Status = to
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_projects SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return Project{}, err
	}
	if tag.RowsAffected() == 0 {
		return Project{}, identity.ErrVersionConflict
	}
	p.RowVersion++
	return p, nil
}

const taskCols = `id, entity_id, project_id, label, description, status, assignee, created_at, updated_at, row_version`

func scanTask(row pgx.Row) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.EntityID, &t.ProjectID, &t.Label, &t.Description,
		&t.Status, &t.Assignee, &t.CreatedAt, &t.UpdatedAt, &t.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, identity.ErrNotFound
	}
	return t, err
}

// projectOpenForWork reports whether tasks/time may be added (not closed/canceled).
func projectOpenForWork(status ProjectStatus) bool {
	return status == ProjectDraft || status == ProjectActive || status == ProjectOnHold
}

func (s *PGStore) CreateTask(ctx context.Context, t *Task) error {
	if err := t.Validate(); err != nil {
		return err
	}
	p, err := s.ProjectByID(ctx, t.ProjectID)
	if err != nil {
		return err
	}
	if !projectOpenForWork(p.Status) {
		return errors.New("services: project closed for new tasks")
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_project_tasks
		(entity_id, project_id, label, description, status, assignee)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, row_version`,
		t.EntityID, t.ProjectID, t.Label, t.Description, t.Status, t.Assignee,
	).Scan(&t.ID, &t.RowVersion)
}

func (s *PGStore) TasksOf(ctx context.Context, projectID int64) ([]Task, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+taskCols+` FROM ferp_project_tasks WHERE project_id=$1 ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PGStore) taskByID(ctx context.Context, id int64) (Task, error) {
	return scanTask(s.pool.QueryRow(ctx, `SELECT `+taskCols+` FROM ferp_project_tasks WHERE id=$1`, id))
}

func (s *PGStore) SetTaskStatus(ctx context.Context, id int64, to TaskStatus, rowVersion int64) (Task, error) {
	t, err := s.taskByID(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if t.RowVersion != rowVersion {
		return Task{}, identity.ErrVersionConflict
	}
	if !t.CanTransition(to) {
		return Task{}, errors.New("services: illegal task transition")
	}
	t.Status = to
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_project_tasks SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return Task{}, err
	}
	if tag.RowsAffected() == 0 {
		return Task{}, identity.ErrVersionConflict
	}
	t.RowVersion++
	return t, nil
}

func (s *PGStore) AddTime(ctx context.Context, e *TimeEntry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	t, err := s.taskByID(ctx, e.TaskID)
	if err != nil {
		return err
	}
	if t.Status == TaskDone || t.Status == TaskCanceled {
		return errors.New("services: task closed for time entries")
	}
	p, err := s.ProjectByID(ctx, e.ProjectID)
	if err != nil {
		return err
	}
	if !projectOpenForWork(p.Status) {
		return errors.New("services: project closed for time entries")
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_time_entries
		(entity_id, project_id, task_id, author, hours, entry_date, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, created_at`,
		e.EntityID, e.ProjectID, e.TaskID, e.Author, e.Hours, e.EntryDate, e.Note,
	).Scan(&e.ID, &e.CreatedAt)
}

func (s *PGStore) TaskHours(ctx context.Context, taskID int64) (int64, error) {
	var sum *int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(hours),0) FROM ferp_time_entries WHERE task_id=$1`, taskID).Scan(&sum)
	if err != nil {
		return 0, err
	}
	return *sum, nil
}

func (s *PGStore) ProjectHours(ctx context.Context, projectID int64) (int64, error) {
	var sum *int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(hours),0) FROM ferp_time_entries WHERE project_id=$1`, projectID).Scan(&sum)
	if err != nil {
		return 0, err
	}
	return *sum, nil
}

const contractCols = `id, entity_id, ref, org_id, label, status, start_date, end_date, created_at, updated_at, row_version`

func scanContract(row pgx.Row) (ServiceContract, error) {
	var c ServiceContract
	err := row.Scan(&c.ID, &c.EntityID, &c.Ref, &c.OrgID, &c.Label, &c.Status,
		&c.StartDate, &c.EndDate, &c.CreatedAt, &c.UpdatedAt, &c.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceContract{}, identity.ErrNotFound
	}
	return c, err
}

func (s *PGStore) CreateContract(ctx context.Context, c *ServiceContract) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_service_contracts
		(entity_id, ref, org_id, label, status, start_date, end_date)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, row_version`,
		c.EntityID, c.Ref, c.OrgID, c.Label, c.Status, c.StartDate, c.EndDate,
	).Scan(&c.ID, &c.RowVersion)
}

func (s *PGStore) SetContractStatus(ctx context.Context, id int64, to ContractStatus, rowVersion int64) (ServiceContract, error) {
	var c ServiceContract
	err := s.pool.QueryRow(ctx, `SELECT `+contractCols+` FROM ferp_service_contracts WHERE id=$1`, id).Scan(
		&c.ID, &c.EntityID, &c.Ref, &c.OrgID, &c.Label, &c.Status,
		&c.StartDate, &c.EndDate, &c.CreatedAt, &c.UpdatedAt, &c.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceContract{}, identity.ErrNotFound
	}
	if err != nil {
		return ServiceContract{}, err
	}
	if c.RowVersion != rowVersion {
		return ServiceContract{}, identity.ErrVersionConflict
	}
	if !c.CanTransition(to) {
		return ServiceContract{}, errors.New("services: illegal contract transition")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_service_contracts SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return ServiceContract{}, err
	}
	if tag.RowsAffected() == 0 {
		return ServiceContract{}, identity.ErrVersionConflict
	}
	c.Status = to
	c.RowVersion++
	return c, nil
}

func (s *PGStore) ContractsOfOrg(ctx context.Context, orgID int64) ([]ServiceContract, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+contractCols+` FROM ferp_service_contracts WHERE org_id=$1 ORDER BY ref`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceContract
	for rows.Next() {
		c, err := scanContract(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PGStore) ListContracts(ctx context.Context, entityID int64, limit, offset int) ([]ServiceContract, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+contractCols+` FROM ferp_service_contracts
		WHERE entity_id=$1 ORDER BY ref LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceContract
	for rows.Next() {
		c, err := scanContract(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PGStore) ListInterventions(ctx context.Context, entityID int64, limit, offset int) ([]Intervention, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+interventionCols+` FROM ferp_interventions
		WHERE entity_id=$1 ORDER BY id LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Intervention
	for rows.Next() {
		in, err := scanIntervention(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

const interventionCols = `id, entity_id, ref, org_id, project_id, contract_id, label, description, status, created_at, updated_at, row_version`

func scanIntervention(row pgx.Row) (Intervention, error) {
	var i Intervention
	err := row.Scan(&i.ID, &i.EntityID, &i.Ref, &i.OrgID, &i.ProjectID, &i.ContractID,
		&i.Label, &i.Description, &i.Status, &i.CreatedAt, &i.UpdatedAt, &i.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Intervention{}, identity.ErrNotFound
	}
	return i, err
}

func (s *PGStore) CreateIntervention(ctx context.Context, in *Intervention) error {
	if err := in.Validate(); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_interventions
		(entity_id, ref, org_id, project_id, contract_id, label, description, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, row_version`,
		in.EntityID, in.Ref, in.OrgID, in.ProjectID, in.ContractID, in.Label, in.Description, in.Status,
	).Scan(&in.ID, &in.RowVersion)
}

func (s *PGStore) SetInterventionStatus(ctx context.Context, id int64, to InterventionStatus, rowVersion int64) (Intervention, error) {
	in, err := scanIntervention(s.pool.QueryRow(ctx, `SELECT `+interventionCols+` FROM ferp_interventions WHERE id=$1`, id))
	if err != nil {
		return Intervention{}, err
	}
	if in.RowVersion != rowVersion {
		return Intervention{}, identity.ErrVersionConflict
	}
	if !in.CanTransition(to) {
		return Intervention{}, errors.New("services: illegal intervention transition")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_interventions SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return Intervention{}, err
	}
	if tag.RowsAffected() == 0 {
		return Intervention{}, identity.ErrVersionConflict
	}
	in.Status = to
	in.RowVersion++
	return in, nil
}

const ticketCols = `id, entity_id, ref, org_id, project_id, subject, priority, status, created_at, updated_at, row_version`

func scanTicket(row pgx.Row) (Ticket, error) {
	var t Ticket
	err := row.Scan(&t.ID, &t.EntityID, &t.Ref, &t.OrgID, &t.ProjectID, &t.Subject,
		&t.Priority, &t.Status, &t.CreatedAt, &t.UpdatedAt, &t.RowVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return Ticket{}, identity.ErrNotFound
	}
	return t, err
}

func (s *PGStore) CreateTicket(ctx context.Context, t *Ticket) error {
	if err := t.Validate(); err != nil {
		return err
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_tickets
		(entity_id, ref, org_id, project_id, subject, priority, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, row_version`,
		t.EntityID, t.Ref, t.OrgID, t.ProjectID, t.Subject, t.Priority, t.Status,
	).Scan(&t.ID, &t.RowVersion)
}

func (s *PGStore) TicketByID(ctx context.Context, id int64) (Ticket, error) {
	return scanTicket(s.pool.QueryRow(ctx, `SELECT `+ticketCols+` FROM ferp_tickets WHERE id=$1`, id))
}

func (s *PGStore) ListTickets(ctx context.Context, entityID int64, limit, offset int) ([]Ticket, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ticketCols+` FROM ferp_tickets
		WHERE entity_id=$1 ORDER BY id LIMIT $2 OFFSET $3`, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ticket
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PGStore) SetTicketStatus(ctx context.Context, id int64, to TicketStatus, rowVersion int64) (Ticket, error) {
	t, err := s.TicketByID(ctx, id)
	if err != nil {
		return Ticket{}, err
	}
	if t.RowVersion != rowVersion {
		return Ticket{}, identity.ErrVersionConflict
	}
	if !t.CanTransition(to) {
		return Ticket{}, errors.New("services: illegal ticket transition")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE ferp_tickets SET status=$1, updated_at=now(), row_version=row_version+1
		WHERE id=$2 AND row_version=$3`, to, id, rowVersion)
	if err != nil {
		return Ticket{}, err
	}
	if tag.RowsAffected() == 0 {
		return Ticket{}, identity.ErrVersionConflict
	}
	t.Status = to
	t.RowVersion++
	return t, nil
}

func (s *PGStore) AddMessage(ctx context.Context, m *TicketMessage) error {
	if err := m.Validate(); err != nil {
		return err
	}
	t, err := s.TicketByID(ctx, m.TicketID)
	if err != nil {
		return err
	}
	if t.Status == TicketClosed {
		return errors.New("services: ticket closed for new messages")
	}
	return s.pool.QueryRow(ctx, `INSERT INTO ferp_ticket_messages
		(entity_id, ticket_id, author, body, internal)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`,
		m.EntityID, m.TicketID, m.Author, m.Body, m.Internal,
	).Scan(&m.ID, &m.CreatedAt)
}

func (s *PGStore) MessagesOf(ctx context.Context, ticketID int64) ([]TicketMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, entity_id, ticket_id, author, body, internal, created_at
		FROM ferp_ticket_messages WHERE ticket_id=$1 ORDER BY created_at, id`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TicketMessage
	for rows.Next() {
		var m TicketMessage
		if err := rows.Scan(&m.ID, &m.EntityID, &m.TicketID, &m.Author, &m.Body, &m.Internal, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MemoryStore is the in-process fake for handler tests.
type MemoryStore struct {
	mu        sync.Mutex
	seq       int64
	projects  map[int64]Project
	tasks     map[int64]Task
	times     map[int64]TimeEntry
	contracts map[int64]ServiceContract
	intervs   map[int64]Intervention
	tickets   map[int64]Ticket
	messages  map[int64]TicketMessage
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		projects: map[int64]Project{}, tasks: map[int64]Task{}, times: map[int64]TimeEntry{},
		contracts: map[int64]ServiceContract{}, intervs: map[int64]Intervention{},
		tickets: map[int64]Ticket{}, messages: map[int64]TicketMessage{},
	}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) CreateProject(_ context.Context, p *Project) error {
	if err := p.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.projects {
		if e.EntityID == p.EntityID && e.Ref == p.Ref {
			return errors.New("services: duplicate project ref")
		}
	}
	p.ID = m.next()
	p.RowVersion = 1
	m.projects[p.ID] = *p
	return nil
}

func (m *MemoryStore) ProjectByID(_ context.Context, id int64) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[id]
	if !ok {
		return Project{}, identity.ErrNotFound
	}
	return p, nil
}

func (m *MemoryStore) ListProjects(_ context.Context, entityID int64, limit, offset int) ([]Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Project
	for _, p := range m.projects {
		if p.EntityID == entityID {
			out = append(out, p)
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

func (m *MemoryStore) SetProjectStatus(_ context.Context, id int64, to ProjectStatus, rowVersion int64) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[id]
	if !ok {
		return Project{}, identity.ErrNotFound
	}
	if p.RowVersion != rowVersion {
		return Project{}, identity.ErrVersionConflict
	}
	if !p.CanTransition(to) {
		return Project{}, errors.New("services: illegal project transition")
	}
	p.Status = to
	p.RowVersion++
	m.projects[id] = p
	return p, nil
}

func (m *MemoryStore) CreateTask(_ context.Context, t *Task) error {
	if err := t.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[t.ProjectID]
	if !ok {
		return errors.New("services: project not found")
	}
	if !projectOpenForWork(p.Status) {
		return errors.New("services: project closed for new tasks")
	}
	t.ID = m.next()
	t.RowVersion = 1
	m.tasks[t.ID] = *t
	return nil
}

func (m *MemoryStore) TasksOf(_ context.Context, projectID int64) ([]Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Task
	for _, t := range m.tasks {
		if t.ProjectID == projectID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *MemoryStore) SetTaskStatus(_ context.Context, id int64, to TaskStatus, rowVersion int64) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return Task{}, identity.ErrNotFound
	}
	if t.RowVersion != rowVersion {
		return Task{}, identity.ErrVersionConflict
	}
	if !t.CanTransition(to) {
		return Task{}, errors.New("services: illegal task transition")
	}
	t.Status = to
	t.RowVersion++
	m.tasks[id] = t
	return t, nil
}

func (m *MemoryStore) AddTime(_ context.Context, e *TimeEntry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[e.TaskID]
	if !ok {
		return errors.New("services: task not found")
	}
	if t.Status == TaskDone || t.Status == TaskCanceled {
		return errors.New("services: task closed for time entries")
	}
	p, ok := m.projects[e.ProjectID]
	if !ok {
		return errors.New("services: project not found")
	}
	if !projectOpenForWork(p.Status) {
		return errors.New("services: project closed for time entries")
	}
	e.ID = m.next()
	m.times[e.ID] = *e
	return nil
}

func (m *MemoryStore) TaskHours(_ context.Context, taskID int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sum int64
	for _, e := range m.times {
		if e.TaskID == taskID {
			sum += e.Hours
		}
	}
	return sum, nil
}

func (m *MemoryStore) ProjectHours(_ context.Context, projectID int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sum int64
	for _, e := range m.times {
		if e.ProjectID == projectID {
			sum += e.Hours
		}
	}
	return sum, nil
}

func (m *MemoryStore) CreateContract(_ context.Context, c *ServiceContract) error {
	if err := c.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.contracts {
		if e.EntityID == c.EntityID && e.Ref == c.Ref {
			return errors.New("services: duplicate contract ref")
		}
	}
	c.ID = m.next()
	c.RowVersion = 1
	m.contracts[c.ID] = *c
	return nil
}

func (m *MemoryStore) SetContractStatus(_ context.Context, id int64, to ContractStatus, rowVersion int64) (ServiceContract, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.contracts[id]
	if !ok {
		return ServiceContract{}, identity.ErrNotFound
	}
	if c.RowVersion != rowVersion {
		return ServiceContract{}, identity.ErrVersionConflict
	}
	if !c.CanTransition(to) {
		return ServiceContract{}, errors.New("services: illegal contract transition")
	}
	c.Status = to
	c.RowVersion++
	m.contracts[id] = c
	return c, nil
}

func (m *MemoryStore) ContractsOfOrg(_ context.Context, orgID int64) ([]ServiceContract, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ServiceContract
	for _, c := range m.contracts {
		if c.OrgID == orgID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *MemoryStore) ListContracts(_ context.Context, entityID int64, limit, offset int) ([]ServiceContract, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ServiceContract
	for _, c := range m.contracts {
		if c.EntityID == entityID {
			out = append(out, c)
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

func (m *MemoryStore) ListInterventions(_ context.Context, entityID int64, limit, offset int) ([]Intervention, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Intervention
	for _, in := range m.intervs {
		if in.EntityID == entityID {
			out = append(out, in)
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

func (m *MemoryStore) CreateIntervention(_ context.Context, in *Intervention) error {
	if err := in.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.intervs {
		if e.EntityID == in.EntityID && e.Ref == in.Ref {
			return errors.New("services: duplicate intervention ref")
		}
	}
	in.ID = m.next()
	in.RowVersion = 1
	m.intervs[in.ID] = *in
	return nil
}

func (m *MemoryStore) SetInterventionStatus(_ context.Context, id int64, to InterventionStatus, rowVersion int64) (Intervention, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.intervs[id]
	if !ok {
		return Intervention{}, identity.ErrNotFound
	}
	if in.RowVersion != rowVersion {
		return Intervention{}, identity.ErrVersionConflict
	}
	if !in.CanTransition(to) {
		return Intervention{}, errors.New("services: illegal intervention transition")
	}
	in.Status = to
	in.RowVersion++
	m.intervs[id] = in
	return in, nil
}

func (m *MemoryStore) CreateTicket(_ context.Context, t *Ticket) error {
	if err := t.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.tickets {
		if e.EntityID == t.EntityID && e.Ref == t.Ref {
			return errors.New("services: duplicate ticket ref")
		}
	}
	t.ID = m.next()
	t.RowVersion = 1
	m.tickets[t.ID] = *t
	return nil
}

func (m *MemoryStore) TicketByID(_ context.Context, id int64) (Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tickets[id]
	if !ok {
		return Ticket{}, identity.ErrNotFound
	}
	return t, nil
}

func (m *MemoryStore) ListTickets(_ context.Context, entityID int64, limit, offset int) ([]Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Ticket
	for _, t := range m.tickets {
		if t.EntityID == entityID {
			out = append(out, t)
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

func (m *MemoryStore) SetTicketStatus(_ context.Context, id int64, to TicketStatus, rowVersion int64) (Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tickets[id]
	if !ok {
		return Ticket{}, identity.ErrNotFound
	}
	if t.RowVersion != rowVersion {
		return Ticket{}, identity.ErrVersionConflict
	}
	if !t.CanTransition(to) {
		return Ticket{}, errors.New("services: illegal ticket transition")
	}
	t.Status = to
	t.RowVersion++
	m.tickets[id] = t
	return t, nil
}

func (m *MemoryStore) AddMessage(_ context.Context, msg *TicketMessage) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tickets[msg.TicketID]
	if !ok {
		return errors.New("services: ticket not found")
	}
	if t.Status == TicketClosed {
		return errors.New("services: ticket closed for new messages")
	}
	msg.ID = m.next()
	m.messages[msg.ID] = *msg
	return nil
}

func (m *MemoryStore) MessagesOf(_ context.Context, ticketID int64) ([]TicketMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []TicketMessage
	for _, msg := range m.messages {
		if msg.TicketID == ticketID {
			out = append(out, msg)
		}
	}
	return out, nil
}
