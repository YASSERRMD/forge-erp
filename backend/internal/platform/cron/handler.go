package cron

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires the cron surface to persistence and the scheduler.
type Deps struct {
	Store     Store
	Scheduler *Scheduler
	Bus       platform.Bus
	DB        platform.DBTX
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the cron surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("cron", "job", "read")).Get("/cron/jobs", h.ListDue)
	r.With(mw("cron", "job", "read")).Get("/cron/jobs/{code}/runs", h.ListRuns)
	r.With(mw("cron", "job", "write")).Post("/cron/jobs", h.Upsert)
	r.With(mw("cron", "job", "execute")).Post("/cron/jobs/{code}/run", h.RunNow)
}

// Handler implements the cron HTTP surface.
type Handler struct{ deps Deps }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

// ListDue returns the caller's entity jobs due as of now.
func (h *Handler) ListDue(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	due, err := h.deps.Store.DueJobs(r.Context(), h.deps.DB, time.Now().UTC(), limit)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	mine := due[:0]
	for _, j := range due {
		if j.EntityID == entityID {
			mine = append(mine, j)
		}
	}
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	platform.ReqCtxLogger(r.Context(), nil).Info("cron: listed due jobs",
		"count", len(mine))
	writeJSON(w, http.StatusOK, mine)
}

// ListRuns returns a job's run history (oldest→newest).
func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	code := chi.URLParam(r, "code")
	j, err := h.deps.Store.JobByCode(r.Context(), h.deps.DB, entityID, code)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	runs, err := h.deps.Store.RunsOf(r.Context(), h.deps.DB, j.ID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

type upsertIn struct {
	Code      string `json:"code"`
	IntervalS int64  `json:"interval_s"`
	CronExpr  string `json:"cron_expr"`
	Enabled   *bool  `json:"enabled"`
}

// Upsert registers (or refreshes) a job's schedule; next_run_at defaults to
// now so a fresh job is due on the next tick.
func (h *Handler) Upsert(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var in upsertIn
	if err := decode(r, &in); err != nil || in.Code == "" {
		writeErr(w, http.StatusBadRequest, "code required")
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	j := &Job{EntityID: entityID, Code: in.Code, IntervalS: in.IntervalS,
		CronExpr: in.CronExpr, NextRunAt: time.Now().UTC(), Enabled: enabled}
	if err := h.deps.Store.UpsertJob(r.Context(), h.deps.DB, j); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, j)
}

// RunNow fires one job on demand (manual run endpoint).
func (h *Handler) RunNow(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	if h.deps.Scheduler == nil {
		writeErr(w, http.StatusNotImplemented, "scheduler not wired")
		return
	}
	run, err := h.deps.Scheduler.RunNow(r.Context(), entityID, chi.URLParam(r, "code"))
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
