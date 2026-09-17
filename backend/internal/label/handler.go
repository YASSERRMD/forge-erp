package label

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Deps wires handlers to persistence and the render service.
type Deps struct {
	Store Store
	Svc   *Service
	Bus   platform.Bus
	DB    platform.DBTX
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the label surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("label", "sheet", "write")).Post("/label-sheets", h.CreateSheet)
	r.With(mw("label", "sheet", "read")).Get("/label-sheets", h.ListSheets)
	r.With(mw("label", "sheet", "read")).Get("/label-sheets/{id}", h.GetSheet)
	r.With(mw("label", "sheet", "write")).Put("/label-sheets/{id}", h.UpdateSheet)
	r.With(mw("label", "sheet", "write")).Delete("/label-sheets/{id}", h.DeleteSheet)
	r.With(mw("label", "sheet", "read")).Post("/label-sheets/{id}/pdf", h.RenderPDF)
}

// Handler implements the label HTTP surface.
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

// CreateSheet creates a sticker-sheet definition.
func (h *Handler) CreateSheet(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var sh SheetDef
	if err := decode(r, &sh); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	sh.ID = 0
	sh.EntityID = entityID
	if err := h.deps.Store.CreateSheet(r.Context(), h.deps.DB, &sh); err != nil {
		platform.WriteError(w, err)
		return
	}
	h.publish(r.Context(), entityID, "forgeerp.label.sheet.created.v1", "sheet", sh.ID)
	writeJSON(w, http.StatusCreated, sh)
}

// ListSheets pages sheet definitions within the caller's entity.
func (h *Handler) ListSheets(w http.ResponseWriter, r *http.Request) {
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
	list, err := h.deps.Store.ListSheets(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetSheet fetches one sheet (404 outside the caller's entity).
func (h *Handler) GetSheet(w http.ResponseWriter, r *http.Request) {
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
	sh, err := h.deps.Store.SheetByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sh)
}

// UpdateSheet applies edits with optimistic locking (409 on stale row_version).
func (h *Handler) UpdateSheet(w http.ResponseWriter, r *http.Request) {
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
	var sh SheetDef
	if err := decode(r, &sh); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	sh.ID = id
	sh.EntityID = entityID
	if err := h.deps.Store.UpdateSheet(r.Context(), h.deps.DB, &sh); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sh)
}

// DeleteSheet removes a sheet definition.
func (h *Handler) DeleteSheet(w http.ResponseWriter, r *http.Request) {
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
	if err := h.deps.Store.DeleteSheet(r.Context(), h.deps.DB, entityID, id); err != nil {
		platform.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type pdfIn struct {
	Rows []LabelRow `json:"rows"`
}

// RenderPDF renders label rows onto the sheet through the docgen registry.
func (h *Handler) RenderPDF(w http.ResponseWriter, r *http.Request) {
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
	var in pdfIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	sh, err := h.deps.Store.SheetByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	pdf, contentType, err := h.svc().RenderPDF(r.Context(), entityID, sh.Code, in.Rows)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdf)
}
