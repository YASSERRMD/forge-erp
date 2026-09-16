package documentsvc

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Phase 2 ECM handlers: folders, versions, search, filing rules.

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// CreateFolder makes a folder under parent_id (absent = root).
func (h *Handler) CreateFolder(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var body struct {
		Name     string `json:"name"`
		ParentID *int64 `json:"parent_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
		return
	}
	f, err := h.svc.CreateFolder(r.Context(), entityID, body.ParentID, body.Name)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

// ListFolders lists direct children of parent_id (absent = root).
func (h *Handler) ListFolders(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var parentID *int64
	if v := r.URL.Query().Get("parent_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, `{"error":"bad parent_id"}`, http.StatusBadRequest)
			return
		}
		parentID = &id
	}
	list, err := h.svc.Store.ListFolders(r.Context(), h.svc.DB, entityID, parentID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if list == nil {
		list = []Folder{}
	}
	writeJSON(w, http.StatusOK, list)
}

// FolderPath resolves the absolute path of a folder.
func (h *Handler) FolderPath(w http.ResponseWriter, r *http.Request) {
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
	p, err := h.svc.FolderPath(r.Context(), entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": p})
}

// MoveFolder reparents a folder (null parent_id = root); cycle-guarded.
func (h *Handler) MoveFolder(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		ParentID *int64 `json:"parent_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
		return
	}
	f, err := h.svc.MoveFolder(r.Context(), entityID, id, body.ParentID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// Versions returns the immutable version history of a file (oldest first).
func (h *Handler) Versions(w http.ResponseWriter, r *http.Request) {
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
	versions, err := h.svc.Versions(r.Context(), entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if versions == nil {
		versions = []FileVersion{}
	}
	writeJSON(w, http.StatusOK, versions)
}

// Restore copies a historical version's bytes into a NEW current version.
func (h *Handler) Restore(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		Version int `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
		return
	}
	u, _ := identity.AuthUser(r)
	d, err := h.svc.RestoreVersion(r.Context(), entityID, id, body.Version, &u.ID)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// Search full-text searches indexed content (entity-scoped, ranked).
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	hits, err := h.svc.Search(r.Context(), entityID,
		r.URL.Query().Get("q"), r.URL.Query().Get("scope"), limit)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if hits == nil {
		hits = []SearchHit{}
	}
	writeJSON(w, http.StatusOK, hits)
}

// ListFilingRules returns all (scope, object_type) -> template mappings.
func (h *Handler) ListFilingRules(w http.ResponseWriter, r *http.Request) {
	if _, entityErr := platform.EntityOf(r); entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	rules, err := h.svc.Store.ListFilingRules(r.Context(), h.svc.DB)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	if rules == nil {
		rules = []FilingRule{}
	}
	writeJSON(w, http.StatusOK, rules)
}

// UpsertFilingRule creates or replaces a filing rule.
func (h *Handler) UpsertFilingRule(w http.ResponseWriter, r *http.Request) {
	if _, entityErr := platform.EntityOf(r); entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var rule FilingRule
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&rule); err != nil {
		http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
		return
	}
	if err := h.svc.UpsertFilingRule(r.Context(), rule); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}
