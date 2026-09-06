package documentsvc

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Middleware builds Require-style RBAC gates.
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the documents surface (caller nests at /api/v1).
func Routes(r chi.Router, svc *Service, mw Middleware) {
	h := &Handler{svc: svc}
	r.With(mw("documents", "file", "write")).Post("/documents/upload", h.Upload)
	r.With(mw("documents", "file", "read")).Get("/documents", h.List)
	r.With(mw("documents", "file", "read")).Get("/documents/{id}/download", h.Download)
}

// Handler implements the documents HTTP surface.
type Handler struct{ svc *Service }

func entityOf(r *http.Request) int64 {
	if u, ok := identity.AuthUser(r); ok && u.EntityID != 0 {
		return u.EntityID
	}
	return 1
}

// Upload accepts one multipart file (field "file", plus scope + object_id fields).
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, `{"error":"multipart required"}`, http.StatusBadRequest)
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"file field required"}`, http.StatusBadRequest)
		return
	}
	defer f.Close()
	var objectID int64
	if v := r.FormValue("object_id"); v != "" {
		objectID, _ = strconv.ParseInt(v, 10, 64)
	}
	u, _ := identity.AuthUser(r)
	d, err := h.svc.Upload(r.Context(), entityOf(r), r.FormValue("scope"), objectID,
		hdr.Filename, hdr.Header.Get("Content-Type"), f, &u.ID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(d)
}

// List filters metadata by scope/object.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	var objectID int64
	if v := r.URL.Query().Get("object_id"); v != "" {
		objectID, _ = strconv.ParseInt(v, 10, 64)
	}
	list, err := h.svc.Store.List(r.Context(), entityOf(r), r.URL.Query().Get("scope"), objectID)
	if err != nil {
		http.Error(w, `{"error":"list failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// Download streams stored bytes with the recorded MIME type.
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"bad id"}`, http.StatusBadRequest)
		return
	}
	d, err := h.svc.Store.ByID(r.Context(), id)
	if err != nil || d.EntityID != entityOf(r) {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	rc, err := h.svc.Storage.Get(r.Context(), d.StorageKey)
	if err != nil {
		http.Error(w, `{"error":"stored bytes missing"}`, http.StatusGone)
		return
	}
	defer rc.Close()
	mime := d.MIME
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", `attachment; filename="`+d.Name+`"`)
	_, _ = io.Copy(w, rc)
}
