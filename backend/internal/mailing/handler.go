package mailing

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
	"github.com/go-chi/chi/v5"
)

// Deps wires handlers to persistence and the campaign service.
type Deps struct {
	Store Store
	Svc   *Service
	Bus   platform.Bus
	DB    platform.DBTX
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the mailing surface (caller nests at /api/v1).
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("mailing", "campaign", "write")).Post("/mailing-campaigns", h.CreateCampaign)
	r.With(mw("mailing", "campaign", "read")).Get("/mailing-campaigns", h.ListCampaigns)
	r.With(mw("mailing", "campaign", "read")).Get("/mailing-campaigns/{id}", h.GetCampaign)
	r.With(mw("mailing", "campaign", "write")).Post("/mailing-campaigns/{id}/queue", h.Queue)
	r.With(mw("mailing", "campaign", "write")).Post("/mailing-campaigns/{id}/send", h.Send)
	r.With(mw("mailing", "campaign", "read")).Get("/mailing-campaigns/{id}/recipients", h.ListRecipients)
	r.With(mw("mailing", "campaign", "write")).Post("/mailing-unsubscribe", h.Unsubscribe)
}

// Handler implements the mailing HTTP surface.
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

func (h *Handler) svc() *Service {
	if h.deps.Svc != nil {
		return h.deps.Svc
	}
	return &Service{Store: h.deps.Store, Bus: h.deps.Bus, DB: h.deps.DB}
}

// CreateCampaign creates a draft campaign.
func (h *Handler) CreateCampaign(w http.ResponseWriter, r *http.Request) {
	entityID, entityErr := platform.EntityOf(r)
	if entityErr != nil {
		platform.WriteError(w, entityErr)
		return
	}
	var c Campaign
	if err := decode(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	c.ID = 0
	c.EntityID = entityID
	c.Status = CampaignDraft
	if err := h.deps.Store.CreateCampaign(r.Context(), h.deps.DB, &c); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// ListCampaigns pages campaigns within the caller's entity.
func (h *Handler) ListCampaigns(w http.ResponseWriter, r *http.Request) {
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
	list, err := h.deps.Store.ListCampaigns(r.Context(), h.deps.DB, entityID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// GetCampaign fetches one campaign (404 outside the caller's entity).
func (h *Handler) GetCampaign(w http.ResponseWriter, r *http.Request) {
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
	c, err := h.deps.Store.CampaignByID(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

type queueIn struct {
	Audience Audience       `json:"audience"`
	Members  []MemberMirror `json:"members"`
	Orgs     []OrgMirror    `json:"orgs"`
}

// Queue expands the audience and queues recipients with unsubscribe tokens.
func (h *Handler) Queue(w http.ResponseWriter, r *http.Request) {
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
	var in queueIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	n, err := h.svc().ExpandAndQueue(r.Context(), entityID, id, in.Audience, in.Members, in.Orgs)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"queued": n})
}

// Send delivers all queued recipients via the notify sender library.
func (h *Handler) Send(w http.ResponseWriter, r *http.Request) {
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
	sent, failed, err := h.svc().SendAll(r.Context(), entityID, id)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent, "failed": failed})
}

// ListRecipients lists per-recipient delivery status.
func (h *Handler) ListRecipients(w http.ResponseWriter, r *http.Request) {
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
	if _, err := h.deps.Store.CampaignByID(r.Context(), h.deps.DB, entityID, id); err != nil {
		platform.WriteError(w, err)
		return
	}
	list, err := h.deps.Store.RecipientsOf(r.Context(), h.deps.DB, entityID, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type unsubIn struct {
	Token string `json:"token"`
}

// Unsubscribe suppresses the address behind a recipient token.
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	var in unsubIn
	if err := decode(r, &in); err != nil || in.Token == "" {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := h.svc().Unsubscribe(r.Context(), in.Token); err != nil {
		platform.WriteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
}
