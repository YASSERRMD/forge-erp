package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// Deps wires handlers to persistence, tokens, and the clock.
type Deps struct {
	Store  Store
	Issuer *Issuer
	Now    func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now().UTC()
}

// Routes mounts /auth, /users, /groups under r (caller nests at /api/v1).
func Routes(r chi.Router, d Deps) {
	h := &Handler{deps: d}
	r.Post("/auth/login", h.Login)
	r.Post("/auth/refresh", h.Refresh)
	r.Post("/auth/logout", h.Logout)
	r.With(h.Require("identity", "user", "read")).Get("/auth/me", h.Me)

	r.With(h.Require("identity", "user", "write")).Post("/users", h.CreateUser)
	r.With(h.Require("identity", "user", "read")).Get("/users/{id}", h.GetUser)
	r.With(h.Require("identity", "group", "write")).Post("/groups", h.CreateGroup)
	r.With(h.Require("identity", "group", "write")).Post("/groups/{id}/members", h.AddMember)
	r.With(h.Require("identity", "right", "write")).Post("/rights/grant", h.Grant)
}

// Handler implements the identity HTTP surface.
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

// ctxKey carries the authenticated user.
type ctxKey struct{}

// AuthUser returns the request's authenticated user (set by Require).
func AuthUser(r *http.Request) (User, bool) {
	u, ok := r.Context().Value(ctxKey{}).(User)
	return u, ok
}

// bearer extracts the access token.
func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

// Require enforces authentication + one rights triple (403 when the grant is missing).
func (h *Handler) Require(module, entity, action string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, err := h.deps.Issuer.Verify(bearer(r))
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			u, err := h.deps.Store.UserByID(r.Context(), claims.Subject)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			direct, inherited, err := h.deps.Store.ResolveRights(r.Context(), u)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "rights resolution failed")
				return
			}
			if !Can(u, direct, inherited, module, entity, action) {
				writeErr(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
		})
	}
}

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	EntityID int64  `json:"entity_id"`
}

type loginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// Login authenticates native credentials (Dolibarr functions_dolibarr.php equivalent:
// throttled, lockout after MaxFailedAttempts, disabled accounts rejected).
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decode(r, &req); err != nil || req.Login == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "login and password required")
		return
	}
	if req.EntityID == 0 {
		req.EntityID = 1
	}
	now := h.deps.now()
	u, err := h.deps.Store.UserByLogin(r.Context(), req.EntityID, req.Login)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err := LoginAllowed(u, now); err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	if u.PasswordHash == "" || !VerifyPassword(u.PasswordHash, req.Password) {
		updated, _ := RegisterFailure(u, now)
		_ = h.deps.Store.UpdateUser(r.Context(), &updated)
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	updated, ok := RegisterSuccess(u, now)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account unavailable")
		return
	}
	_ = h.deps.Store.UpdateUser(r.Context(), &updated)
	access, err := h.deps.Issuer.IssueAccess(updated)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token issue failed")
		return
	}
	refresh, hash, err := MintRefresh()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token issue failed")
		return
	}
	if err := h.deps.Store.CreateSession(r.Context(), updated.ID, hash, now.Add(RefreshTokenTTL)); err != nil {
		writeErr(w, http.StatusInternalServerError, "session create failed")
		return
	}
	writeJSON(w, http.StatusOK, loginResponse{AccessToken: access, RefreshToken: refresh,
		TokenType: "Bearer", ExpiresIn: int(AccessTokenTTL.Seconds())})
}

