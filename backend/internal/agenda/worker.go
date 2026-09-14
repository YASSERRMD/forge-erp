package agenda

import (
	"context"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"time"
)

// agendaReminderLockKey is the Postgres advisory-lock key for reminder
// leader election: only the holder drains due reminders each tick, so N
// pods stop sending every reminder N times. Chosen arbitrarily; collisions
// with other advisory locks in this repo are avoided by namespacing here.
const agendaReminderLockKey int64 = 2026091001

// Worker drains due reminders on an interval (daemon path; the dispatch
// endpoint serves cron setups). Each reminder fires exactly once via
// MarkReminded; failures are logged and retried next tick.
//
// Multi-replica safety: when Pool is set, each tick first tries
// pg_try_advisory_xact_lock(agendaReminderLockKey) on a fresh transaction
// and only the holder runs the drain; non-leader ticks no-op silently.
// When Pool is nil (memory/single-node), every tick runs unconditionally.
type Worker struct {
	Store    Store
	DB       platform.DBTX
	Pool     *pgxpool.Pool
	Interval time.Duration
	Now      func() time.Time
	Logger   *log.Logger
}

// RunOnce dispatches all due reminders, returning the sent count.
// It runs unconditionally against w.DB; the Run loop applies leader
// election before calling the shared drain when Pool is set.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	return w.runOnceWithDB(ctx, w.DB)
}

func (w *Worker) runOnceWithDB(ctx context.Context, db platform.DBTX) (int, error) {
	now := time.Now().UTC()
	if w.Now != nil {
		now = w.Now()
	}
	due, err := w.Store.DueRemindersAll(ctx, db, now, 100)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, e := range due {
		if err := w.Store.MarkReminded(ctx, db, e.EntityID, e.ID); err != nil {
			if w.Logger != nil {
				w.Logger.Printf("agenda: reminder %d failed: %v", e.ID, err)
			}
			continue
		}
		if w.Logger != nil {
			w.Logger.Printf("agenda: reminded event %d %q to %s", e.ID, e.Title, e.OwnerLogin)
		}
		sent++
	}
	return sent, nil
}

// tick runs one drain attempt: unconditional when Pool is nil, otherwise
// gated on winning the advisory-lock election for this tick. The election
// and the drain share one transaction so the xact lock is held for the
// whole drain and released on commit/rollback.
func (w *Worker) tick(ctx context.Context) {
	if w.Pool == nil {
		if _, err := w.runOnceWithDB(ctx, w.DB); err != nil && w.Logger != nil {
			w.Logger.Printf("agenda: tick failed: %v", err)
		}
		return
	}
	conn, err := w.Pool.Acquire(ctx)
	if err != nil {
		if w.Logger != nil {
			w.Logger.Printf("agenda: tick failed: %v", err)
		}
		return
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		if w.Logger != nil {
			w.Logger.Printf("agenda: tick failed: %v", err)
		}
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var leader bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, agendaReminderLockKey).Scan(&leader); err != nil {
		if w.Logger != nil {
			w.Logger.Printf("agenda: tick failed: %v", err)
		}
		return
	}
	if !leader {
		return // non-leader tick: no-op, no log spam
	}
	if _, err := w.runOnceWithDB(ctx, tx); err != nil && w.Logger != nil {
		w.Logger.Printf("agenda: tick failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil && w.Logger != nil {
		w.Logger.Printf("agenda: tick failed: %v", err)
	}
}

// Run ticks until ctx ends (0 interval disables the daemon).
func (w *Worker) Run(ctx context.Context) {
	if w.Interval <= 0 {
		return
	}
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}
