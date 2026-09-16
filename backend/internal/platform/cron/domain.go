// Package cron owns the ferp_jobs / ferp_job_runs scheduler ledger
// (migration 0008_platform2; previously no Go owner — the agenda package
// only drains reminders). Jobs carry BOTH scheduling styles: the original
// fixed-interval interval_s column keeps working, and the Phase-2 cron_expr
// column (5-field standard cron, no seconds) takes precedence when set.
// Failures publish a platform.Bus alert; history is capped per job.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// JobFailedSubject is the platform.Bus alert emitted when a run fails.
const JobFailedSubject = "forgeerp.cron.job.failed.v1"

// DefaultRetention is the default cap on stored runs per job (oldest deleted).
const DefaultRetention = 100

// cronLeaderLockKey namespaces the scheduler's advisory-lock leader election
// away from agenda's reminder lock and finance's ledger lock.
const cronLeaderLockKey int64 = 2026091002

// Job is one scheduled unit of work (ferp_jobs row).
type Job struct {
	ID         int64     `json:"id"`
	EntityID   int64     `json:"entity_id"`
	Code       string    `json:"code"`
	IntervalS  int64     `json:"interval_s"` // fixed-interval fallback schedule
	CronExpr   string    `json:"cron_expr"`  // 5-field cron, "" = use IntervalS
	NextRunAt  time.Time `json:"next_run_at"`
	LastStatus string    `json:"last_status"`
	Enabled    bool      `json:"enabled"`
}

