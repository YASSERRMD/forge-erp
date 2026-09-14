package partners

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to persistence and the event bus (bus may be nil in tests).
type Deps struct {
	Store Store
	Bus   platform.Bus
	DB    platform.DBTX
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the partners surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("partners", "organization", "write")).Post("/organizations", h.CreateOrg)
	r.With(mw("partners", "organization", "read")).Get("/organizations", h.ListOrgs)
	r.With(mw("partners", "organization", "read")).Get("/organizations/{id}", h.GetOrg)
	r.With(mw("partners", "organization", "write")).Put("/organizations/{id}", h.UpdateOrg)
	r.With(mw("partners", "contact", "write")).Post("/organizations/{id}/contacts", h.CreateContact)
	r.With(mw("partners", "contact", "read")).Get("/organizations/{id}/contacts", h.ListContacts)
	r.With(mw("partners", "category", "write")).Post("/categories", h.CreateCategory)
}

// Handler implements the partners HTTP surface.
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

func (h *Handler) publish(ctx context.Context, entityID int64, subject, entity string, id int64) {
	if h.deps.Bus == nil {
		return
	}
	_ = h.deps.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, EntityID: entityID, ID: id})
}

// CreateOrg creates an organization (validates rules + hierarchy cycles; 409 on duplicate codes).
func (h *Handler) CreateOrg(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var o Organization
	if err := decode(r, &o); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	o.ID = 0
	o.EntityID = entityID
	o.Status = OrgActive
	if err := o.Validate(); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if o.ParentID != nil {
		if err := CheckNoCycle(0, o.ParentID, func(id int64) (*int64, bool) {
			return h.deps.Store.ParentOf(r.Context(), h.deps.DB, entityID, id)
		}); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	if err := h.deps.Store.CreateOrg(r.Context(), h.deps.DB, &o); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.partners.organization.created.v1", "organization", o.ID)
	writeJSON(w, http.StatusCreated, o)
}

// ListOrgs pages organizations within the caller's entity.
func (h *Handler) ListOrgs(w http.ResponseWriter, r *http.Request) {
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
	list, err := h.deps.Store.ListOrgs(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetOrg fetches one organization (404 outside the caller's entity).
func (h *Handler) GetOrg(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	o, err := h.deps.Store.OrgByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "organization not found")
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// UpdateOrg applies edits with optimistic locking (409 on stale row_version).
func (h *Handler) UpdateOrg(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var o Organization
	if err := decode(r, &o); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	o.ID = id
	o.EntityID = entityID
	if err := o.Validate(); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if o.ParentID != nil {
		if err := CheckNoCycle(o.ID, o.ParentID, func(pid int64) (*int64, bool) {
			return h.deps.Store.ParentOf(r.Context(), h.deps.DB, entityID, pid)
		}); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	if err := h.deps.Store.UpdateOrg(r.Context(), h.deps.DB, &o); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.partners.organization.updated.v1", "organization", o.ID)
	writeJSON(w, http.StatusOK, o)
}

// CreateContact attaches a contact to an organization.
func (h *Handler) CreateContact(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	orgID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if _, err := h.deps.Store.OrgByID(r.Context(), h.deps.DB, entityID, orgID); err != nil {
		writeErr(w, http.StatusNotFound, "organization not found")
		return
	}
	var c Contact
	if err := decode(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	c.ID = 0
	c.EntityID = entityID
	c.OrgID = orgID
	if err := h.deps.Store.CreateContact(r.Context(), h.deps.DB, &c); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.partners.contact.created.v1", "contact", c.ID)
	writeJSON(w, http.StatusCreated, c)
}

// ListContacts returns an organization's contacts.
func (h *Handler) ListContacts(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	orgID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if _, err := h.deps.Store.OrgByID(r.Context(), h.deps.DB, entityID, orgID); err != nil {
		writeErr(w, http.StatusNotFound, "organization not found")
		return
	}
	list, err := h.deps.Store.ContactsOf(r.Context(), h.deps.DB, orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// CreateCategory creates a tag category.
func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var c Category
	if err := decode(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	c.ID = 0
	c.EntityID = entityID
	if err := h.deps.Store.CreateCategory(r.Context(), h.deps.DB, &c); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}
