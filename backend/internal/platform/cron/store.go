package cron

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Store is the persistence contract for jobs and their run history.
type Store interface {
	UpsertJob(ctx context.Context, db platform.DBTX, j *Job) error
	JobByCode(ctx context.Context, db platform.DBTX, entityID int64, code string) (Job, error)
	DueJobs(ctx context.Context, db platform.DBTX, now time.Time, limit int) ([]Job, error)
	MarkNextRun(ctx context.Context, db platform.DBTX, entityID, id int64, next time.Time, status string) error
	StartRun(ctx context.Context, db platform.DBTX, jobID int64, at time.Time) (Run, error)
	FinishRun(ctx context.Context, db platform.DBTX, entityID, runID int64, status, detail string, at time.Time) error
	RunsOf(ctx context.Context, db platform.DBTX, jobID int64) ([]Run, error)
	DeleteRuns(ctx context.Context, db platform.DBTX, runIDs []int64) error
}

// PGStore implements Store against PostgreSQL (ferp_jobs + ferp_job_runs).
type PGStore struct{ pool *pgxpool.Pool }

// NewPGStore wraps a pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const jobCols = `id, entity_id, code, interval_s, cron_expr, next_run_at, last_status, enabled`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.EntityID, &j.Code, &j.IntervalS, &j.CronExpr,
		&j.NextRunAt, &j.LastStatus, &j.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, identity.ErrNotFound
	}
	return j, err
}

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.JobID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.Detail)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, identity.ErrNotFound
	}
	return r, err
}

// UpsertJob creates the job or refreshes its schedule on (entity, code)
// conflict (scheduler registration is idempotent across restarts).
func (s *PGStore) UpsertJob(ctx context.Context, db platform.DBTX, j *Job) error {
	if err := j.Validate(); err != nil {
		return err
	}
	return db.QueryRow(ctx, `INSERT INTO ferp_jobs
		(entity_id, code, interval_s, cron_expr, next_run_at, enabled)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (entity_id, code) DO UPDATE
		SET interval_s=EXCLUDED.interval_s, cron_expr=EXCLUDED.cron_expr,
		    next_run_at=EXCLUDED.next_run_at, enabled=EXCLUDED.enabled
		RETURNING id, last_status, enabled`,
		j.EntityID, j.Code, j.IntervalS, j.CronExpr, j.NextRunAt, j.Enabled,
	).Scan(&j.ID, &j.LastStatus, &j.Enabled)
}

func (s *PGStore) JobByCode(ctx context.Context, db platform.DBTX, entityID int64, code string) (Job, error) {
	return scanJob(db.QueryRow(ctx, `SELECT `+jobCols+` FROM ferp_jobs WHERE entity_id=$1 AND code=$2`, entityID, code))
}

func (s *PGStore) DueJobs(ctx context.Context, db platform.DBTX, now time.Time, limit int) ([]Job, error) {
	rows, err := db.Query(ctx, `SELECT `+jobCols+` FROM ferp_jobs
		WHERE enabled AND next_run_at <= $1 ORDER BY next_run_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *PGStore) MarkNextRun(ctx context.Context, db platform.DBTX, entityID, id int64, next time.Time, status string) error {
	tag, err := db.Exec(ctx, `UPDATE ferp_jobs SET next_run_at=$1, last_status=$2 WHERE id=$3 AND entity_id=$4`,
		next, status, id, entityID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrNotFound
	}
	return nil
}

func (s *PGStore) StartRun(ctx context.Context, db platform.DBTX, jobID int64, at time.Time) (Run, error) {
	var r Run
	err := db.QueryRow(ctx, `INSERT INTO ferp_job_runs (job_id, started_at, status)
		VALUES ($1,$2,'running') RETURNING id, job_id, started_at, finished_at, status, detail`,
		jobID, at).Scan(&r.ID, &r.JobID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.Detail)
	return r, err
}

func (s *PGStore) FinishRun(ctx context.Context, db platform.DBTX, entityID, runID int64, status, detail string, at time.Time) error {
	tag, err := db.Exec(ctx, `UPDATE ferp_job_runs SET status=$1, detail=$2, finished_at=$3 WHERE id=$4 AND job_id IN (SELECT id FROM ferp_jobs WHERE entity_id=$5)`,
		status, detail, at, runID, entityID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrNotFound
	}
	return nil
}

// RunsOf returns runs oldest→newest (the order PruneIDs expects).
func (s *PGStore) RunsOf(ctx context.Context, db platform.DBTX, jobID int64) ([]Run, error) {
	rows, err := db.Query(ctx, `SELECT id, job_id, started_at, finished_at, status, detail
		FROM ferp_job_runs WHERE job_id=$1 ORDER BY started_at, id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PGStore) DeleteRuns(ctx context.Context, db platform.DBTX, runIDs []int64) error {
	if len(runIDs) == 0 {
		return nil
	}
	_, err := db.Exec(ctx, `DELETE FROM ferp_job_runs WHERE id = ANY($1)`, runIDs)
	return err
}

// MemoryStore is the in-process fake for scheduler/handler tests.
type MemoryStore struct {
	mu   sync.Mutex
	seq  int64
	jobs map[int64]Job
	runs map[int64]Run
}

// NewMemoryStore builds an empty fake.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{jobs: map[int64]Job{}, runs: map[int64]Run{}}
}

func (m *MemoryStore) next() int64 { m.seq++; return m.seq }

func (m *MemoryStore) UpsertJob(_ context.Context, _ platform.DBTX, j *Job) error {
	if err := j.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.jobs {
		if e.EntityID == j.EntityID && e.Code == j.Code {
			j.ID = id
			m.jobs[id] = *j
			return nil
		}
	}
	j.ID = m.next()
	m.jobs[j.ID] = *j
	return nil
}

func (m *MemoryStore) JobByCode(_ context.Context, _ platform.DBTX, entityID int64, code string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.EntityID == entityID && j.Code == code {
			return j, nil
		}
	}
	return Job{}, identity.ErrNotFound
}

func (m *MemoryStore) DueJobs(_ context.Context, _ platform.DBTX, now time.Time, limit int) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Job
	for _, j := range m.jobs {
		if j.Enabled && !j.NextRunAt.After(now) {
			out = append(out, j)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) MarkNextRun(_ context.Context, _ platform.DBTX, entityID, id int64, next time.Time, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || j.EntityID != entityID {
		return identity.ErrNotFound
	}
	j.NextRunAt, j.LastStatus = next, status
	m.jobs[id] = j
	return nil
}

func (m *MemoryStore) StartRun(_ context.Context, _ platform.DBTX, jobID int64, at time.Time) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := Run{ID: m.next(), JobID: jobID, StartedAt: at, Status: "running"}
	m.runs[r.ID] = r
	return r, nil
}

func (m *MemoryStore) FinishRun(_ context.Context, _ platform.DBTX, entityID, runID int64, status, detail string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	if !ok {
		return identity.ErrNotFound
	}
	if j, ok := m.jobs[r.JobID]; !ok || j.EntityID != entityID {
		return identity.ErrNotFound
	}
	r.Status, r.Detail, r.FinishedAt = status, detail, &at
	m.runs[runID] = r
	return nil
}

func (m *MemoryStore) RunsOf(_ context.Context, _ platform.DBTX, jobID int64) ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Run
	for _, r := range m.runs {
		if r.JobID == jobID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *MemoryStore) DeleteRuns(_ context.Context, _ platform.DBTX, runIDs []int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range runIDs {
		delete(m.runs, id)
	}
	return nil
}
