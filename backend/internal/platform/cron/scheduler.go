package cron

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// JobFunc is the work a job code performs. Return nil on success; any error
// marks the run failed and publishes the JobFailedSubject bus alert.
type JobFunc func(ctx context.Context) error

// Scheduler runs due jobs on an interval (daemon path; the handler serves
// manual runs). Each tick fires every due job exactly once via its run row;
// failures advance the schedule normally (no hot-loop on a poison job) and
// are recorded + alerted.
//
// Multi-replica safety reuses the agenda advisory-lock pattern: when Pool is
// set, each tick first tries pg_try_advisory_xact_lock(cronLeaderLockKey) on
// a fresh transaction and only the holder drains; non-leader ticks no-op
// silently. When Pool is nil (memory/single-node), every tick runs.
type Scheduler struct {
	Store     Store
	DB        platform.DBTX
	Pool      *pgxpool.Pool
	Bus       platform.Bus
	Interval  time.Duration
	Retention int // max runs kept per job (<=0 = DefaultRetention)
	Now       func() time.Time
	Logger    *slog.Logger

	mu   sync.Mutex
	funcs map[string]JobFunc
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Scheduler) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return platform.Default()
}

func (s *Scheduler) retention() int {
	if s.Retention <= 0 {
		return DefaultRetention
	}
	return s.Retention
}

// Register binds executable code to a job code (called at boot wiring).
func (s *Scheduler) Register(code string, fn JobFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.funcs == nil {
		s.funcs = map[string]JobFunc{}
	}
	s.funcs[code] = fn
}

func (s *Scheduler) lookup(code string) (JobFunc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn, ok := s.funcs[code]
	return fn, ok
}

// Tick drains all due jobs once, returning the fired count. Unconditional
// against s.DB; the Run loop applies leader election when Pool is set.
func (s *Scheduler) Tick(ctx context.Context) (int, error) {
	return s.tickOn(ctx, s.DB)
}

func (s *Scheduler) tickOn(ctx context.Context, db platform.DBTX) (int, error) {
	now := s.now()
	due, err := s.Store.DueJobs(ctx, db, now, 100)
	if err != nil {
		return 0, err
	}
	fired := 0
	for _, j := range due {
		if err := s.fireOne(ctx, db, j, now); err != nil {
			s.log().Error("cron: job tick failed", "code", j.Code, "entity", j.EntityID, "error", err)
			continue
		}
		fired++
	}
	return fired, nil
}

// fireOne records a run row, executes the registered func (unregistered codes
// are recorded as failed so the gap is visible, not silent), advances the
// schedule, and enforces the retention cap.
func (s *Scheduler) fireOne(ctx context.Context, db platform.DBTX, j Job, now time.Time) error {
	fn, ok := s.lookup(j.Code)
	run, err := s.Store.StartRun(ctx, db, j.ID, now)
	if err != nil {
		return err
	}
	status, detail := "ok", ""
	if !ok {
		status, detail = "failed", "cron: no func registered for code"
	} else if err := fn(ctx); err != nil {
		status, detail = "failed", err.Error()
	}
	if err := s.Store.FinishRun(ctx, db, run.ID, status, detail, s.now()); err != nil {
		return err
	}
	next, err := j.NextAfter(now)
	if err != nil {
		// Poison cron_expr edited behind our back: keep the fixed-interval
		// fallback moving so one bad edit cannot wedge the whole tick.
		next = now.Add(time.Duration(max(j.IntervalS, 60)) * time.Second)
		detail += " (bad cron_expr, interval fallback)"
	}
	if err := s.Store.MarkNextRun(ctx, db, j.EntityID, j.ID, next, status); err != nil {
		return err
	}
	s.log().Info("cron: job fired", "code", j.Code, "entity", j.EntityID,
		"status", status, "next_run", next.Format(time.RFC3339))
	if status == "failed" && s.Bus != nil {
		_ = s.Bus.Publish(ctx, platform.Event{
			Subject:  JobFailedSubject,
			Entity:   "job",
			EntityID: j.EntityID,
			ID:       j.ID,
			Payload:  map[string]any{"code": j.Code, "run_id": run.ID, "detail": detail},
		})
	}
	return s.enforceRetention(ctx, db, j.ID)
}

func (s *Scheduler) enforceRetention(ctx context.Context, db platform.DBTX, jobID int64) error {
	runs, err := s.Store.RunsOf(ctx, db, jobID)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	if drop := PruneIDs(ids, s.retention()); len(drop) > 0 {
		return s.Store.DeleteRuns(ctx, db, drop)
	}
	return nil
}

// RunNow fires one job on demand (manual run endpoint): it executes even
// when disabled or not due, then advances the schedule from now.
func (s *Scheduler) RunNow(ctx context.Context, entityID int64, code string) (Run, error) {
	j, err := s.Store.JobByCode(ctx, s.DB, entityID, code)
	if err != nil {
		return Run{}, err
	}
	now := s.now()
	fn, ok := s.lookup(code)
	run, err := s.Store.StartRun(ctx, s.DB, j.ID, now)
	if err != nil {
		return Run{}, err
	}
	status, detail := "ok", "manual"
	if !ok {
		status, detail = "failed", "cron: no func registered for code"
	} else if err := fn(ctx); err != nil {
		status, detail = "failed", err.Error()
	}
	if err := s.Store.FinishRun(ctx, s.DB, run.ID, status, detail, s.now()); err != nil {
		return Run{}, err
	}
	run.Status, run.Detail = status, detail
	if status == "failed" && s.Bus != nil {
		_ = s.Bus.Publish(ctx, platform.Event{
			Subject:  JobFailedSubject,
			Entity:   "job",
			EntityID: j.EntityID,
			ID:       j.ID,
			Payload:  map[string]any{"code": j.Code, "run_id": run.ID, "detail": detail, "manual": true},
		})
	}
	return run, s.enforceRetention(ctx, s.DB, j.ID)
}

// tick runs one drain attempt: unconditional when Pool is nil, otherwise
// gated on winning the advisory-lock election (agenda pattern: election and
// drain share one transaction so the xact lock covers the whole drain).
func (s *Scheduler) tick(ctx context.Context) {
	if s.Pool == nil {
		if _, err := s.tickOn(ctx, s.DB); err != nil {
			s.log().Error("cron: tick failed", "error", err)
		}
		return
	}
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		s.log().Error("cron: tick failed", "error", err)
		return
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		s.log().Error("cron: tick failed", "error", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var leader bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, cronLeaderLockKey).Scan(&leader); err != nil {
		s.log().Error("cron: tick failed", "error", err)
		return
	}
	if !leader {
		return // non-leader tick: no-op, no log spam
	}
	if _, err := s.tickOn(ctx, tx); err != nil {
		s.log().Error("cron: tick failed", "error", err)
	}
	if err := tx.Commit(ctx); err != nil {
		s.log().Error("cron: tick failed", "error", err)
	}
}

// Run ticks until ctx ends (0 interval disables the daemon).
func (s *Scheduler) Run(ctx context.Context) {
	if s.Interval <= 0 {
		return
	}
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
