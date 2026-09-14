package agenda

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

// Nil pool = single-node: tick runs the drain unconditionally.
func TestWorkerNilPoolTickRuns(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Now().UTC().Truncate(time.Second)
	e := &Event{EntityID: 1, Title: "Tick", OwnerLogin: "ada",
		StartAt: now.Add(5 * time.Minute), EndAt: now.Add(30 * time.Minute), ReminderMin: 15}
	if err := m.CreateEvent(ctx, nil, e); err != nil {
		t.Fatalf("create: %v", err)
	}
	w := &Worker{Store: m, DB: nil, Pool: nil, Now: func() time.Time { return now }}
	w.tick(ctx)
	due, _ := m.DueRemindersAll(ctx, nil, now, 50)
	if len(due) != 0 {
		t.Fatalf("nil-pool tick did not drain: %d due", len(due))
	}
}

// Pool set + real PG: leader tick drains exactly once (skips without DB).
func TestWorkerLeaderTickDrainsOncePG(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	st := NewPGStore(pool)
	now := time.Now().UTC().Truncate(time.Second)
	e := &Event{EntityID: 1, Title: "PG Tick", OwnerLogin: "ada",
		StartAt: now.Add(5 * time.Minute), EndAt: now.Add(30 * time.Minute), ReminderMin: 15}
	if err := st.CreateEvent(ctx, pool, e); err != nil {
		t.Fatalf("create: %v", err)
	}
	w := &Worker{Store: st, DB: pool, Pool: pool, Now: func() time.Time { return now }}
	w.tick(ctx)
	due, err := st.DueRemindersAll(ctx, pool, now, 50)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("leader tick did not drain: %d due", len(due))
	}
	w.tick(ctx) // second tick (still leader, nothing due) must no-op cleanly
}
