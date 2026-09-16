package documentsvc

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Middleware builds Require-style RBAC gates.
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the documents surface (caller nests at /api/v1).
func Routes(r chi.Router, svc *Service, mw Middleware) {
	h := &Handler{svc: svc}
	r.With(mw("documents", "file", "write")).Post("/documents/upload", h.Upload)
	r.With(mw("documents", "file", "read")).Get("/documents", h.List)
	r.With(mw("documents", "file", "read")).Get("/documents/search", h.Search)
	r.With(mw("documents", "file", "read")).Get("/documents/{id}/download", h.Download)
	r.With(mw("documents", "file", "read")).Get("/documents/{id}/versions", h.Versions)
	r.With(mw("documents", "file", "write")).Post("/documents/{id}/restore", h.Restore)
	r.With(mw("documents", "file", "write")).Post("/documents/{id}/share", h.Share)
	r.With(mw("documents", "file", "write")).Post("/documents/folders", h.CreateFolder)
	r.With(mw("documents", "file", "read")).Get("/documents/folders", h.ListFolders)
	r.With(mw("documents", "file", "read")).Get("/documents/folders/{id}/path", h.FolderPath)
	r.With(mw("documents", "file", "write")).Post("/documents/folders/{id}/move", h.MoveFolder)
	r.With(mw("documents", "file", "read")).Get("/documents/filing-rules", h.ListFilingRules)
	r.With(mw("documents", "file", "write")).Post("/documents/filing-rules", h.UpsertFilingRule)
}

// Handler implements the documents HTTP surface.
type Handler struct{ svc *Service }

// Upload accepts one multipart file (field "file", plus scope + object_id fields).
// Optional filing: folder_id (explicit folder), folder_path (auto-created path),
// or auto_file=1 with object_type (filing-rule resolution + auto-create).
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
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
	var folderID *int64
	if v := r.FormValue("folder_id"); v != "" {
		id, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || id <= 0 {
			http.Error(w, `{"error":"bad folder_id"}`, http.StatusBadRequest)
			return
		}
		folderID = &id
	}
	autoFile := r.FormValue("auto_file") == "1" || r.FormValue("auto_file") == "true"
	u, _ := identity.AuthUser(r)
	d, err := h.svc.UploadEx(r.Context(), entityID, UploadInput{
		Scope: r.FormValue("scope"), ObjectType: r.FormValue("object_type"),
		ObjectID: objectID, Name: hdr.Filename, MIME: hdr.Header.Get("Content-Type"),
		Body: f, CreatedBy: &u.ID, FolderID: folderID,
		FolderPath: r.FormValue("folder_path"), AutoFile: autoFile,
	})
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(d)
}

// List filters metadata by scope/object.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var objectID int64
	if v := r.URL.Query().Get("object_id"); v != "" {
		objectID, _ = strconv.ParseInt(v, 10, 64)
	}
	list, err := h.svc.Store.List(r.Context(), h.svc.DB, entityID, r.URL.Query().Get("scope"), objectID)
	if err != nil {
		http.Error(w, `{"error":"list failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// Download streams stored bytes with the recorded MIME type.
// ?version=N serves a historical revision (defaults to current).
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"bad id"}`, http.StatusBadRequest)
		return
	}
	d, err := h.svc.Store.ByID(r.Context(), h.svc.DB, entityID, id)
	if err != nil || d.EntityID != entityID {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	key, mime, name := d.StorageKey, d.MIME, d.Name
	if v := r.URL.Query().Get("version"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n <= 0 {
			http.Error(w, `{"error":"bad version"}`, http.StatusBadRequest)
			return
		}
		vv, verr := h.svc.Store.VersionByNumber(r.Context(), h.svc.DB, entityID, id, n)
		if verr != nil {
			platform.WriteError(w, verr)
			return
		}
		key, mime = vv.StorageKey, vv.MIME
	}
	rc, err := h.svc.Storage.Get(r.Context(), key)
	if err != nil {
		http.Error(w, `{"error":"stored bytes missing"}`, http.StatusGone)
		return
	}
	defer rc.Close()
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = io.Copy(w, rc)
}

// Share mints a bearer link for one file (default 7 days, max 90).
func (h *Handler) Share(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, `{"error":"bad id"}`, http.StatusBadRequest)
		return
	}
	d, err := h.svc.Store.ByID(r.Context(), h.svc.DB, entityID, id)
	if err != nil || d.EntityID != entityID {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	sb, ok := h.svc.Store.(ShareStore)
	if !ok {
		http.Error(w, `{"error":"sharing unavailable"}`, http.StatusNotImplemented)
		return
	}
	var body struct {
		ExpiresHours int `json:"expires_hours"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16)).Decode(&body)
	defer r.Body.Close()
	if body.ExpiresHours <= 0 {
		body.ExpiresHours = 24 * 7
	}
	if body.ExpiresHours > 24*90 {
		body.ExpiresHours = 24 * 90
	}
	token, err := MintToken()
	if err != nil {
		http.Error(w, `{"error":"token failed"}`, http.StatusInternalServerError)
		return
	}
	st := &ShareToken{Token: token, EntityID: d.EntityID, DocID: d.ID,
		ExpiresAt: time.Now().UTC().Add(time.Duration(body.ExpiresHours) * time.Hour)}
	if err := sb.CreateShare(r.Context(), h.svc.DB, st); err != nil {
		http.Error(w, `{"error":"share failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(st)
}

// PublicShare serves a live bearer link without authentication (portal-lite).
// Mount outside the RBAC group: the token IS the credential.
func PublicShare(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := chi.URLParam(r, "token")
		if token == "" {
			http.Error(w, `{"error":"bad token"}`, http.StatusBadRequest)
			return
		}
		sb, ok := svc.Store.(ShareStore)
		if !ok {
			http.Error(w, `{"error":"sharing unavailable"}`, http.StatusNotImplemented)
			return
		}
		st, err := sb.ShareTarget(r.Context(), svc.DB, token)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		d, err := svc.Store.ByID(r.Context(), svc.DB, st.EntityID, st.DocID)
		if err != nil || d.EntityID != st.EntityID {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		rc, err := svc.Storage.Get(r.Context(), d.StorageKey)
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
}
