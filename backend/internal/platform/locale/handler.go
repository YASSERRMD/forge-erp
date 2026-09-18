package locale

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/forge-erp/backend/internal/platform"
)

// Deps wires handlers to entity defaults (DB may be nil: entity default
// then reads as unset and resolution falls through to English).
type Deps struct {
	DB platform.DBTX
}

// Middleware builds Require-style RBAC gates (identity.Handler.Require in production).
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts the locale surface (caller nests at /api/v1). Catalogues
// are directly consumable by the SPA; direction/plural ride along so the
// client can switch layout without a second call.
func Routes(r chi.Router, d Deps, mw Middleware) {
	h := &Handler{deps: d}
	r.With(mw("locale", "catalogue", "read")).Get("/locales", h.List)
	r.With(mw("locale", "catalogue", "read")).Get("/locales/{code}", h.Get)
}

// Handler implements the locale HTTP surface.
type Handler struct{ deps Deps }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type localeInfo struct {
	Code      string `json:"code"`
	Direction string `json:"direction"`
	Plural    string `json:"plural"`
	Decimal   string `json:"decimal"`
	Thousand  string `json:"thousand"`
	Keys      int    `json:"keys"`
}

// List returns shipped catalogues with layout metadata.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	loader, err := NewLoader("")
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	var out []localeInfo
	for _, tag := range Tags() {
		c, _ := loader.Catalogue(tag)
		dir := "ltr"
		if c.RTL {
			dir = "rtl"
		}
		out = append(out, localeInfo{Code: tag, Direction: dir, Plural: c.Plural,
			Decimal: c.Decimal, Thousand: c.Thousand, Keys: len(c.Strings)})
	}
	writeJSON(w, http.StatusOK, out)
}

// catalogueOut is one catalogue plus the matched tag and the caller's
// effective locale (resolved user → header → entity default → English).
type catalogueOut struct {
	Requested string            `json:"requested"`
	Effective string            `json:"effective"`
	Matched   string            `json:"matched"`
	Direction string            `json:"direction"`
	Strings   map[string]string `json:"strings"`
}

// Get returns one catalogue with fallback applied: the {code} path segment
// wins, then ?locale=, then Accept-Language, then the entity default, then
// English. Matched reports which catalogue actually served (ar requested on
// an en/fr-only future build would match en, visibly).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	eff, err := h.effective(r, code)
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	loader, err := NewLoader("")
	if err != nil {
		platform.WriteError(w, err)
		return
	}
	c, matched := loader.Catalogue(eff)
	dir := "ltr"
	if c.RTL {
		dir = "rtl"
	}
	writeJSON(w, http.StatusOK, catalogueOut{Requested: code, Effective: eff,
		Matched: matched, Direction: dir, Strings: c.Strings})
}

// effective resolves explicit code → ?locale= → Accept-Language → entity
// default → English. An explicit code that names no shipped catalogue is
// ignored (but still echoed as Requested) so header negotiation keeps
// working; the Matched field always shows what actually served.
// (User-preference lookup needs the identity store; callers with one pass
// it explicitly — this surface stays dependency-free.)
func (h *Handler) effective(r *http.Request, code string) (string, error) {
	entityID, _ := platform.EntityOf(r)
	entityDefault, err := EntityDefault(r.Context(), h.deps.DB, entityID)
	if err != nil {
		return "", err
	}
	explicit := code
	if explicit != "" && !slices.Contains(Tags(), Normalize(explicit)) {
		explicit = "" // unshipped tag: negotiate instead of pinning a miss
	}
	if explicit == "" {
		explicit = r.URL.Query().Get("locale")
	}
	if explicit == "" {
		explicit = r.URL.Query().Get("lang")
	}
	header := r.Header.Get("Accept-Language")
	if explicit != "" {
		// Explicit request outranks the header: pass it as the
		// "preference" slot so ResolveLocale prefers it.
		return ResolveLocale(header, explicit, entityDefault), nil
	}
	return ResolveLocale(header, "", entityDefault), nil
}
