package partnership

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Deps wires handlers to persistence, the accrual service, and the bus.
type Deps struct {
	Store Store
	Svc   *Service
	Bus   platform.Bus
	DB    platform.DBTX
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the partnership surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("partnership", "program", "write")).Post("/partner-programs", h.CreateProgram)
	r.With(mw("partnership", "program", "read")).Get("/partner-programs", h.ListPrograms)
	r.With(mw("partnership", "program", "read")).Get("/partner-programs/{id}", h.GetProgram)
	r.With(mw("partnership", "referral", "write")).Post("/partner-programs/{id}/referrals", h.RegisterReferral)
	r.With(mw("partnership", "referral", "read")).Get("/partner-programs/{id}/referrals", h.ListReferrals)
	r.With(mw("partnership", "accrual", "write")).Post("/referrals/{id}/accruals", h.Accrue)
	r.With(mw("partnership", "accrual", "read")).Get("/referrals/{id}/accruals", h.ListAccruals)
}

// Handler implements the partnership HTTP surface.
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

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (h *Handler) publish(ctx context.Context, entityID int64, subject, entity string, id int64) {
	if h.deps.Bus == nil {
		return
	}
	_ = h.deps.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

func (h *Handler) svc() *Service {
	if h.deps.Svc != nil {
		return h.deps.Svc
	}
	return &Service{Store: h.deps.Store, Bus: h.deps.Bus, DB: h.deps.DB}
}

// CreateProgram creates a partner program with commission tiers.
func (h *Handler) CreateProgram(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var p Program
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	p.ID = 0
	p.EntityID = entityID
	if err := h.deps.Store.CreateProgram(r.Context(), h.deps.DB, &p); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.partnership.program.created.v1", "program", p.ID)
	writeJSON(w, http.StatusCreated, p)
}

// ListPrograms pages programs within the caller's entity.
func (h *Handler) ListPrograms(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := h.deps.Store.ListPrograms(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetProgram fetches one program (404 outside the caller's entity).
func (h *Handler) GetProgram(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	p, err := h.deps.Store.ProgramByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type referralIn struct {
	ReferrerOrgID int64  `json:"referrer_org_id"`
	ReferredOrgID int64  `json:"referred_org_id"`
	Code          string `json:"code"`
}

// RegisterReferral records a referral link under a program.
func (h *Handler) RegisterReferral(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	programID, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in referralIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	ref, err := h.svc().RegisterReferral(r.Context(), entityID, programID, in.ReferrerOrgID, in.ReferredOrgID, in.Code)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ref)
}

// ListReferrals lists a program's referrals.
func (h *Handler) ListReferrals(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	programID, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if _, err := h.deps.Store.ProgramByID(r.Context(), h.deps.DB, entityID, programID); err != nil {
		platform.WriteError(w, err)
		return
	}
	list, err := h.deps.Store.ListReferrals(r.Context(), h.deps.DB, entityID, programID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type accrualIn struct {
	SaleTotal int64 `json:"sale_total"`
}

// Accrue records commission on one referred sale total (payout out of scope).
func (h *Handler) Accrue(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	referralID, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in accrualIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	a, err := h.svc().AccrueCommission(r.Context(), entityID, referralID, in.SaleTotal)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// ListAccruals lists a referral's commission accruals.
func (h *Handler) ListAccruals(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	referralID, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if _, err := h.deps.Store.ReferralByID(r.Context(), h.deps.DB, entityID, referralID); err != nil {
		platform.WriteError(w, err)
		return
	}
	list, err := h.deps.Store.AccrualsOf(r.Context(), h.deps.DB, entityID, referralID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}