// Run is one execution attempt (ferp_job_runs row).
type Run struct {
	ID         int64      `json:"id"`
	JobID      int64      `json:"job_id"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Status     string     `json:"status"` // running|ok|failed
	Detail     string     `json:"detail"`
}

// Validate checks job invariants (cron expressions parse via ParseExpr).
func (j Job) Validate() error {
	if j.EntityID <= 0 {
		return fmt.Errorf("cron: entity_id required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(j.Code) == "" {
		return fmt.Errorf("cron: code required: %w", platform.ErrValidation)
	}
	if strings.TrimSpace(j.CronExpr) != "" {
		if _, err := ParseExpr(j.CronExpr); err != nil {
			return err
		}
	} else if j.IntervalS <= 0 {
		return fmt.Errorf("cron: interval_s must be positive without cron_expr: %w", platform.ErrValidation)
	}
	return nil
}

// NextAfter computes the job's next fire time after from: cron expression
// when set, otherwise the fixed-interval fallback (from + interval_s).
func (j Job) NextAfter(from time.Time) (time.Time, error) {
	if strings.TrimSpace(j.CronExpr) != "" {
		expr, err := ParseExpr(j.CronExpr)
		if err != nil {
			return time.Time{}, err
		}
		return expr.NextAfter(from), nil
	}
	return from.Add(time.Duration(j.IntervalS) * time.Second), nil
}

// field is one parsed cron field (minute/hour/dom/month/dow).
type field struct {
	min, max int
	set      map[int]bool // nil + all=false means "*"
	all      bool
}

func (f field) matches(v int) bool {
	if f.all {
		return true
	}
	return f.set[v]
}

// Expr is a parsed 5-field cron expression (minute hour dom month dow,
// no seconds). Semantics follow standard cron with the usual OR rule:
// when BOTH dom and dow are restricted (not "*"), a time matches if EITHER
// matches; when one is unrestricted, only the restricted one must match.
type Expr struct {
	Minute, Hour, Dom, Month, Dow field
	raw                           string
}

var fieldBounds = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}

// ParseExpr parses a 5-field cron expression. Supported per field:
// "*" (all), "*/n" (step over full range), "a-b" (range), "a-b/n"
// (stepped range), "a,b,c" (lists of the above), plain integers.
// Sunday is 7 or 0 (normalized to 0). Anything else is ErrValidation.
func ParseExpr(raw string) (Expr, error) {
	parts := strings.Fields(raw)
	if len(parts) != 5 {
		return Expr{}, fmt.Errorf("cron: expression %q needs 5 fields: %w", raw, platform.ErrValidation)
	}
	var e Expr
	fields := []*field{&e.Minute, &e.Hour, &e.Dom, &e.Month, &e.Dow}
	for i, p := range parts {
		f, err := parseField(p, fieldBounds[i][0], fieldBounds[i][1])
		if err != nil {
			return Expr{}, fmt.Errorf("cron: field %d: %w", i+1, err)
		}
		*fields[i] = f
	}
	// Normalize Sunday 7 → 0 in the dow set.
	if e.Dow.set != nil && e.Dow.set[7] {
		delete(e.Dow.set, 7)
		e.Dow.set[0] = true
	}
	e.raw = raw
	return e, nil
}

func parseField(s string, min, max int) (field, error) {
	if s == "*" {
		return field{min: min, max: max, all: true}, nil
	}
	out := field{min: min, max: max, set: map[int]bool{}}
	for _, alt := range strings.Split(s, ",") {
		if err := parseAlt(alt, min, max, out.set); err != nil {
			return field{}, err
		}
	}
	if len(out.set) == 0 {
		return field{}, fmt.Errorf("empty field: %w", platform.ErrValidation)
	}
	return out, nil
}

func parseAlt(alt string, min, max int, set map[int]bool) error {
	step := 1
	base := alt
	if i := strings.Index(alt, "/"); i >= 0 {
		base, alt = alt[:i], alt[i+1:]
		n, err := strconv.Atoi(alt)
		if err != nil || n <= 0 {
			return fmt.Errorf("bad step %q: %w", alt, platform.ErrValidation)
		}
		step = n
	}
	lo, hi := min, max
	switch {
	case base == "*":
		// full range with step
	case strings.Contains(base, "-"):
		b := strings.SplitN(base, "-", 2)
		var err error
		if lo, err = strconv.Atoi(b[0]); err != nil {
			return fmt.Errorf("bad range %q: %w", base, platform.ErrValidation)
		}
		if hi, err = strconv.Atoi(b[1]); err != nil {
			return fmt.Errorf("bad range %q: %w", base, platform.ErrValidation)
		}
		if lo > hi {
			return fmt.Errorf("reversed range %q: %w", base, platform.ErrValidation)
		}
	default:
		v, err := strconv.Atoi(base)
		if err != nil {
			return fmt.Errorf("bad value %q: %w", base, platform.ErrValidation)
		}
		lo, hi = v, v
	}
	if lo < min || hi > max {
		return fmt.Errorf("value out of range [%d-%d]: %w", min, max, platform.ErrValidation)
	}
	for v := lo; v <= hi; v += step {
		set[v] = true
	}
	return nil
}

// matches reports whether t satisfies the expression (cron OR rule for dom/dow).
func (e Expr) matches(t time.Time) bool {
	if !e.Minute.matches(t.Minute()) || !e.Hour.matches(t.Hour()) || !e.Month.matches(int(t.Month())) {
		return false
	}
	domR, dowR := !e.Dom.all, !e.Dow.all
	domM, dowM := e.Dom.matches(t.Day()), e.Dow.matches(int(t.Weekday()))
	switch {
	case domR && dowR:
		return domM || dowM
	case domR:
		return domM
	case dowR:
		return dowM
	default:
		return true
	}
}

// NextAfter returns the first minute-boundary fire time strictly after from.
// Search is capped at ~4 years of minutes; failure means no fire time exists
// (only possible for impossible dates like Feb 30 — reported as an error
// rather than looping forever).
func (e Expr) NextAfter(from time.Time) time.Time {
	t := from.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*4*24*60+525600; i++ {
		if e.matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

// PruneIDs returns the run IDs to delete so that at most keep newest runs
// survive. ids must be ordered oldest→newest (as the store returns them).
// Pure function so retention math is unit-testable without a database.
func PruneIDs(ids []int64, keep int) []int64 {
	if keep < 0 {
		keep = 0
	}
	if len(ids) <= keep {
		return nil
	}
	return ids[:len(ids)-keep]
}
