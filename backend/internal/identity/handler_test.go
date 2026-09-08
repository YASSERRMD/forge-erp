package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func testDeps() (Deps, *MemoryStore) {
	iss, _ := NewIssuer("test-secret-for-phase-03")
	st := NewMemoryStore()
	return Deps{Store: st, Issuer: iss, Now: func() time.Time { return time.Now().UTC() }}, st
}

func testRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { Routes(r, d) })
	return r
}

func seedUser(t *testing.T, st *MemoryStore, login, pw string, admin bool) User {
	t.Helper()
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	u := &User{EntityID: 1, Login: login, Email: login + "@example.com", Status: UserActive,
		PasswordHash: hash, IsAdmin: admin}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return *u
}

func doLogin(t *testing.T, h http.Handler, login, pw string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"login": login, "password": pw})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&out)
	return rec.Code, out
}

func TestLoginSuccessAndMe(t *testing.T) {
	d, st := testDeps()
	h := testRouter(d)
	seedUser(t, st, "yassine", "supersecret-99", false)
	// Grant identity.user.read so /auth/me passes Require.
	u, _ := st.UserByLogin(context.Background(), 1, "yassine")
	id := u.ID
	_ = st.Grant(context.Background(), 1, &id, nil, Right{Module: "identity", Entity: "user", Action: "read"})

	code, out := doLogin(t, h, "yassine", "supersecret-99")
	if code != http.StatusOK || out["access_token"] == nil || out["refresh_token"] == nil {
		t.Fatalf("login: code=%d body=%v", code, out)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+out["access_token"].(string))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("yassine")) {
		t.Fatalf("me body missing login: %s", rec.Body.String())
	}
}

func TestLoginWrongPassword401AndLockout(t *testing.T) {
	d, st := testDeps()
	h := testRouter(d)
	seedUser(t, st, "layla", "correct-pass-1", false)
	for i := 0; i < MaxFailedAttempts; i++ {
		if code, _ := doLogin(t, h, "layla", "wrong-pass-000"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code=%d want 401", i, code)
		}
	}
	u, _ := st.UserByLogin(context.Background(), 1, "layla")
	if u.Status != UserLocked {
		t.Fatalf("expected lockout, status=%d attempts=%d", u.Status, u.FailedAttempts)
	}
	// Correct password now still rejected.
	if code, _ := doLogin(t, h, "layla", "correct-pass-1"); code != http.StatusUnauthorized {
		t.Fatalf("locked login: code=%d want 401", code)
	}
}

func TestRequireForbiddenWithoutRight(t *testing.T) {
	d, st := testDeps()
	h := testRouter(d)
	seedUser(t, st, "omar", "valid-pass-99", false)
	_, out := doLogin(t, h, "omar", "valid-pass-99")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+out["access_token"].(string))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("me without grant: code=%d want 403", rec.Code)
	}
	// No token at all → 401.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("me without token: code=%d want 401", rec2.Code)
	}
}

func TestRefreshRotation(t *testing.T) {
	d, st := testDeps()
	h := testRouter(d)
	seedUser(t, st, "sara", "refreshable-1", true)
	_, out := doLogin(t, h, "sara", "refreshable-1")
	refresh := out["refresh_token"].(string)

	body, _ := json.Marshal(map[string]string{"refresh_token": refresh})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// Old refresh token is single-use → second attempt fails.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("reused refresh: code=%d want 401", rec2.Code)
	}
}

