package agenda

import (
	"context"
	"log"
	"time"
)

// Worker drains due reminders on an interval (daemon path; the dispatch
// endpoint serves cron setups). Each reminder fires exactly once via
// MarkReminded; failures are logged and retried next tick.
type Worker struct {
	Store    Store
	Interval time.Duration
	Now      func() time.Time
	Logger   *log.Logger
}

// RunOnce dispatches all due reminders, returning the sent count.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	if w.Now != nil {
		now = w.Now()
	}
	due, err := w.Store.DueRemindersAll(ctx, now, 100)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, e := range due {
		if err := w.Store.MarkReminded(ctx, e.ID); err != nil {
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
			if _, err := w.RunOnce(ctx); err != nil && w.Logger != nil {
				w.Logger.Printf("agenda: tick failed: %v", err)
			}
		}
	}
}
