package cron

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

func TestParseVectors(t *testing.T) {
	valid := []string{
		"* * * * *",
		"0 9 * * 1-5",
		"*/15 * * * *",
		"0 0 1 * *",
		"30 4 1,15 * 0",
		"0 0 * * 7", // Sunday as 7
		"5-45/20 8-18 * * *",
	}
	for _, raw := range valid {
		if _, err := ParseExpr(raw); err != nil {
			t.Errorf("ParseExpr(%q) = %v, want nil", raw, err)
		}
	}
	invalid := []string{
		"",
		"* * * *",          // 4 fields
		"* * * * * *",      // 6 fields (seconds rejected)
		"60 * * * *",       // minute out of range
		"* 24 * * *",       // hour out of range
		"* * 0 * *",        // dom 0 invalid
		"* * * 13 *",       // month 13 invalid
		"* * * * 8",        // dow 8 invalid
		"*/0 * * * *",      // zero step
		"5-2 * * * *",      // reversed range
		"abc * * * *",      // garbage
		"* * * * MON",      // names unsupported
		"@daily",           // nicknames unsupported
		"*, * * * *",       // space inside field
	}
	for _, raw := range invalid {
		expr, err := ParseExpr(raw)
		if err == nil {
			t.Errorf("ParseExpr(%q) = %+v, want error", raw, expr)
			continue
		}
		if !errors.Is(err, platform.ErrValidation) {
			t.Errorf("ParseExpr(%q) err %v is not ErrValidation", raw, err)
		}
	}
}

