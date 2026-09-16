package hr

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// RoutesHRM mounts the employee-record surface (called from Routes).
func RoutesHRM(r chi.Router, h *Handler, mw Middleware) {
	r.With(mw("hr", "establishment", "write")).Post("/hr/establishments", h.CreateEstablishment)
	r.With(mw("hr", "establishment", "read")).Get("/hr/establishments", h.ListEstablishments)
	r.With(mw("hr", "employee", "write")).Post("/hr/employees", h.HireEmployee)
	r.With(mw("hr", "employee", "read")).Get("/hr/employees", h.ListEmployees)
	r.With(mw("hr", "employee", "read")).Get("/hr/employees/{id}", h.GetEmployee)
	r.With(mw("hr", "employee", "validate")).Post("/hr/employees/{id}/status", h.SetEmployeeStatus)
	r.With(mw("hr", "employee", "validate")).Post("/hr/employees/{id}/terminate", h.TerminateEmployee)
	r.With(mw("hr", "employee", "write")).Post("/hr/employees/{id}/skills", h.AddSkill)
	r.With(mw("hr", "employee", "read")).Get("/hr/employees/{id}/skills", h.ListSkills)
	r.With(mw("hr", "employee", "write")).Post("/hr/employees/{id}/evaluations", h.AddEvaluation)
	r.With(mw("hr", "employee", "read")).Get("/hr/employees/{id}/evaluations", h.ListEvaluations)
}

// CreateEstablishment registers a work site.
func (h *Handler) CreateEstablishment(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var e Establishment
	if err := decode(r, &e); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	e.ID = 0
	e.EntityID = entityID
	if err := h.deps.Store.CreateEstablishment(r.Context(), h.deps.DB, &e); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.hr.establishment.created.v1", "establishment", e.ID)
	writeJSON(w, http.StatusCreated, e)
}

// ListEstablishments lists work sites.
func (h *Handler) ListEstablishments(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	list, err := h.deps.Store.EstablishmentsOf(r.Context(), h.deps.DB, entityID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// HireEmployee creates an active employee via the HRM service.
func (h *Handler) HireEmployee(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var e Employee
	if err := decode(r, &e); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	e.ID = 0
	svc := NewHRMService(h.deps.Pool, h.deps.Store, h.deps.Bus)
	if err := svc.Hire(r.Context(), h.deps.DB, entityID, &e); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

// ListEmployees pages employee records.
func (h *Handler) ListEmployees(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit, offset, _ := page(r)
	list, err := h.deps.Store.EmployeesOf(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetEmployee fetches one employee record.
func (h *Handler) GetEmployee(w http.ResponseWriter, r *http.Request) {
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
	e, err := h.deps.Store.EmployeeByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// SetEmployeeStatus moves an employee active↔suspended (terminate via /terminate).
func (h *Handler) SetEmployeeStatus(w http.ResponseWriter, r *http.Request) {
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
	var in statusIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	to := EmployeeStatus(in.Status)
	if to == EmployeeTerminated {
		writeErr(w, http.StatusUnprocessableEntity, "hr: use /terminate to end employment")
		return
	}
	e, err := h.deps.Store.SetEmployeeStatus(r.Context(), h.deps.DB, entityID, id, to, in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// TerminateEmployee ends employment via the HRM service.
func (h *Handler) TerminateEmployee(w http.ResponseWriter, r *http.Request) {
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
	var in statusIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	svc := NewHRMService(h.deps.Pool, h.deps.Store, h.deps.Bus)
	e, err := svc.Terminate(r.Context(), h.deps.DB, entityID, id, in.RowVersion)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// AddSkill attaches a skill to an employee.
func (h *Handler) AddSkill(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	eid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var sk EmployeeSkill
	if err := decode(r, &sk); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	sk.ID = 0
	sk.EntityID = entityID
	sk.EmployeeID = eid
	if err := h.deps.Store.AddSkill(r.Context(), h.deps.DB, &sk); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sk)
}

// ListSkills lists an employee's skills.
func (h *Handler) ListSkills(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	eid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.SkillsOf(r.Context(), h.deps.DB, entityID, eid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// AddEvaluation records a periodic review.
func (h *Handler) AddEvaluation(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	eid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var ev Evaluation
	if err := decode(r, &ev); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	ev.ID = 0
	ev.EntityID = entityID
	ev.EmployeeID = eid
	if err := h.deps.Store.AddEvaluation(r.Context(), h.deps.DB, &ev); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ev)
}

// ListEvaluations lists an employee's reviews.
func (h *Handler) ListEvaluations(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	eid, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	list, err := h.deps.Store.EvaluationsOf(r.Context(), h.deps.DB, entityID, eid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}
