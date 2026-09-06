package search

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/YASSERRMD/forge-erp/backend/internal/identity"
)

// Middleware builds Require-style RBAC gates.
type Middleware func(module, entity, action string) func(http.Handler) http.Handler

// Routes mounts GET /search (caller nests at /api/v1).
func Routes(r chi.Router, s *MemorySearcher, mw Middleware) {
	r.With(mw("search", "query", "read")).Get("/search", func(w http.ResponseWriter, req *http.Request) {
		var entityID int64 = 1
		if u, ok := identity.AuthUser(req); ok && u.EntityID != 0 {
			entityID = u.EntityID
		}
		var scopes []string
		if v := req.URL.Query().Get("scope"); v != "" {
			scopes = strings.Split(v, ",")
		}
		hits, err := s.Search(req.Context(), entityID, req.URL.Query().Get("q"), scopes)
		if err != nil {
			http.Error(w, `{"error":"search failed"}`, http.StatusInternalServerError)
			return
		}
		if hits == nil {
			hits = []Result{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hits)
	})
}
