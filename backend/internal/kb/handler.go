package kb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to persistence and the event bus.
type Deps struct {
	Store Store
	Bus   platform.Bus
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the kb surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("kb", "article", "write")).Post("/kb/articles", h.CreateArticle)
	r.With(mw("kb", "article", "read")).Get("/kb/articles", h.ListArticles)
	r.With(mw("kb", "article", "write")).Put("/kb/articles/{id}", h.UpdateArticle)
	r.With(mw("kb", "article", "read")).Get("/kb/search", h.Search)
	r.With(mw("kb", "article", "validate")).Post("/kb/articles/{id}/status", h.SetArticleStatus)
}

// Handler implements the kb HTTP surface.
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

func entityOf(r *http.Request) int64 {
	if u, ok := identity.AuthUser(r); ok && u.EntityID != 0 {
		return u.EntityID
	}
	return 1
}

func storeErrorCode(err error) int {
	switch {
	case errors.Is(err, identity.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, identity.ErrVersionConflict):
		return http.StatusConflict
	case err != nil && strings.Contains(err.Error(), "duplicate"):
		return http.StatusConflict
	default:
		return http.StatusUnprocessableEntity
	}
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func page(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (h *Handler) publish(ctx context.Context, subject, entity string, id int64) {
	if h.deps.Bus == nil {
		return
	}
	_ = h.deps.Bus.Publish(ctx, platform.Event{Subject: subject, Entity: entity, ID: id})
}

type statusIn struct {
	Status     int16 `json:"status"`
	RowVersion int64 `json:"row_version"`
}

// CreateArticle saves a draft article.
func (h *Handler) CreateArticle(w http.ResponseWriter, r *http.Request) {
	var a Article
	if err := decode(r, &a); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	a.ID = 0
	a.EntityID = entityOf(r)
	a.Status = ArticleDraft
	if err := h.deps.Store.CreateArticle(r.Context(), &a); err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// ListArticles pages articles (publishedOnly=1 filters drafts).
func (h *Handler) ListArticles(w http.ResponseWriter, r *http.Request) {
	limit, offset := page(r)
	list, err := h.deps.Store.ListArticles(r.Context(), entityOf(r),
		r.URL.Query().Get("publishedOnly") == "1", limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type updateArticleIn struct {
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Tags       []string `json:"tags"`
	RowVersion int64    `json:"row_version"`
}

// UpdateArticle edits a draft article.
func (h *Handler) UpdateArticle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in updateArticleIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	a, err := h.deps.Store.UpdateArticle(r.Context(), id, in.Title, in.Body, in.Tags, in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// SetArticleStatus publishes or unpublishes an article.
func (h *Handler) SetArticleStatus(w http.ResponseWriter, r *http.Request) {
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
	a, err := h.deps.Store.SetArticleStatus(r.Context(), id, ArticleStatus(in.Status), in.RowVersion)
	if err != nil {
		writeErr(w, storeErrorCode(err), err.Error())
		return
	}
	h.publish(r.Context(), "forgeerp.kb.article.status.v1", "article", a.ID)
	writeJSON(w, http.StatusOK, a)
}

// Search searches published articles.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	list, err := h.deps.Store.SearchArticles(r.Context(), entityOf(r), r.URL.Query().Get("q"), 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "search failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}