func TestNextFireVectors(t *testing.T) {
	must := func(raw string) Expr {
		e, err := ParseExpr(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		return e
	}
	// Wednesday 2026-09-16 10:20 UTC.
	base := time.Date(2026, 9, 16, 10, 20, 0, 0, time.UTC)
	cases := []struct {
		expr string
		want time.Time
	}{
		{"* * * * *", time.Date(2026, 9, 16, 10, 21, 0, 0, time.UTC)},
		{"30 10 * * *", time.Date(2026, 9, 16, 10, 30, 0, 0, time.UTC)},
		{"20 10 * * *", time.Date(2026, 9, 17, 10, 20, 0, 0, time.UTC)}, // strictly after
		{"0 9 * * *", time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)},
		{"0 9 * * 1-5", time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)},  // Thu
		{"0 9 * * 6", time.Date(2026, 9, 19, 9, 0, 0, 0, time.UTC)},    // Sat
		{"0 9 * * 0", time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)},    // Sun
		{"0 9 * * 7", time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)},    // Sun as 7
		{"*/15 * * * *", time.Date(2026, 9, 16, 10, 30, 0, 0, time.UTC)},
		{"0 0 1 * *", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := must(c.expr).NextAfter(base); !got.Equal(c.want) {
			t.Errorf("NextAfter(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
	// Cron OR rule: dom OR dow when both restricted.
	orExpr := must("0 0 1 * 0") // 1st of month OR Sunday
	got := orExpr.NextAfter(base)
	if got.Weekday() != time.Sunday && got.Day() != 1 {
		t.Errorf("OR rule: got %v, want a Sunday or the 1st", got)
	}
	// Fixed-interval compat: cron_expr empty → from + interval.
	j := Job{EntityID: 1, Code: "x", IntervalS: 3600}
	next, err := j.NextAfter(base)
	if err != nil || !next.Equal(base.Add(time.Hour)) {
		t.Errorf("interval fallback = %v,%v; want base+1h", next, err)
	}
	// Cron takes precedence over interval when set.
	j2 := Job{EntityID: 1, Code: "y", IntervalS: 3600, CronExpr: "0 9 * * *"}
	next2, err := j2.NextAfter(base)
	if err != nil || !next2.Equal(time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("cron precedence = %v,%v", next2, err)
	}
	// Validation: neither schedule → error.
	if err := (Job{EntityID: 1, Code: "z"}).Validate(); !errors.Is(err, platform.ErrValidation) {
		t.Errorf("empty schedule Validate = %v, want ErrValidation", err)
	}
}

func TestPruneIDs(t *testing.T) {
	ids := []int64{1, 2, 3, 4, 5}
	if got := PruneIDs(ids, 5); len(got) != 0 {
		t.Errorf("keep-all = %v", got)
	}
	if got := PruneIDs(ids, 2); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("keep-2 = %v, want [1 2 3]", got)
	}
	if got := PruneIDs(ids, 0); len(got) != 5 {
		t.Errorf("keep-0 = %v", got)
	}
	if got := PruneIDs(nil, 3); len(got) != 0 {
		t.Errorf("empty = %v", got)
	}
}

func TestSchedulerTick(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	now := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	bus := platform.NewMemoryBus()
	var alerts []platform.Event
	bus.Subscribe(JobFailedSubject, func(_ context.Context, e platform.Event) { alerts = append(alerts, e) })

	s := &Scheduler{Store: st, Bus: bus, Retention: 3, Now: func() time.Time { return now }}
	calls := 0
	s.Register("ok.job", func(ctx context.Context) error { calls++; return nil })
	s.Register("bad.job", func(ctx context.Context) error { return errors.New("boom") })

	past := now.Add(-time.Minute)
	for _, j := range []*Job{
		{EntityID: 1, Code: "ok.job", IntervalS: 60, NextRunAt: past, Enabled: true},
		{EntityID: 1, Code: "bad.job", IntervalS: 60, NextRunAt: past, Enabled: true},
		{EntityID: 1, Code: "ghost.job", IntervalS: 60, NextRunAt: past, Enabled: true}, // unregistered
		{EntityID: 1, Code: "future.job", IntervalS: 3600, NextRunAt: now.Add(time.Hour)},
	} {
		if err := st.UpsertJob(ctx, nil, j); err != nil {
			t.Fatal(err)
		}
	}
	fired, err := s.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fired != 3 || calls != 1 {
		t.Errorf("fired=%d calls=%d, want 3/1", fired, calls)
	}
	// bad.job + ghost.job both alerted.
	if len(alerts) != 2 {
		t.Fatalf("alerts=%d, want 2", len(alerts))
	}
	codes := map[string]bool{}
	for _, a := range alerts {
		codes[a.Payload["code"].(string)] = true
	}
	if !codes["bad.job"] || !codes["ghost.job"] {
		t.Errorf("alert codes = %v", codes)
	}
	// Failed jobs still advance (no hot-loop).
	j, _ := st.JobByCode(ctx, nil, 1, "bad.job")
	if !j.NextRunAt.After(now.Add(-time.Minute)) || j.LastStatus != "failed" {
		t.Errorf("bad.job not advanced: %+v", j)
	}
	// Cron-expr job advances by expression, not interval.
	if err := st.UpsertJob(ctx, nil, &Job{EntityID: 1, Code: "ok.job", IntervalS: 60,
		CronExpr: "0 9 * * *", NextRunAt: past, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	j, _ = st.JobByCode(ctx, nil, 1, "ok.job")
	want := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)
	if !j.NextRunAt.Equal(want) {
		t.Errorf("cron next = %v, want %v", j.NextRunAt, want)
	}
}

func TestSchedulerRetention(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	s := &Scheduler{Store: st, Retention: 3}
	s.Register("r.job", func(ctx context.Context) error { return nil })
	if err := st.UpsertJob(ctx, nil, &Job{EntityID: 1, Code: "r.job",
		IntervalS: 1, NextRunAt: time.Now().UTC().Add(-time.Hour), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		j, _ := st.JobByCode(ctx, nil, 1, "r.job")
		if err := s.fireOne(ctx, nil, j, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	runs, _ := st.RunsOf(ctx, nil, 1)
	if len(runs) > 3 {
		t.Errorf("runs kept = %d, want <= 3", len(runs))
	}
}

func TestRunNow(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()
	s := &Scheduler{Store: st}
	ran := false
	s.Register("m.job", func(ctx context.Context) error { ran = true; return nil })
	future := time.Now().UTC().Add(24 * time.Hour)
	if err := st.UpsertJob(ctx, nil, &Job{EntityID: 7, Code: "m.job",
		IntervalS: 86400, NextRunAt: future, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	run, err := s.RunNow(ctx, 7, "m.job")
	if err != nil {
		t.Fatal(err)
	}
	if !ran || run.Status != "ok" || run.Detail != "manual" {
		t.Errorf("run = %+v ran=%v", run, ran)
	}
	// Unknown code → not found (entity-scoped, no cross-tenant leak).
	if _, err := s.RunNow(ctx, 7, "nope"); !errors.Is(err, platform.ErrNotFound) {
		t.Errorf("unknown code err = %v, want ErrNotFound", err)
	}
	if _, err := s.RunNow(ctx, 8, "m.job"); !errors.Is(err, platform.ErrNotFound) {
		t.Errorf("wrong entity err = %v, want ErrNotFound", err)
	}
	// Failed manual run still returns the run row for inspection.
	s.Register("f.job", func(ctx context.Context) error { return errors.New("manual boom") })
	if err := st.UpsertJob(ctx, nil, &Job{EntityID: 7, Code: "f.job",
		IntervalS: 60, NextRunAt: future}); err != nil {
		t.Fatal(err)
	}
	run, err = s.RunNow(ctx, 7, "f.job")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "failed" || !strings.Contains(run.Detail, "manual boom") {
		t.Errorf("failed manual run = %+v", run)
	}
}