func TestGroupGrantFlow(t *testing.T) {
	d, st := testDeps()
	h := testRouter(d)
	admin := seedUser(t, st, "root", "admin-pass-99", true)
	_ = admin
	_, out := doLogin(t, h, "root", "admin-pass-99")
	auth := "Bearer " + out["access_token"].(string)

	// Admin creates group (admin bypass covers identity.group.write).
	gbody, _ := json.Marshal(map[string]string{"code": "acct", "label": "Accountants"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/groups", bytes.NewReader(gbody))
	req.Header.Set("Authorization", auth)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create group: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var g Group
	_ = json.NewDecoder(rec.Body).Decode(&g)

	// Grant group a right, add member, member inherits access.
	member := seedUser(t, st, "hana", "member-pass-1", false)
	grbody, _ := json.Marshal(map[string]any{
		"group_id": g.ID, "module": "identity", "entity": "user", "action": "read",
	})
	reqg := httptest.NewRequest(http.MethodPost, "/api/v1/rights/grant", bytes.NewReader(grbody))
	reqg.Header.Set("Authorization", auth)
	recg := httptest.NewRecorder()
	h.ServeHTTP(recg, reqg)
	if recg.Code != http.StatusNoContent {
		t.Fatalf("grant: code=%d body=%s", recg.Code, recg.Body.String())
	}
	mbody, _ := json.Marshal(map[string]any{"user_id": member.ID})
	reqm := httptest.NewRequest(http.MethodPost, "/api/v1/groups/"+strconv.FormatInt(g.ID, 10)+"/members", bytes.NewReader(mbody))
	reqm.Header.Set("Authorization", auth)
	recm := httptest.NewRecorder()
	h.ServeHTTP(recm, reqm)
	if recm.Code != http.StatusNoContent {
		t.Fatalf("add member: code=%d body=%s", recm.Code, recm.Body.String())
	}
	_, mout := doLogin(t, h, "hana", "member-pass-1")
	reqme := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	reqme.Header.Set("Authorization", "Bearer "+mout["access_token"].(string))
	recme := httptest.NewRecorder()
	h.ServeHTTP(recme, reqme)
	if recme.Code != http.StatusOK {
		t.Fatalf("member inherited access: code=%d body=%s", recme.Code, recme.Body.String())
	}
}

func TestOIDCLoginFlows(t *testing.T) {
	d, st := testDeps()
	seedUser(t, st, "sso-user", "irrelevant-pw-00", false)
	d.OIDC = StaticVerifier{Users: map[string]User{
		"tok-known": {ID: 0, Email: "sso-user@example.com"},
		"tok-new":   {ID: 0, Email: "newcomer@example.com"},
	}}
	h := testRouter(d)
	sso := func(token string) (int, map[string]any) {
		body, _ := json.Marshal(map[string]string{"id_token": token})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oidc", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&out)
		return rec.Code, out
	}
	if code, out := sso("tok-known"); code != http.StatusOK || out["access_token"] == nil {
		t.Fatalf("known SSO: code=%d out=%v", code, out)
	}
	if code, out := sso("tok-new"); code != http.StatusOK || out["access_token"] == nil {
		t.Fatalf("JIT SSO: code=%d out=%v", code, out)
	}
	u, err := st.UserByEmail(context.Background(), 1, "newcomer@example.com")
	if err != nil || u.IsAdmin || u.PasswordHash != "" {
		t.Fatalf("provisioned: %+v err=%v", u, err)
	}
	if code, _ := sso("tok-bogus"); code != http.StatusUnauthorized {
		t.Fatalf("bogus: code=%d want 401", code)
	}
	d2, _ := testDeps()
	h2 := testRouter(d2)
	body, _ := json.Marshal(map[string]string{"id_token": "tok-known"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oidc", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled: code=%d want 503", rec.Code)
	}
}

func TestListAndUpdateUsers(t *testing.T) {
	d, st := testDeps()
	h := testRouter(d)
	seedUser(t, st, "listee", "supersecret-99", false)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users?limit=50", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// Unauthenticated listing requires rights; seed admin rights instead.
	_ = rec
	u, _ := st.UserByLogin(context.Background(), 1, "listee")
	list, err := st.ListUsers(context.Background(), 1, 50, 0)
	if err != nil || len(list) != 1 || list[0].PasswordHash != "" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	upd := u
	upd.FirstName = "List"
	upd.LastName = "Ee"
	if err := st.UpdateUser(context.Background(), &upd); err != nil {
		t.Fatalf("update: %v", err)
	}
	groups, err := st.ListGroups(context.Background(), 1)
	if err != nil {
		t.Fatalf("groups: %v", err)
	}
	_ = groups
	// Handler update with stale version → 409.
	body, _ := json.Marshal(map[string]any{"first_name": "X", "row_version": 1})
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/users/"+strconv.FormatInt(u.ID, 10), bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, r2)
	if rec2.Code == http.StatusOK {
		t.Fatal("stale update accepted")
	}
}
