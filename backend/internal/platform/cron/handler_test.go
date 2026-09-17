package cron

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform/pgtest"
)

func passMW(module, entity, action string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func cronRouter(d Deps) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, d, passMW) })
	return r
}

func doReq(t *testing.T, r *chi.Mux, entityID int64, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	req = req.WithContext(platform.ContextWithEntity(req.Context(), entityID))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestCronHandlerManualRun(t *testing.T) {
	st := NewMemoryStore()
	s := &Scheduler{Store: st}
	ran := 0
	s.Register("nightly", func(ctx context.Context) error { ran++; return nil })
	ctx := context.Background()
	if err := st.UpsertJob(ctx, nil, &Job{EntityID: 1, Code: "nightly",
		IntervalS: 3600, NextRunAt: time.Now().UTC().Add(time.Hour), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	r := cronRouter(Deps{Store: st, Scheduler: s})

	rec := doReq(t, r, 1, http.MethodPost, "/api/v1/cron/jobs/nightly/run", "")
	if rec.Code != http.StatusOK || ran != 1 {
		t.Fatalf("manual run: code=%d ran=%d body=%s", rec.Code, ran, rec.Body.String())
	}
	// Due list is entity-scoped (other tenant's due jobs invisible).
	if err := st.UpsertJob(ctx, nil, &Job{EntityID: 1, Code: "due.job",
		IntervalS: 60, NextRunAt: time.Now().UTC().Add(-time.Minute), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rec = doReq(t, r, 1, http.MethodGet, "/api/v1/cron/jobs", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "due.job") {
		t.Fatalf("owner due list: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, r, 2, http.MethodGet, "/api/v1/cron/jobs", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "due.job") {
		t.Fatalf("cross-entity due list leaked: %s", rec.Body.String())
	}
	// Runs history visible to owner.
	rec = doReq(t, r, 1, http.MethodGet, "/api/v1/cron/jobs/nightly/runs", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "manual") {
		t.Fatalf("runs: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Upsert rejects bad cron expressions with 422.
	rec = doReq(t, r, 1, http.MethodPost, "/api/v1/cron/jobs",
		`{"code":"bad","cron_expr":"*/x * * * *"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad cron upsert: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Upsert accepts a valid cron job.
	rec = doReq(t, r, 1, http.MethodPost, "/api/v1/cron/jobs",
		`{"code":"daily","cron_expr":"0 9 * * *"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("cron upsert: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Missing entity → 401.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cron/jobs", nil)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("no entity: code=%d", rec2.Code)
	}
}

func TestPGCronPersist(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	st := NewPGStore(pool)

	j := &Job{EntityID: 1, Code: "pg.job", IntervalS: 300,
		CronExpr: "*/5 * * * *", NextRunAt: time.Now().UTC().Add(-time.Minute), Enabled: true}
	if err := st.UpsertJob(ctx, pool, j); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if j.ID == 0 {
		t.Fatal("upsert left ID unset")
	}
	got, err := st.JobByCode(ctx, pool, 1, "pg.job")
	if err != nil {
		t.Fatalf("by code: %v", err)
	}
	if got.CronExpr != "*/5 * * * *" || !got.Enabled {
		t.Errorf("round-trip: %+v", got)
	}
	run, err := st.StartRun(ctx, pool, j.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := st.FinishRun(ctx, pool, 1, run.ID, "ok", "", time.Now().UTC()); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	runs, err := st.RunsOf(ctx, pool, j.ID)
	if err != nil || len(runs) != 1 || runs[0].Status != "ok" {
		t.Fatalf("history: %+v %v", runs, err)
	}
	if err := st.DeleteRuns(ctx, pool, []int64{runs[0].ID}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	runs, _ = st.RunsOf(ctx, pool, j.ID)
	if len(runs) != 0 {
		t.Errorf("history after delete: %+v", runs)
	}
}