// Refresh rotates an opaque refresh token (single-use: old hash revoked).
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decode(r, &req); err != nil || req.RefreshToken == "" {
		writeErr(w, http.StatusBadRequest, "refresh_token required")
		return
	}
	sum := sha256hex(req.RefreshToken)
	now := h.deps.now()
	u, err := h.deps.Store.SessionUser(r.Context(), sum, now)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	_ = h.deps.Store.RevokeSession(r.Context(), sum)
	access, err := h.deps.Issuer.IssueAccess(u)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token issue failed")
		return
	}
	refresh, hash, err := MintRefresh()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token issue failed")
		return
	}
	if err := h.deps.Store.CreateSession(r.Context(), u.ID, hash, now.Add(RefreshTokenTTL)); err != nil {
		writeErr(w, http.StatusInternalServerError, "session create failed")
		return
	}
	writeJSON(w, http.StatusOK, loginResponse{AccessToken: access, RefreshToken: refresh,
		TokenType: "Bearer", ExpiresIn: int(AccessTokenTTL.Seconds())})
}

// Logout revokes the presented refresh token.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decode(r, &req); err != nil || req.RefreshToken == "" {
		writeErr(w, http.StatusBadRequest, "refresh_token required")
		return
	}
	_ = h.deps.Store.RevokeSession(r.Context(), sha256hex(req.RefreshToken))
	w.WriteHeader(http.StatusNoContent)
}

// Me returns the authenticated user profile.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	u, _ := AuthUser(r)
	u.PasswordHash = ""
	writeJSON(w, http.StatusOK, u)
}

type createUserRequest struct {
	Login     string `json:"login"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Password  string `json:"password"`
	IsAdmin   bool   `json:"is_admin"`
}

// CreateUser registers a native account (requires identity.user.write).
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decode(r, &req); err != nil || req.Login == "" {
		writeErr(w, http.StatusBadRequest, "login required")
		return
	}
	actor, _ := AuthUser(r)
	hash := ""
	if req.Password != "" {
		var err error
		if hash, err = HashPassword(req.Password); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	u := &User{EntityID: actor.EntityID, Login: req.Login, Email: req.Email,
		FirstName: req.FirstName, LastName: req.LastName, Status: UserActive,
		PasswordHash: hash, IsAdmin: req.IsAdmin, CreatedBy: &actor.ID, UpdatedBy: &actor.ID}
	if err := h.deps.Store.CreateUser(r.Context(), u); err != nil {
		writeErr(w, http.StatusConflict, "user exists")
		return
	}
	u.PasswordHash = ""
	writeJSON(w, http.StatusCreated, u)
}

// GetUser fetches one user (requires identity.user.read).
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	u, err := h.deps.Store.UserByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	u.PasswordHash = ""
	writeJSON(w, http.StatusOK, u)
}

type createGroupRequest struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

// CreateGroup creates a rights group (requires identity.group.write).
func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var req createGroupRequest
	if err := decode(r, &req); err != nil || req.Code == "" {
		writeErr(w, http.StatusBadRequest, "code required")
		return
	}
	actor, _ := AuthUser(r)
	g := &Group{EntityID: actor.EntityID, Code: req.Code, Label: req.Label}
	if err := h.deps.Store.CreateGroup(r.Context(), g); err != nil {
		writeErr(w, http.StatusConflict, "group exists")
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

// AddMember adds a user to a group (requires identity.group.write).
func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) {
	gid, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := decode(r, &req); err != nil || req.UserID == 0 {
		writeErr(w, http.StatusBadRequest, "user_id required")
		return
	}
	if err := h.deps.Store.AddMember(r.Context(), gid, req.UserID); err != nil {
		writeErr(w, http.StatusInternalServerError, "add member failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type grantRequest struct {
	UserID  *int64 `json:"user_id"`
	GroupID *int64 `json:"group_id"`
	Module  string `json:"module"`
	Entity  string `json:"entity"`
	Action  string `json:"action"`
}

// Grant issues one rights triple (requires identity.right.write; exactly one target).
func (h *Handler) Grant(w http.ResponseWriter, r *http.Request) {
	var req grantRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if (req.UserID == nil) == (req.GroupID == nil) {
		writeErr(w, http.StatusBadRequest, "exactly one of user_id / group_id required")
		return
	}
	actor, _ := AuthUser(r)
	if err := h.deps.Store.Grant(r.Context(), actor.EntityID, req.UserID, req.GroupID,
		Right{Module: req.Module, Entity: req.Entity, Action: req.Action}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
